package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNetConfigValidate(t *testing.T) {
	good := NetConfig{Iface: "ens33", Address: "192.168.1.10/24", Gateway: "192.168.1.1"}
	if err := good.Validate(); err != nil {
		t.Fatalf("configuration valide refusée: %v", err)
	}
	if err := (NetConfig{Iface: "ens33", DHCP: true}).Validate(); err != nil {
		t.Errorf("DHCP: %v", err)
	}

	tests := []struct {
		name string
		c    NetConfig
		want string
	}{
		{"sans carte", NetConfig{DHCP: true}, "carte"},
		{"nom de carte piégé", NetConfig{Iface: "ens33\n[Network]", DHCP: true}, "invalide"},
		{"adresse sans masque", NetConfig{Iface: "e", Address: "192.168.1.10", Gateway: "192.168.1.1"}, "adresse invalide"},
		{"adresse absurde", NetConfig{Iface: "e", Address: "pas-une-ip/24", Gateway: "192.168.1.1"}, "adresse invalide"},
		{"IPv6 non géré", NetConfig{Iface: "e", Address: "fd00::10/64", Gateway: "192.168.1.1"}, "adresse invalide"},
		{"masque trop petit", NetConfig{Iface: "e", Address: "192.168.1.10/2", Gateway: "192.168.1.1"}, "masque"},
		{"masque /32", NetConfig{Iface: "e", Address: "192.168.1.10/32", Gateway: "192.168.1.1"}, "masque"},
		{"passerelle absente", NetConfig{Iface: "e", Address: "192.168.1.10/24"}, "passerelle invalide"},
		{"passerelle hors réseau", NetConfig{Iface: "e", Address: "192.168.1.10/24", Gateway: "10.0.0.1"}, "n'est pas dans le réseau"},
		{"passerelle = machine", NetConfig{Iface: "e", Address: "192.168.1.10/24", Gateway: "192.168.1.10"}, "adresse de la machine"},
	}
	for _, tt := range tests {
		if err := tt.c.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("%s : erreur = %v, attendu un message contenant %q", tt.name, err, tt.want)
		}
	}
}

func TestNetConfigRender(t *testing.T) {
	u, err := NetConfig{Iface: "ens33", Address: "192.168.1.10/24", Gateway: "192.168.1.1"}.Render()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"[Match]\nName=ens33", "[Network]", "Address=192.168.1.10/24", "Gateway=192.168.1.1"} {
		if !strings.Contains(u, want) {
			t.Errorf("fichier networkd sans %q:\n%s", want, u)
		}
	}
	if strings.Contains(u, "DHCP=") || isDHCP(u) {
		t.Error("adresse fixe : pas de DHCP")
	}

	d, _ := NetConfig{Iface: "ens33", DHCP: true}.Render()
	if !strings.Contains(d, "DHCP=ipv4") || !isDHCP(d) || strings.Contains(d, "Gateway=") {
		t.Errorf("DHCP:\n%s", d)
	}
	if _, err := (NetConfig{Iface: "e", Address: "x", Gateway: "y"}).Render(); err == nil {
		t.Error("Render doit refuser une configuration invalide")
	}
}

func TestParseGateway(t *testing.T) {
	route := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"ens36\t00000000\t0132A8C0\t0003\t0\t0\t0\t00000000\t0\t0\t0\n" + // 192.168.50.1 (octets inversés)
		"ens36\t0032A8C0\t00000000\t0001\t0\t0\t0\t00FFFFFF\t0\t0\t0\n"
	if gw := parseGateway(route); gw != "192.168.50.1" {
		t.Errorf("passerelle = %q, attendu 192.168.50.1", gw)
	}
	noDefault := "Iface\tDestination\tGateway\nens36\t0032A8C0\t00000000\n"
	if gw := parseGateway(noDefault); gw != "" {
		t.Errorf("sans route par défaut: %q", gw)
	}
	if parseGateway("") != "" || parseGateway("entête seule\n") != "" {
		t.Error("entrée vide ou tronquée")
	}
}

