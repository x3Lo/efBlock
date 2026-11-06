// Package update cherche les nouvelles versions d'efBlock publiées sur GitHub. Il est partagé
// par la console (qui les installe) et par l'interface web (qui les signale et en demande l'installation).
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// DefaultAPI : publications du dépôt, de la plus récente à la plus ancienne.
//
// On n'utilise PAS ".../releases/latest" : GitHub y ignore les pré-versions, donc tant que toutes
// les versions sont des 0.x (publiées comme pré-versions), la mise à jour ne trouverait rien.
const DefaultAPI = "https://api.github.com/repos/x3Lo/efBlock/releases?per_page=20"

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

// HasPackage : la publication contient au moins un paquet .deb et son SHA256SUMS.
func (r Release) HasPackage() bool {
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

// LatestRelease interroge l'API GitHub et retourne la publication la plus récente utilisable :
// jamais un brouillon, et une pré-version seulement si allowPre est vrai.
func LatestRelease(ctx context.Context, c *http.Client, api string, allowPre bool) (Release, error) {
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
		if r.Draft || r.TagName == "" || (r.Prerelease && !allowPre) || !r.HasPackage() {
			continue // une publication sans paquet (construction ratée) n'est pas une mise à jour
		}
		return r, nil
	}
	return Release{}, errors.New("aucune version publiée trouvée")
}

// AcceptsPrereleases indique si cette installation suit aussi les pré-versions : c'est le cas
// d'une version de développement ou d'une version 0.x (avant la 1.0). Une installation en version
// stable (1.0 et après) ne reçoit que des versions stables.
func AcceptsPrereleases(current string) bool {
	v, ok := Semver(current)
	return !ok || v[0] == 0
}

// Semver : trois nombres d'une version ("v1.2.3" ou "1.2.3").
func Semver(v string) ([3]int, bool) {
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

// NewerThan indique si latest est strictement plus récente que current.
// Une version de développement ("dev") est considérée comme plus ancienne que toute publication.
func NewerThan(current, latest string) bool {
	l, ok := Semver(latest)
	if !ok {
		return false
	}
	c, ok := Semver(current)
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
