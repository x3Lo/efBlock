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
	"sync"
	"sync/atomic"
	"time"
)

// Source décrit une liste de filtres : une URL http(s), ou un chemin de fichier local.
type Source struct {
	Name string
	URL  string
}

// ListStatus décrit l'état d'une liste chargée (affiché dans l'interface web).
type ListStatus struct {
	Name       string
	URL        string
	Blocked    int       // domaines à bloquer apportés par cette liste
	Exceptions int       // exceptions (@@) apportées par cette liste
	UpdatedAt  time.Time // dernière modification du fichier en cache
	Error      string    // dernière erreur (téléchargement ou lecture), vide si tout va bien
}

// maxListSize protège la VM contre une URL qui renverrait un fichier énorme.
const maxListSize = 100 << 20 // 100 Mo

// Garde-fous contre une liste corrompue. Un serveur en panne renvoie parfois une
// page d'erreur HTML avec le code 200 : sans contrôle, elle remplacerait une bonne
// liste par... rien, sans aucune erreur, et la copie fautive survivrait au redémarrage.
const (
	minOldForRatio = 100 // en dessous, on ne compare pas à l'ancienne version (petite liste perso)
	minKeepRatio   = 0.3 // une mise à jour qui garde moins de 30 % des domaines est rejetée
)

// Updater télécharge les listes, les garde en cache sur disque et alimente l'Engine.
// La liste des sources peut changer à chaud (SetSources), depuis l'interface web.
type Updater struct {
	engine   *Engine
	dir      string       // dossier de cache des listes
	interval atomic.Int64 // durée entre deux mises à jour (nanosecondes) ; modifiable à chaud
	client   *http.Client

	mu      sync.Mutex // protège sources et lastErr
	sources []Source
	lastErr map[string]string // URL -> dernière erreur de téléchargement

	buildMu sync.Mutex // une seule mise à jour à la fois
	trigger chan struct{}
	status  atomic.Pointer[[]ListStatus]
}

// NewUpdater crée un Updater. dir est créé si besoin.
func NewUpdater(engine *Engine, sources []Source, dir string, interval time.Duration) *Updater {
	u := &Updater{
		engine:  engine,
		sources: sources,
		dir:     dir,
		client:  &http.Client{Timeout: 2 * time.Minute},
		lastErr: map[string]string{},
		trigger: make(chan struct{}, 1),
	}
	u.interval.Store(int64(interval))
	return u
}

// SetInterval change la fréquence des mises à jour (prise en compte au prochain cycle).
func (u *Updater) SetInterval(d time.Duration) { u.interval.Store(int64(d)) }

// SetSources remplace la liste des sources. Appelle Trigger() ensuite pour
// télécharger les nouvelles.
func (u *Updater) SetSources(sources []Source) {
	u.mu.Lock()
	u.sources = append([]Source(nil), sources...)
	u.mu.Unlock()
}

func (u *Updater) currentSources() []Source {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]Source(nil), u.sources...)
}

// Trigger demande une mise à jour immédiate (sans attendre l'intervalle).
// Ne bloque jamais : si une demande est déjà en attente, celle-ci suffit.
func (u *Updater) Trigger() {
	select {
	case u.trigger <- struct{}{}:
	default:
	}
}

// Status retourne l'état de chaque liste.
func (u *Updater) Status() []ListStatus {
	if p := u.status.Load(); p != nil {
		return append([]ListStatus(nil), *p...)
	}
	return nil
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
// internet), puis les met à jour tout de suite, à chaque intervalle, et à
// chaque Trigger(). Elle bloque jusqu'à l'annulation de ctx : à lancer dans
// une goroutine.
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
		case <-time.After(time.Duration(u.interval.Load())):
		case <-u.trigger:
		}
	}
}

// Update télécharge toutes les listes puis reconstruit l'index.
// Si une liste échoue, la copie en cache (plus ancienne) est conservée :
// une panne réseau ne vide jamais le filtre.
func (u *Updater) Update(ctx context.Context) error {
	u.buildMu.Lock()
	defer u.buildMu.Unlock()

	if err := os.MkdirAll(u.dir, 0o755); err != nil {
		return err
	}
	var errs []error
	for _, s := range u.currentSources() {
		if !isRemote(s.URL) {
			continue // fichier local : lu tel quel par rebuild
		}
		err := u.download(ctx, s)
		u.mu.Lock()
		if err != nil {
			u.lastErr[s.URL] = err.Error()
		} else {
			delete(u.lastErr, s.URL)
		}
		u.mu.Unlock()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
			slog.Warn("téléchargement échoué, copie en cache conservée", "liste", s.Name, "err", err)
		}
	}
	if err := u.rebuild(); err != nil {
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

	// Contrôle de plausibilité AVANT de remplacer la copie en cache.
	got, err := countRules(tmp.Name())
	if err != nil {
		return err
	}
	if got == 0 {
		return errors.New("liste rejetée : aucun domaine reconnu (le serveur a probablement renvoyé une page d'erreur)")
	}
	if old, err := countRules(dst); err == nil && old >= minOldForRatio && float64(got) < float64(old)*minKeepRatio {
		return fmt.Errorf("liste rejetée : %d domaines contre %d auparavant (baisse de plus de %d %%), copie précédente conservée",
			got, old, int((1-minKeepRatio)*100))
	}
	return os.Rename(tmp.Name(), dst)
}

// countRules compte les règles (blocage et exceptions) d'un fichier de liste.
func countRules(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n := 0
	err = ParseReader(f, func(string, bool) { n++ })
	return n, err
}

// Rebuild construit un nouvel index depuis les fichiers disponibles
// (cache pour les URL, fichier direct pour les chemins locaux) et le publie.
func (u *Updater) Rebuild() error {
	u.buildMu.Lock()
	defer u.buildMu.Unlock()
	return u.rebuild()
}

func (u *Updater) rebuild() error {
	idx := &listIndex{}
	var errs []error
	var statuses []ListStatus
	total := 0

	for _, s := range u.currentSources() {
		st := ListStatus{Name: s.Name, URL: s.URL}
		u.mu.Lock()
		st.Error = u.lastErr[s.URL]
		u.mu.Unlock()

		path := s.URL
		if isRemote(s.URL) {
			path = u.cachePath(s)
		}
		f, err := os.Open(path)
		if err != nil {
			if !(isRemote(s.URL) && errors.Is(err, os.ErrNotExist)) {
				errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
				st.Error = err.Error()
			} // sinon : pas encore téléchargée, normal au premier démarrage
			statuses = append(statuses, st)
			continue
		}
		if fi, err := f.Stat(); err == nil {
			st.UpdatedAt = fi.ModTime()
		}
		ls := listSets{id: s.URL, block: Set{}, allow: Set{}}
		err = ParseReader(f, func(domain string, allow bool) {
			if allow {
				ls.allow.Add(domain)
				st.Exceptions++
			} else {
				ls.block.Add(domain)
				st.Blocked++
			}
		})
		f.Close()
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name, err))
			st.Error = err.Error()
		} else {
			idx.lists = append(idx.lists, ls)
			total += len(ls.block)
			slog.Info("liste chargée", "liste", s.Name, "bloqués", st.Blocked, "exceptions", st.Exceptions)
		}
		statuses = append(statuses, st)
	}

	u.engine.setLists(idx)
	u.status.Store(&statuses)
	slog.Info("index des listes publié", "domaines_bloqués", total)
	return errors.Join(errs...)
}