func TestListenHelpers(t *testing.T) {
	if listenPort(":8080") != "8080" || listenPort("192.168.1.10:53") != "53" || listenPort("n'importe quoi") != "" {
		t.Error("listenPort")
	}
	if hostOf(":8080", "192.168.1.5") != "192.168.1.5" || hostOf("0.0.0.0:80", "192.168.1.5") != "192.168.1.5" {
		t.Error("écoute sur toutes les adresses : on affiche celle de la machine")
	}
	if hostOf("10.0.0.2:80", "192.168.1.5") != "10.0.0.2" {
		t.Error("écoute précise : on affiche cette adresse")
	}
}

func TestSemverAndNewerThan(t *testing.T) {
	tests := []struct {
		cur, latest string
		want        bool
	}{
		{"1.2.3", "v1.2.4", true},
		{"1.2.3", "v1.3.0", true},
		{"1.2.3", "v2.0.0", true},
		{"1.2.3", "v1.2.3", false},
		{"1.10.0", "v1.9.0", false}, // 10 > 9 : comparaison numérique, pas alphabétique
		{"2.0.0", "v1.9.9", false},
		{"dev", "v0.1.0", true}, // une version de développement est plus ancienne que toute publication
		{"1.2.3", "n'importe quoi", false},
		{"v1.2.3-beta", "v1.2.4", true},
	}
	for _, tt := range tests {
		if got := newerThan(tt.cur, tt.latest); got != tt.want {
			t.Errorf("newerThan(%q, %q) = %v, attendu %v", tt.cur, tt.latest, got, tt.want)
		}
	}
}

func TestAssetURLsAndExpectedSum(t *testing.T) {
	rel := Release{TagName: "v1.0.0"}
	add := func(name string) {
		rel.Assets = append(rel.Assets, struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		}{name, "https://exemple/" + name})
	}
	add("efblock_1.0.0_amd64.deb")
	add("efblock_1.0.0_arm64.deb")
	add("efblock-1.0.0-amd64.iso")
	add("SHA256SUMS")

	name, url, sums, err := assetURLs(rel, "arm64")
	if err != nil || name != "efblock_1.0.0_arm64.deb" || !strings.HasSuffix(url, "_arm64.deb") || !strings.HasSuffix(sums, "SHA256SUMS") {
		t.Errorf("assetURLs = %q %q %q %v", name, url, sums, err)
	}
	if _, _, _, err := assetURLs(rel, "riscv64"); err == nil {
		t.Error("architecture absente : erreur attendue")
	}
	rel.Assets = rel.Assets[:3] // plus de SHA256SUMS
	if _, _, _, err := assetURLs(rel, "amd64"); err == nil || !strings.Contains(err.Error(), "SHA256SUMS") {
		t.Errorf("sans empreintes, la mise à jour doit être refusée: %v", err)
	}

	h := strings.Repeat("ab", 32)
	if got, err := expectedSum(h+"  fichier.deb\n"+strings.Repeat("cd", 32)+" *autre.deb\n", "autre.deb"); err != nil || got != strings.Repeat("cd", 32) {
		t.Errorf("expectedSum = %q %v", got, err)
	}
	if _, err := expectedSum(h+"  fichier.deb\n", "absent.deb"); err == nil {
		t.Error("fichier absent de la liste")
	}
	if _, err := expectedSum("court  fichier.deb\n", "fichier.deb"); err == nil {
		t.Error("empreinte mal formée")
	}
}

