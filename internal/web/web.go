// Package web sert l'interface d'administration d'efBlock : une API JSON
// protégée par mot de passe, et l'interface Next.js compilée (embarquée dans
// le binaire, donc rien à installer d'autre sur la VM).
package web

import (
	"embed"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/x3Lo/efBlock/internal/cache"
	"github.com/x3Lo/efBlock/internal/filter"
	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/settings"
	"github.com/x3Lo/efBlock/internal/store"
	"github.com/x3Lo/efBlock/internal/upstream"
)

// dist contient l'interface compilée (voir `make ui`).
//
//go:embed all:dist
var distFS embed.FS

const (
	cookieName    = "efblock_session"
	csrfHeader    = "X-Requested-With"
	csrfValue     = "efblock"
	sessionTTL    = 24 * time.Hour
	maxBodyBytes  = 64 << 10
	loginAttempts = 5
	loginLockout  = 5 * time.Minute
)

// Deps regroupe tout ce dont l'interface a besoin.
type Deps struct {
	Engine  *filter.Engine
	Updater *filter.Updater
	Store   *store.Store
	Log     *querylog.Logger    // peut être nil (journal désactivé)
	Cache   func() *cache.Cache // le cache actuel (peut changer à chaud) ; peut être nil

	PasswordHash string       // hash bcrypt du mot de passe
	AllowedNets  []*net.IPNet // seuls ces réseaux peuvent atteindre l'interface
	Secure       bool         // true si servi en HTTPS : cookie marqué "Secure"

	Settings   *settings.Manager          // réglages modifiables depuis l'interface
	Upstream   func() *upstream.Forwarder // résolveurs amont actuels (état de santé)
	ClientName func(ip string) string     // nom d'une machine à partir de son IP ("" si inconnu)
	Restart    func()                     // redémarre efBlock ; nil si impossible (hors systemd)
	Update     UpdateConfig               // mise à jour depuis l'interface
}

// Server est le gestionnaire HTTP de l'interface.
type Server struct {
	d        Deps
	sessions *sessions
	limiter  *limiter

	// Modifiables à chaud depuis l'interface.
	nets atomic.Pointer[[]*net.IPNet]
	hash atomic.Pointer[string]

	statsMu    sync.Mutex
	statsCache map[int]cachedStats

	upd  updateState
	conn connectivityState
}

// New crée le serveur.
func New(d Deps) *Server {
	s := &Server{d: d, sessions: newSessions(sessionTTL), limiter: newLimiter(loginAttempts, loginLockout), statsCache: map[int]cachedStats{}}
	s.SetAllowedNets(d.AllowedNets)
	s.SetPasswordHash(d.PasswordHash)
	return s
}

// SetAllowedNets remplace les réseaux autorisés à atteindre l'interface.
func (s *Server) SetAllowedNets(nets []*net.IPNet) { s.nets.Store(&nets) }

// SetPasswordHash remplace le hash du mot de passe.
func (s *Server) SetPasswordHash(h string) { s.hash.Store(&h) }

