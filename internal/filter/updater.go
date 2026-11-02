package filter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Source décrit une liste de filtres : une URL http(s), ou un chemin de fichier local.
type Source struct {
	Name string
	URL  string
}

// maxListSize protège la VM contre une URL qui renverrait un fichier énorme.
const maxListSize = 100 << 20 // 100 Mo

// Updater télécharge les listes, les garde en cache sur disque et alimente l'Engine.
type Updater struct {
	engine   *Engine
	sources  []Source
	dir      string // dossier de cache des listes
	interval time.Duration
	client   *http.Client
}

// NewUpdater crée un Updater. dir est créé si besoin.
func NewUpdater(engine *Engine, sources []Source, dir string, interval time.Duration) *Updater {
	return &Updater{
		engine:   engine,
		sources:  sources,
		dir:      dir,
		interval: interval,
		client:   &http.Client{Timeout: 2 * time.Minute},
	}
}

// cachePath : nom de fichier stable dérivé de l'URL.
func (u *Updater) cachePath(s Source) string {
	sum := sha256.Sum256([]byte(s.URL))
	return filepath.Join(u.dir, hex.EncodeToString(sum[:6])+".txt")
}

func isRemote(url string) bool {
	return strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
}

// Run charge d'abord les listes en cache (démarrage instantané, même sans
// internet), puis les met à jour tout de suite et à chaque intervalle.
// Elle bloque jusqu'à l'annulation de ctx : à lancer dans une goroutine.
func (u *Updater) Run(ctx context.Context) {
	if err := u.Rebuild(); err != nil {
		slog.Warn("chargement du cache des listes", "err", err)
	}
	for {
		if err := u.Update(ctx); err != nil {
			slog.Warn("mise à jour des listes", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(u.interval):
		}
	}
}

// Update télécharge toutes les listes puis reconstruit l'index.
// Si une liste échoue, la copie en cache (plus ancienne) est conservée :
// une panne réseau ne vide jamais le filtre.
func (u *Updater) Update(ctx context.Context) error {
	if err := os.MkdirAll(u.dir, 0o755); err != nil {
		return err
	}
	var errs []error
	for _, s := range u.sources {
		if !isRemote(s.URL) {
			continue // fichier local : lu tel quel par Rebuild
		}
		if err := u.download(ctx, s); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
			slog.Warn("téléchargement échoué, copie en cache conservée", "liste", s.Name, "err", err)
		}
	}
	if err := u.Rebuild(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// download récupère une liste et l'écrit dans le cache de façon atomique
// (fichier temporaire puis renommage : jamais de fichier à moitié écrit).
func (u *Updater) download(ctx context.Context, s Source) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.URL, nil)
	if err != nil {
		return err
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	dst := u.cachePath(s)
	tmp, err := os.CreateTemp(u.dir, "dl-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // sans effet si le renommage a réussi

	n, err := io.Copy(tmp, io.LimitReader(resp.Body, maxListSize+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > maxListSize {
		return fmt.Errorf("liste trop grosse (> %d Mo)", maxListSize>>20)
	}
	return os.Rename(tmp.Name(), dst)
}

// Rebuild construit un nouvel index depuis les fichiers disponibles
// (cache pour les URL, fichier direct pour les chemins locaux) et le publie.
func (u *Updater) Rebuild() error {
	idx := &listIndex{block: Set{}, allow: Set{}}
	var errs []error

	for _, s := range u.sources {
		path := s.URL
		if isRemote(s.URL) {
			path = u.cachePath(s)
		}
		f, err := os.Open(path)
		if err != nil {
			if !(isRemote(s.URL) && errors.Is(err, os.ErrNotExist)) {
				errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
			}
			continue // pas encore téléchargée : normal au premier démarrage
		}
		nb, na := 0, 0
		err = ParseReader(f, func(domain string, allow bool) {
			if allow {
				idx.allow.Add(domain)
				na++
			} else {
				idx.block.Add(domain)
				nb++
			}
		})
		f.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
			continue
		}
		slog.Info("liste chargée", "liste", s.Name, "bloqués", nb, "exceptions", na)
	}

	u.engine.setLists(idx)
	slog.Info("index des listes publié", "domaines_bloqués", len(idx.block), "exceptions", len(idx.allow))
	return errors.Join(errs...)
}
