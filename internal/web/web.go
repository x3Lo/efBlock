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
	"time"

	"github.com/x3Lo/efBlock/internal/cache"
	"github.com/x3Lo/efBlock/internal/filter"
	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/store"
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
	Log     *querylog.Logger // peut être nil (journal désactivé)
	Cache   *cache.Cache     // peut être nil

	PasswordHash string       // hash bcrypt du mot de passe
	AllowedNets  []*net.IPNet // seuls ces réseaux peuvent atteindre l'interface
	Secure       bool         // true si servi en HTTPS : cookie marqué "Secure"
}

// Server est le gestionnaire HTTP de l'interface.
type Server struct {
	d        Deps
	sessions *sessions
	limiter  *limiter
}

// New crée le serveur.
func New(d Deps) *Server {
	return &Server{d: d, sessions: newSessions(sessionTTL), limiter: newLimiter(loginAttempts, loginLockout)}
}

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
	mux.HandleFunc("GET /api/allowlist", s.auth(s.handleDomains(store.Allow)))
	mux.HandleFunc("POST /api/allowlist", s.auth(s.handleAddDomain(store.Allow)))
	mux.HandleFunc("DELETE /api/allowlist", s.auth(s.handleRemoveDomain(store.Allow)))
	mux.HandleFunc("GET /api/blocklist", s.auth(s.handleDomains(store.Block)))
	mux.HandleFunc("POST /api/blocklist", s.auth(s.handleAddDomain(store.Block)))
	mux.HandleFunc("DELETE /api/blocklist", s.auth(s.handleRemoveDomain(store.Block)))
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
	for _, n := range s.d.AllowedNets {
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
