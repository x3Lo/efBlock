// Package dnsserver écoute sur UDP+TCP et répond aux requêtes DNS des clients.
package dnsserver

import (
	"log/slog"
	"net"

	"github.com/miekg/dns"

	"github.com/x3Lo/efBlock/internal/filter"
	"github.com/x3Lo/efBlock/internal/upstream"
)

// blockedTTL : durée (secondes) pendant laquelle un client garde en mémoire
// une réponse "bloquée". Court, pour qu'un domaine débloqué le soit vite.
const blockedTTL = 10

// Server est le serveur DNS d'efBlock.
type Server struct {
	addr     string
	upstream *upstream.Forwarder
	filter   *filter.Engine
	nxdomain bool // true : bloque avec NXDOMAIN ; false : avec 0.0.0.0 / ::
	servers  []*dns.Server
}

// New crée un serveur qui écoute sur addr, filtre via eng puis transmet via fwd.
// Si nxdomain est vrai, les domaines bloqués reçoivent NXDOMAIN, sinon 0.0.0.0 / ::.
func New(addr string, fwd *upstream.Forwarder, eng *filter.Engine, nxdomain bool) *Server {
	return &Server{addr: addr, upstream: fwd, filter: eng, nxdomain: nxdomain}
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

	// Étape de filtrage : avant tout accès au réseau, on regarde si le domaine
	// est bloqué. Un domaine bloqué ne quitte jamais la maison.
	if v := s.filter.Check(q.Name); v.Blocked {
		slog.Debug("bloqué", "name", q.Name, "type", dns.TypeToString[q.Qtype], "règle", v.Rule, "raison", v.Reason)
		_ = w.WriteMsg(s.blockedResponse(req, q))
		return
	}

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

// blockedResponse fabrique la fausse réponse envoyée pour un domaine bloqué.
//
// Concept DNS : deux façons de dire "ce site n'est pas joignable" :
//   - 0.0.0.0 (A) / :: (AAAA) : le domaine "existe" mais pointe vers une
//     adresse inutilisable. L'appli échoue immédiatement ("connexion refusée")
//     sans attendre de timeout. C'est le mode par défaut de Pi-hole/AdGuard.
//   - NXDOMAIN : "ce domaine n'existe pas". Plus radical, mais certains
//     clients réessayent plus longtemps.
//
// Pour les autres types (HTTPS, MX, TXT...) on répond NOERROR sans réponse
// (dite "NODATA") : "le domaine existe, mais je n'ai rien de ce type".
func (s *Server) blockedResponse(req *dns.Msg, q dns.Question) *dns.Msg {
	resp := new(dns.Msg)
	resp.SetReply(req)
	resp.RecursionAvailable = true

	if s.nxdomain {
		resp.Rcode = dns.RcodeNameError // NXDOMAIN
		return resp
	}

	hdr := dns.RR_Header{Name: q.Name, Class: dns.ClassINET, Ttl: blockedTTL}
	switch q.Qtype {
	case dns.TypeA:
		hdr.Rrtype = dns.TypeA
		resp.Answer = append(resp.Answer, &dns.A{Hdr: hdr, A: net.IPv4zero})
	case dns.TypeAAAA:
		hdr.Rrtype = dns.TypeAAAA
		resp.Answer = append(resp.Answer, &dns.AAAA{Hdr: hdr, AAAA: net.IPv6zero})
	}
	return resp
}
