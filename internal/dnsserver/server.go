// Package dnsserver écoute sur UDP+TCP et répond aux requêtes DNS des clients.
package dnsserver

import (
	"log/slog"
	"net"
	"time"

	"github.com/miekg/dns"

	"github.com/x3Lo/efBlock/internal/cache"
	"github.com/x3Lo/efBlock/internal/filter"
	"github.com/x3Lo/efBlock/internal/querylog"
	"github.com/x3Lo/efBlock/internal/upstream"
)

// blockedTTL : durée (secondes) pendant laquelle un client garde en mémoire
// une réponse "bloquée". Court, pour qu'un domaine débloqué le soit vite.
const blockedTTL = 10

// Options regroupe ce dont le serveur a besoin pour fonctionner.
type Options struct {
	Addr     string              // adresse d'écoute, ex "192.168.50.10:53"
	Upstream *upstream.Forwarder // résolveurs amont
	Filter   *filter.Engine      // décide quels domaines sont bloqués
	Cache    *cache.Cache        // nil = pas de cache
	Log      *querylog.Logger    // nil = pas de journal
	NXDomain bool                // true : bloque avec NXDOMAIN ; false : avec 0.0.0.0 / ::
}

// Server est le serveur DNS d'efBlock.
//
// Chaque requête traverse : filtre -> cache -> upstream.
type Server struct {
	opts    Options
	servers []*dns.Server
}

// New crée un serveur à partir de ses options.
func New(opts Options) *Server {
	return &Server{opts: opts}
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
		srv := &dns.Server{Addr: s.opts.Addr, Net: network, Handler: mux}
		s.servers = append(s.servers, srv)
		go func() {
			slog.Info("serveur DNS démarré", "addr", s.opts.Addr, "proto", srv.Net)
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
	start := time.Now()

	// Une requête valide contient exactement une question (nom + type).
	if len(req.Question) != 1 {
		resp := new(dns.Msg)
		resp.SetRcode(req, dns.RcodeFormatError)
		s.reply(w, req, resp)
		return
	}
	q := req.Question[0]
	qtype := dns.TypeToString[q.Qtype]

	// record ajoute la requête au journal (sans jamais bloquer, voir querylog).
	record := func(action, rule string, rcode int) {
		s.opts.Log.Log(querylog.Entry{
			Time:     start,
			Client:   clientIP(w),
			Name:     filter.NormalizeName(q.Name),
			QType:    qtype,
			Action:   action,
			Rule:     rule,
			Rcode:    dns.RcodeToString[rcode],
			Duration: time.Since(start),
		})
	}

	// 1. Filtrage : avant tout accès au réseau, on regarde si le domaine
	// est bloqué. Un domaine bloqué ne quitte jamais la maison.
	if v := s.opts.Filter.Check(q.Name); v.Blocked {
		slog.Debug("bloqué", "name", q.Name, "type", qtype, "règle", v.Rule, "raison", v.Reason)
		resp := s.blockedResponse(req, q)
		s.reply(w, req, resp)
		record(querylog.ActionBlocked, v.Rule, resp.Rcode)
		return
	}

	// 2. Cache : si on a déjà la réponse (et qu'elle n'est pas périmée), on la
	// sert sans toucher au réseau.
	if s.opts.Cache != nil {
		if resp, ok := s.opts.Cache.Get(req); ok {
			slog.Debug("cache", "name", q.Name, "type", qtype, "rcode", dns.RcodeToString[resp.Rcode])
			s.reply(w, req, resp)
			record(querylog.ActionCached, "", resp.Rcode)
			return
		}
	}

	// 3. Upstream.
	resp, err := s.opts.Upstream.Resolve(req)
	if err != nil {
		slog.Warn("échec upstream", "name", q.Name, "type", qtype, "err", err)
		// SERVFAIL = "j'ai essayé mais je n'ai pas pu résoudre".
		fail := new(dns.Msg)
		fail.SetRcode(req, dns.RcodeServerFailure)
		s.reply(w, req, fail)
		record(querylog.ActionError, "", dns.RcodeServerFailure)
		return
	}

	slog.Debug("requête transmise", "name", q.Name, "type", qtype, "rcode", dns.RcodeToString[resp.Rcode])
	if s.opts.Cache != nil {
		s.opts.Cache.Put(req, resp) // Put copie : on peut ensuite modifier resp (troncature)
	}
	s.reply(w, req, resp)
	record(querylog.ActionForwarded, "", resp.Rcode)
}

// clientIP extrait l'IP du client (sans le port) de la connexion.
func clientIP(w dns.ResponseWriter) string {
	host, _, err := net.SplitHostPort(w.RemoteAddr().String())
	if err != nil {
		return w.RemoteAddr().String()
	}
	return host
}

// reply envoie resp au client, en respectant la taille de paquet qu'il accepte.
//
// Concept DNS : en UDP, un client annonce la taille max de réponse qu'il sait
// recevoir. Sans EDNS0 c'est 512 octets ; avec EDNS0 il l'écrit dans un
// enregistrement OPT (souvent 1232 ou 4096). Si la réponse est plus grosse,
// on la tronque et on met le drapeau TC : le client refait alors la question
// en TCP, qui n'a pas cette limite.
func (s *Server) reply(w dns.ResponseWriter, req, resp *dns.Msg) {
	if _, isUDP := w.RemoteAddr().(*net.UDPAddr); isUDP {
		size := dns.MinMsgSize // 512
		if opt := req.IsEdns0(); opt != nil && int(opt.UDPSize()) > size {
			size = int(opt.UDPSize())
		}
		resp.Truncate(size)
	}
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

	if s.opts.NXDomain {
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
