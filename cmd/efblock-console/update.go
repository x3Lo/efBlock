package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Publications du dépôt, de la plus récente à la plus ancienne.
//
// On n'utilise PAS ".../releases/latest" : GitHub y ignore les pré-versions, donc tant que toutes
// les versions sont des 0.x (publiées comme pré-versions), la mise à jour ne trouverait rien.
const defaultUpdateAPI = "https://api.github.com/repos/x3Lo/efBlock/releases?per_page=20"

const maxDownload = 200 << 20 // 200 Mo : garde-fou

// Release décrit une version publiée.
type Release struct {
	TagName    string `json:"tag_name"`
	Prerelease bool   `json:"prerelease"`
	Draft      bool   `json:"draft"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// hasPackage : la publication contient au moins un paquet .deb et son SHA256SUMS.
func (r Release) hasPackage() bool {
	deb, sums := false, false
	for _, a := range r.Assets {
		switch {
		case a.Name == "SHA256SUMS":
			sums = true
		case strings.HasSuffix(a.Name, ".deb"):
			deb = true
		}
	}
	return deb && sums
}

// latestRelease interroge l'API GitHub et retourne la publication la plus récente utilisable :
// jamais un brouillon, et une pré-version seulement si allowPre est vrai.
func latestRelease(ctx context.Context, c *http.Client, api string, allowPre bool) (Release, error) {
	var rels []Release
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "efblock-console")
	resp, err := c.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub a répondu HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rels); err != nil {
		return Release{}, fmt.Errorf("réponse GitHub inattendue : %w", err)
	}
	for _, r := range rels { // GitHub les renvoie de la plus récente à la plus ancienne
		if r.Draft || r.TagName == "" || (r.Prerelease && !allowPre) || !r.hasPackage() {
			continue // une publication sans paquet (construction ratée) n'est pas une mise à jour
		}
		return r, nil
	}
	return Release{}, errors.New("aucune version publiée trouvée")
}

// acceptsPrereleases indique si cette installation suit aussi les pré-versions : c'est le cas
// d'une version de développement ou d'une version 0.x (avant la 1.0). Une installation en version
// stable (1.0 et après) ne reçoit que des versions stables.
func acceptsPrereleases(current string) bool {
	v, ok := semver(current)
	return !ok || v[0] == 0
}

// semver : trois nombres d'une version ("v1.2.3" ou "1.2.3").
func semver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newerThan indique si latest est strictement plus récente que current.
// Une version de développement ("dev") est considérée comme plus ancienne que toute publication.
func newerThan(current, latest string) bool {
	l, ok := semver(latest)
	if !ok {
		return false
	}
	c, ok := semver(current)
	if !ok {
		return true
	}
	for i := range l {
		if l[i] != c[i] {
			return l[i] > c[i]
		}
	}
	return false
}

// assetURLs retrouve, dans une publication, le paquet pour cette architecture et la liste
// des empreintes SHA-256.
func assetURLs(rel Release, arch string) (debName, debURL, sumsURL string, err error) {
	suffix := "_" + arch + ".deb"
	for _, a := range rel.Assets {
		switch {
		case strings.HasPrefix(a.Name, "efblock_") && strings.HasSuffix(a.Name, suffix):
			debName, debURL = a.Name, a.URL
		case a.Name == "SHA256SUMS":
			sumsURL = a.URL
		}
	}
	if debURL == "" {
		return "", "", "", fmt.Errorf("aucun paquet pour l'architecture %s dans cette publication", arch)
	}
	if sumsURL == "" {
		return "", "", "", errors.New("la publication n'a pas de fichier SHA256SUMS : mise à jour refusée")
	}
	return debName, debURL, sumsURL, nil
}

func fetch(ctx context.Context, c *http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "efblock-console")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d pour %s", resp.StatusCode, url)
	}
	return resp.Body, nil
}

// downloadVerified télécharge le paquet, et ne le garde QUE si son empreinte SHA-256 est celle
// annoncée dans SHA256SUMS. Retourne le chemin du fichier.
//
// L'empreinte protège d'un téléchargement corrompu ou tronqué. Elle ne protège pas d'un compte
// GitHub piraté (l'attaquant publierait aussi de fausses empreintes) : fais confiance à la
// source, comme pour tout logiciel.
func downloadVerified(ctx context.Context, c *http.Client, debName, debURL, sumsURL, dir string) (string, error) {
	sumsBody, err := fetch(ctx, c, sumsURL)
	if err != nil {
		return "", err
	}
	sums, err := io.ReadAll(io.LimitReader(sumsBody, 1<<20))
	sumsBody.Close()
	if err != nil {
		return "", err
	}
	want, err := expectedSum(string(sums), debName)
	if err != nil {
		return "", err
	}

	body, err := fetch(ctx, c, debURL)
	if err != nil {
		return "", err
	}
	defer body.Close()
	f, err := os.CreateTemp(dir, "efblock-update-*.deb")
	if err != nil {
		return "", err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, maxDownload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil || n > maxDownload {
		os.Remove(f.Name())
		if err == nil {
			err = errors.New("fichier trop gros")
		}
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(f.Name())
		return "", fmt.Errorf("empreinte incorrecte (attendue %s, reçue %s) : fichier rejeté", want[:12], got[:12])
	}
	return f.Name(), nil
}

// expectedSum lit l'empreinte d'un fichier dans un SHA256SUMS ("<empreinte>  <nom>").
func expectedSum(sums, name string) (string, error) {
	sc := bufio.NewScanner(strings.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name {
			if len(f[0]) != 64 {
				return "", errors.New("empreinte mal formée")
			}
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("%s ne figure pas dans SHA256SUMS", name)
}