// Handler retourne le gestionnaire à passer à http.Server.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Sans authentification : connexion et test de session.
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("GET /api/session", s.handleSession)

	// Avec authentification.
	mux.HandleFunc("POST /api/logout", s.auth(s.handleLogout))
	mux.HandleFunc("GET /api/stats", s.auth(s.handleStats))
	mux.HandleFunc("POST /api/blocking", s.auth(s.handleBlocking))
	mux.HandleFunc("GET /api/queries", s.auth(s.handleQueries))
	mux.HandleFunc("GET /api/queries/stream", s.auth(s.handleQueryStream))
	mux.HandleFunc("GET /api/settings", s.auth(s.handleSettings))
	mux.HandleFunc("PUT /api/settings/{group}", s.auth(s.handleSettingsUpdate))
	mux.HandleFunc("DELETE /api/settings/{group}", s.auth(s.handleSettingsReset))
	mux.HandleFunc("POST /api/upstreams/test", s.auth(s.handleUpstreamsTest))
	mux.HandleFunc("POST /api/password", s.auth(s.handlePassword))
	mux.HandleFunc("POST /api/restart", s.auth(s.handleRestart))
	mux.HandleFunc("GET /api/connectivity", s.auth(s.handleConnectivity))
	mux.HandleFunc("GET /api/backup", s.auth(s.handleBackup))
	mux.HandleFunc("POST /api/restore", s.auth(s.handleRestore))
	mux.HandleFunc("GET /api/update", s.auth(s.handleUpdateInfo))
	mux.HandleFunc("POST /api/update", s.auth(s.handleUpdateApply))
	mux.HandleFunc("GET /api/allowlist", s.auth(s.handleDomains(store.Allow)))
	mux.HandleFunc("POST /api/allowlist", s.auth(s.handleAddDomain(store.Allow)))
	mux.HandleFunc("DELETE /api/allowlist", s.auth(s.handleRemoveDomain(store.Allow)))
	mux.HandleFunc("GET /api/blocklist", s.auth(s.handleDomains(store.Block)))
	mux.HandleFunc("POST /api/blocklist", s.auth(s.handleAddDomain(store.Block)))
	mux.HandleFunc("DELETE /api/blocklist", s.auth(s.handleRemoveDomain(store.Block)))
	mux.HandleFunc("GET /api/groups", s.auth(s.handleGroups))
	mux.HandleFunc("PUT /api/groups", s.auth(s.handleGroupsUpdate))
	mux.HandleFunc("GET /api/lists", s.auth(s.handleLists))
	mux.HandleFunc("POST /api/lists", s.auth(s.handleAddList))
	mux.HandleFunc("DELETE /api/lists", s.auth(s.handleRemoveList))
	mux.HandleFunc("POST /api/lists/update", s.auth(s.handleUpdateLists))
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "route inconnue")
	})

	// Tout le reste : les fichiers de l'interface (publics : la page de
	// connexion doit pouvoir se charger. Les DONNÉES, elles, exigent une session).
	mux.Handle("/", s.static())

	return s.securityHeaders(s.lanOnly(mux))
}

// lanOnly refuse toute connexion qui ne vient pas d'un réseau autorisé.
//
// On se base sur l'adresse réelle de la connexion TCP, jamais sur un en-tête
// comme X-Forwarded-For : un client pourrait le falsifier.
func (s *Server) lanOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.ipAllowed(clientIP(r)) {
			http.Error(w, "accès refusé", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) ipAllowed(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range *s.nets.Load() {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP : l'IP de l'autre bout de la connexion.
func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if v4 := ip.To4(); v4 != nil {
		return v4 // une IPv4 vue en IPv6 (::ffff:a.b.c.d) redevient une IPv4
	}
	return ip
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY") // interdit d'afficher l'interface dans une iframe (clickjacking)
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// auth exige une session valide, et pour les requêtes qui modifient quelque
// chose, l'en-tête anti-CSRF.
//
// CSRF : un site malveillant ouvert dans ton navigateur pourrait envoyer une
// requête à efBlock en profitant de ton cookie de session. Un navigateur ne
// laisse pas une page d'un autre site ajouter un en-tête personnalisé sans
// l'accord du serveur : exiger "X-Requested-With" bloque donc ces attaques.
// Le cookie est en plus SameSite=Strict.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil || !s.sessions.valid(c.Value) {
			writeError(w, http.StatusUnauthorized, "non connecté")
			return
		}
		if r.Method != http.MethodGet && r.Header.Get(csrfHeader) != csrfValue {
			writeError(w, http.StatusForbidden, "en-tête de sécurité manquant")
			return
		}
		next(w, r)
	}
}

// static sert l'interface compilée.
func (s *Server) static() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err) // le dossier est embarqué à la compilation : ne peut pas manquer
	}
	files := http.FileServerFS(sub)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Les fichiers de /_next/static/ ont un nom qui change à chaque
		// compilation : le navigateur peut les garder pour toujours.
		if strings.HasPrefix(r.URL.Path, "/_next/static/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
