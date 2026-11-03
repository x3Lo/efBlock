package web

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// sessions : connexions ouvertes. En mémoire seulement : un redémarrage
// d'efBlock déconnecte tout le monde, ce qui est acceptable et plus sûr.
type sessions struct {
	mu  sync.Mutex
	m   map[string]time.Time // jeton -> expiration
	ttl time.Duration
}

func newSessions(ttl time.Duration) *sessions {
	return &sessions{m: map[string]time.Time{}, ttl: ttl}
}

// create génère un jeton aléatoire de 256 bits : impossible à deviner.
func (s *sessions) create() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // le générateur aléatoire du système ne doit jamais échouer
	}
	tok := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, exp := range s.m { // ménage des sessions expirées
		if now.After(exp) {
			delete(s.m, k)
		}
	}
	s.m[tok] = now.Add(s.ttl)
	return tok
}

func (s *sessions) valid(tok string) bool {
	if tok == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.m[tok]
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		delete(s.m, tok)
		return false
	}
	return true
}

func (s *sessions) delete(tok string) {
	s.mu.Lock()
	delete(s.m, tok)
	s.mu.Unlock()
}

// limiter freine les tentatives de mot de passe : après maxFails échecs
// depuis une même IP, elle est bloquée pendant lockFor. Sans ça, quelqu'un
// sur le wifi pourrait essayer des milliers de mots de passe par minute.
type limiter struct {
	mu       sync.Mutex
	fails    map[string]*failState
	maxFails int
	lockFor  time.Duration
}

type failState struct {
	count       int
	lockedUntil time.Time
}

func newLimiter(maxFails int, lockFor time.Duration) *limiter {
	return &limiter{fails: map[string]*failState{}, maxFails: maxFails, lockFor: lockFor}
}

// allowed indique si ip peut tenter de se connecter maintenant.
func (l *limiter) allowed(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[ip]
	return f == nil || !time.Now().Before(f.lockedUntil)
}

func (l *limiter) failure(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.fails[ip]
	if f == nil {
		f = &failState{}
		l.fails[ip] = f
	}
	f.count++
	if f.count >= l.maxFails {
		f.lockedUntil = time.Now().Add(l.lockFor)
		f.count = 0
	}
}

func (l *limiter) success(ip string) {
	l.mu.Lock()
	delete(l.fails, ip)
	l.mu.Unlock()
}

// deleteExcept ferme toutes les sessions sauf celle de keep (après un changement de mot de passe).
func (s *sessions) deleteExcept(keep string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.m {
		if k != keep {
			delete(s.m, k)
		}
	}
}