// releaseServer simule GitHub : l'API, le fichier .deb et SHA256SUMS.
func releaseServer(t *testing.T, payload []byte, sumOf []byte) *httptest.Server {
	t.Helper()
	sum := sha256.Sum256(sumOf)
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[{"tag_name":"v1.5.0","prerelease":false,"draft":false,"assets":[
			{"name":"efblock_1.5.0_amd64.deb","browser_download_url":"%s/deb"},
			{"name":"SHA256SUMS","browser_download_url":"%s/sums"}]}]`, srv.URL, srv.URL)
	})
	mux.HandleFunc("/deb", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  efblock_1.5.0_amd64.deb\n", hex.EncodeToString(sum[:]))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestUpdateDownloadIsVerified(t *testing.T) {
	payload := []byte("contenu fictif du paquet")
	srv := releaseServer(t, payload, payload)
	ctx := context.Background()

	rel, err := latestRelease(ctx, srv.Client(), srv.URL+"/api", false)
	if err != nil || rel.TagName != "v1.5.0" {
		t.Fatalf("latestRelease: %+v %v", rel, err)
	}
	name, debURL, sumsURL, err := assetURLs(rel, "amd64")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path, err := downloadVerified(ctx, srv.Client(), name, debURL, sumsURL, dir)
	if err != nil {
		t.Fatalf("un téléchargement conforme doit être accepté: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(payload) {
		t.Error("contenu du fichier téléchargé incorrect")
	}
}

// Un fichier dont l'empreinte ne correspond pas doit être rejeté ET supprimé.
func TestUpdateRejectsTamperedFile(t *testing.T) {
	srv := releaseServer(t, []byte("fichier modifié par quelqu'un"), []byte("le vrai fichier"))
	rel, _ := latestRelease(context.Background(), srv.Client(), srv.URL+"/api", false)
	name, debURL, sumsURL, _ := assetURLs(rel, "amd64")
	dir := t.TempDir()

	path, err := downloadVerified(context.Background(), srv.Client(), name, debURL, sumsURL, dir)
	if err == nil || !strings.Contains(err.Error(), "empreinte") {
		t.Fatalf("un fichier altéré doit être rejeté: %q %v", path, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
		t.Errorf("le fichier rejeté ne doit pas rester sur le disque: %v", left)
	}
}

func TestLatestReleaseErrors(t *testing.T) {
	serve := func(code int, body string) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			w.Write([]byte(body))
		}))
	}
	cases := []struct {
		name string
		code int
		body string
		want string
	}{
		{"HTTP 403", 403, "x", "403"},
		{"réponse qui n'est pas une liste", 200, `{"autre":1}`, "inattendue"},
		{"liste vide", 200, `[]`, "aucune version"},
	}
	for _, tc := range cases {
		srv := serve(tc.code, tc.body)
		_, err := latestRelease(context.Background(), srv.Client(), srv.URL, true)
		srv.Close()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s : erreur = %v, attendu un message contenant %q", tc.name, err, tc.want)
		}
	}
}

// GitHub renvoie les versions de la plus récente à la plus ancienne : on prend la première utilisable.
func TestLatestReleaseSelection(t *testing.T) {
	list := `[
		{"tag_name":"v2.0.0","draft":true,"prerelease":false,"assets":[{"name":"efblock_1_amd64.deb","browser_download_url":"x"},{"name":"SHA256SUMS","browser_download_url":"y"}]},
		{"tag_name":"v1.9.5","draft":false,"prerelease":true},
		{"tag_name":"v1.9.0","draft":false,"prerelease":true,"assets":[{"name":"efblock_1_amd64.deb","browser_download_url":"x"},{"name":"SHA256SUMS","browser_download_url":"y"}]},
		{"tag_name":"v1.8.0","draft":false,"prerelease":false,"assets":[{"name":"efblock_1_amd64.deb","browser_download_url":"x"},{"name":"SHA256SUMS","browser_download_url":"y"}]},
		{"tag_name":"v1.7.0","draft":false,"prerelease":false,"assets":[{"name":"efblock_1_amd64.deb","browser_download_url":"x"},{"name":"SHA256SUMS","browser_download_url":"y"}]}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(list)) }))
	defer srv.Close()
	ctx := context.Background()

	r, err := latestRelease(ctx, srv.Client(), srv.URL, true)
	if err != nil || r.TagName != "v1.9.0" {
		t.Errorf("pré-versions acceptées : %q %v, attendu v1.9.0 (ni le brouillon v2.0.0, ni v1.9.5 sans paquet)", r.TagName, err)
	}
	r, err = latestRelease(ctx, srv.Client(), srv.URL, false)
	if err != nil || r.TagName != "v1.8.0" {
		t.Errorf("version stable seulement : %q %v, attendu v1.8.0", r.TagName, err)
	}

	// Le cas réel d'aujourd'hui : il n'existe que des pré-versions (0.x).
	only := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"tag_name":"v0.1.0","draft":false,"prerelease":true,"assets":[{"name":"efblock_1_amd64.deb","browser_download_url":"x"},{"name":"SHA256SUMS","browser_download_url":"y"}]}]`))
	}))
	defer only.Close()
	if r, err := latestRelease(ctx, only.Client(), only.URL, true); err != nil || r.TagName != "v0.1.0" {
		t.Errorf("une installation en 0.x doit voir la pré-version v0.1.0 : %q %v", r.TagName, err)
	}
	if _, err := latestRelease(ctx, only.Client(), only.URL, false); err == nil {
		t.Error("une installation stable ne doit pas recevoir une pré-version")
	}
}

func TestAcceptsPrereleases(t *testing.T) {
	for v, want := range map[string]bool{
		"dev": true, "0.1.0": true, "v0.9.3": true, "garbage": true, // avant la 1.0, ou version inconnue
		"1.0.0": false, "2.3.1": false, "v1.2.3": false, // versions stables
	} {
		if got := acceptsPrereleases(v); got != want {
			t.Errorf("acceptsPrereleases(%q) = %v, attendu %v", v, got, want)
		}
	}
}

func TestGenSelfSigned(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, err := genSelfSigned(dir, []net.IP{net.ParseIP("192.168.1.10")}, []string{"efblock"}, time.Hour*24)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(keyPath); fi.Mode().Perm() != 0o640 {
		t.Errorf("la clé doit être en 0640 (pas lisible par tous): %v", fi.Mode().Perm())
	}
	// Le couple certificat/clé doit être utilisable par un serveur HTTPS.
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("certificat et clé inutilisables: %v", err)
	}
	pemBytes, _ := os.ReadFile(certPath)
	block, _ := pem.Decode(pemBytes)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.IPAddresses) != 1 || cert.IPAddresses[0].String() != "192.168.1.10" || len(cert.DNSNames) != 1 || cert.DNSNames[0] != "efblock" {
		t.Errorf("adresses du certificat: %v %v", cert.IPAddresses, cert.DNSNames)
	}
	if cert.NotAfter.Before(time.Now()) || cert.NotBefore.After(time.Now()) {
		t.Error("période de validité incorrecte")
	}
	_ = pair
	if _, _, err := genSelfSigned(t.TempDir(), nil, nil, time.Hour); err == nil {
		t.Error("sans adresse ni nom, le certificat n'a aucun intérêt : erreur attendue")
	}
}

func TestConfirmOuiNon(t *testing.T) {
	cases := []struct {
		in   string
		fn   func(*UI, string) bool
		want bool
	}{
		{"oui\n", (*UI).confirm, true},
		{"\n", (*UI).confirm, false},
		{"peut-etre\nnon\n", (*UI).confirm, false},
		{"bof\no\n", (*UI).confirm, true},
		{"\n", (*UI).confirmYes, true},
		{"non\n", (*UI).confirmYes, false},
		{"", (*UI).confirmYes, false},
	}
	for _, c := range cases {
		out, err := os.CreateTemp(t.TempDir(), "out")
		if err != nil {
			t.Fatal(err)
		}
		ui := &UI{in: bufio.NewReader(strings.NewReader(c.in)), out: out}
		if got := c.fn(ui, "Question ?"); got != c.want {
			t.Errorf("entrée %q : %v, attendu %v", c.in, got, c.want)
		}
	}
}

func TestFirstBoot(t *testing.T) {
	dir := t.TempDir()
	firstBootFile = filepath.Join(dir, "marqueur")
	newUI := func(in string) *UI {
		out, err := os.CreateTemp(dir, "out")
		if err != nil {
			t.Fatal(err)
		}
		return &UI{in: bufio.NewReader(strings.NewReader(in)), out: out}
	}

	// Entrée fermée avant la fin : on ne marque pas la configuration comme faite.
	firstBoot(newUI(""))
	if _, err := os.Stat(firstBootFile); err == nil {
		t.Fatal("marqueur écrit alors que rien n'a été répondu")
	}

	// non à tout : le marqueur est écrit, et on ne repose plus les questions.
	firstBoot(newUI("non\nnon\n\n"))
	if _, err := os.Stat(firstBootFile); err != nil {
		t.Fatalf("marqueur absent : %v", err)
	}
	ui := newUI("")
	firstBoot(ui)
	if fi, _ := ui.out.Stat(); fi.Size() != 0 {
		t.Error("les questions ont été reposées")
	}
}

// Régression : l'écran de bienvenue doit s'afficher tout de suite, sans attendre une touche
// (un Peek bloquant laissait l'écran noir avec un simple curseur au premier démarrage).
func TestFirstBootAffichageImmediat(t *testing.T) {
	dir := t.TempDir()
	firstBootFile = filepath.Join(dir, "marqueur")
	out, err := os.CreateTemp(dir, "out")
	if err != nil {
		t.Fatal(err)
	}
	pr, pw := io.Pipe() // entrée ouverte mais silencieuse, comme un clavier auquel on ne touche pas
	ui := &UI{in: bufio.NewReader(pr), out: out}
	done := make(chan struct{})
	go func() { firstBoot(ui); close(done) }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		b, _ := os.ReadFile(out.Name())
		if strings.Contains(string(b), "Bienvenue") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("rien affiché tant qu'aucune touche n'est tapée")
		}
		time.Sleep(20 * time.Millisecond)
	}
	pw.Close() // l'entrée se ferme : fin propre, sans marqueur
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("firstBoot ne se termine pas à la fermeture de l'entrée")
	}
	if _, err := os.Stat(firstBootFile); err == nil {
		t.Error("marqueur écrit alors que l'entrée s'est fermée")
	}
}

func TestApplyUpdate(t *testing.T) {
	dir := t.TempDir()
	updateRequestFile = filepath.Join(dir, "demande")
	updateStatusFile = filepath.Join(dir, "etat")
	updateLogFile = filepath.Join(dir, "journal")
	defer func(f func(*UI, bool) error) { runUpdateFn = f }(runUpdateFn)
	read := func() map[string]string {
		var m map[string]string
		b, err := os.ReadFile(updateStatusFile)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	// Succès : la demande est effacée AVANT la mise à jour, l'état passe par « running » puis « ok ».
	os.WriteFile(updateRequestFile, []byte("x"), 0o644)
	runUpdateFn = func(ui *UI, yes bool) error {
		if _, err := os.Stat(updateRequestFile); err == nil {
			t.Error("la demande doit être effacée avant la mise à jour (sinon boucle systemd)")
		}
		if !yes {
			t.Error("la mise à jour demandée depuis l'interface ne doit pas poser de question")
		}
		if m := read(); m["state"] != "running" {
			t.Errorf("état pendant la mise à jour : %v", m)
		}
		ui.printf("ligne de journal\n")
		return nil
	}
	if code := applyUpdate(); code != 0 {
		t.Errorf("code de sortie %d", code)
	}
	if m := read(); m["state"] != "ok" {
		t.Errorf("état final : %v", m)
	}
	if b, _ := os.ReadFile(updateLogFile); !strings.Contains(string(b), "ligne de journal") {
		t.Errorf("journal : %q", b)
	}

	// Échec : l'état « failed » porte le message.
	runUpdateFn = func(ui *UI, yes bool) error { return errors.New("empreinte incorrecte") }
	if code := applyUpdate(); code == 0 {
		t.Error("un échec doit donner un code de sortie non nul")
	}
	if m := read(); m["state"] != "failed" || !strings.Contains(m["message"], "empreinte") {
		t.Errorf("état après échec : %v", m)
	}
}
