package web

import (
	"net/http"
	"sync"
	"time"

	"github.com/miekg/dns"
)

// Indicateur « le serveur a accès à internet ».
//
// On pose une vraie question aux résolveurs amont (les serveurs NS de la racine : toujours
// disponibles, aucune adresse privée, sans passer par le cache ni les filtres). Si l'un répond,
// efBlock sait résoudre des noms sur internet. Le résultat est gardé quelques secondes : plusieurs
// onglets ouverts ne multiplient pas les requêtes.

const connectivityEvery = 15 * time.Second

type connectivityInfo struct {
	DNS      bool   `json:"dns"`               // un résolveur amont répond
	Latency  int64  `json:"latency_ms"`        // durée de la réponse
	Error    string `json:"error,omitempty"`   // pourquoi pas
	Checked  string `json:"checked"`           // RFC3339
	Upstream int    `json:"upstreams_total"`   // nombre de résolveurs configurés
	Healthy  int    `json:"upstreams_healthy"` // ... dont en état de marche
	Updates  string `json:"updates"`           // GitHub : ok | error | unknown (dernière vérification)
}

type connectivityState struct {
	mu   sync.Mutex
	last connectivityInfo
	at   time.Time
}

func (s *Server) probeConnectivity() connectivityInfo {
	st := &s.conn
	st.mu.Lock()
	defer st.mu.Unlock()
	if !st.at.IsZero() && time.Since(st.at) < connectivityEvery {
		return st.last
	}
	info := connectivityInfo{Updates: "unknown"}
	var fwd interface {
		Resolve(*dns.Msg) (*dns.Msg, error)
	}
	if s.d.Upstream != nil {
		if f := s.d.Upstream(); f != nil {
			fwd = f
			for _, u := range f.Status() {
				info.Upstream++
				if u.Healthy {
					info.Healthy++
				}
			}
		}
	}
	if fwd == nil {
		info.Error = "aucun résolveur amont configuré"
	} else {
		q := new(dns.Msg)
		q.SetQuestion(".", dns.TypeNS)
		start := time.Now()
		resp, err := fwd.Resolve(q)
		info.Latency = time.Since(start).Milliseconds()
		switch {
		case err != nil:
			info.Error = "aucun résolveur amont ne répond : " + err.Error()
		case resp.Rcode != dns.RcodeSuccess:
			info.Error = "réponse inattendue : " + dns.RcodeToString[resp.Rcode]
		default:
			info.DNS = true
		}
	}
	// Joignabilité de GitHub (mises à jour) : déduite de la dernière vérification, sans trafic de plus.
	s.upd.mu.Lock()
	if !s.upd.checked.IsZero() {
		if s.upd.err == "" {
			info.Updates = "ok"
		} else {
			info.Updates = "error"
		}
	}
	s.upd.mu.Unlock()

	info.Checked = time.Now().Format(time.RFC3339)
	st.last, st.at = info, time.Now()
	return info
}

func (s *Server) handleConnectivity(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.probeConnectivity())
}
