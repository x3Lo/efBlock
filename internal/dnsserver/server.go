// Package dnsserver écoute sur UDP+TCP et répond aux requêtes DNS des clients.
package dnsserver

import (
	"log/slog"

	"github.com/miekg/dns"

	"github.com/x3Lo/efBlock/internal/upstream"
)

// Server est le serveur DNS d'efBlock.
type Server struct {
	addr     string
	upstream *upstream.Forwarder
	servers  []*dns.Server
}

// New crée un serveur qui écoute sur addr et transmet via fwd.
func New(addr string, fwd *upstream.Forwarder) *Server {
	return &Server{addr: addr, upstream: fwd}
}

// Start lance les écouteurs UDP et TCP. Il bloque jusqu'à la première erreur.
//
// Concept DNS : le DNS utilise UDP par défaut (rapide, un paquet par question
// et par réponse) et TCP pour les grosses réponses ou certains clients.
// Un vrai serveur DNS doit donc écouter sur les DEUX protocoles.
func (s *Server) Start() error {
	mux := dns.NewServeMux()
	mux.HandleFunc(".", s.handle) // "." = tous les domaines

	errCh := make(chan error, 2)
	for _, network := range []string{"udp", "tcp"} {
		srv := &dns.Server{Addr: s.addr, Net: network, Handler: mux}
		s.servers = append(s.servers, srv)
		go func() {
			slog.Info("serveur DNS démarré", "addr", s.addr, "proto", srv.Net)
			errCh <- srv.ListenAndServe()
		}()
	}
	return <-errCh
}

// Shutdown arrête proprement les écouteurs.
func (s *Server) Shutdown() {
	for _, srv := range s.servers {
		_ = srv.Shutdown()
	}
}

// handle traite UNE requête. miekg/dns l'appelle dans une goroutine par requête.
func (s *Server) handle(w dns.ResponseWriter, req *dns.Msg) {
	// Une requête valide contient exactement une question (nom + type).
	if len(req.Question) != 1 {
		resp := new(dns.Msg)
		resp.SetRcode(req, dns.RcodeFormatError)
		_ = w.WriteMsg(resp)
		return
	}
	q := req.Question[0]

	resp, err := s.upstream.Resolve(req)
	if err != nil {
		slog.Warn("échec upstream", "name", q.Name, "type", dns.TypeToString[q.Qtype], "err", err)
		// SERVFAIL = "j'ai essayé mais je n'ai pas pu résoudre".
		fail := new(dns.Msg)
		fail.SetRcode(req, dns.RcodeServerFailure)
		_ = w.WriteMsg(fail)
		return
	}

	slog.Debug("requête transmise", "name", q.Name, "type", dns.TypeToString[q.Qtype], "rcode", dns.RcodeToString[resp.Rcode])
	_ = w.WriteMsg(resp)
}
