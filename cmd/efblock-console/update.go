package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/x3Lo/efBlock/internal/update"
	"io"
	"net/http"
	"os"
	"strings"
)

const defaultUpdateAPI = update.DefaultAPI

const maxDownload = 200 << 20 // 200 Mo : garde-fou

// Ces éléments vivent dans internal/update (partagés avec l'interface web) ; ces alias gardent
// les noms utilisés ici.
type Release = update.Release

var (
	latestRelease      = update.LatestRelease
	acceptsPrereleases = update.AcceptsPrereleases
	semver             = update.Semver
	newerThan          = update.NewerThan
)

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
