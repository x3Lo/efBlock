package filter

import (
	"sync/atomic"
	"time"
)

// Reason explique une décision de filtrage (utile pour les logs et le dashboard).
type Reason int

const (
	ReasonNone      Reason = iota // domaine inconnu des règles : on laisse passer
	ReasonUserAllow               // allowlist perso
	ReasonUserBlock               // blocklist perso
	ReasonListAllow               // exception (@@) d'une liste de filtres
	ReasonList                    // domaine présent dans une liste de filtres
)

func (r Reason) String() string {
	return [...]string{"none", "allowlist", "blocklist", "list-exception", "list"}[r]
}

// Verdict est le résultat de Engine.Check.
type Verdict struct {
	Blocked bool
	Reason  Reason
	Rule    string // la règle qui a matché (ex: "doubleclick.net")
}

// listIndex est l'index construit à partir des listes téléchargées.
// Il est immuable une fois publié : on ne le modifie jamais, on le remplace.
type listIndex struct {
	block Set
	allow Set
}

// userRules : allowlist et blocklist perso de l'utilisateur.
type userRules struct {
	allow Set
	block Set
}

// Engine décide si un domaine doit être bloqué. Il est sûr à utiliser depuis
// plusieurs goroutines : les requêtes DNS lisent pendant qu'une mise à jour
// remplace les données.
//
// Astuce : plutôt qu'un mutex, on garde un pointeur atomique vers des données
// immuables. Une mise à jour construit un nouvel index à côté puis échange le
// pointeur en une instruction : les lecteurs ne sont jamais bloqués et ne
// voient jamais un index à moitié construit.
type Engine struct {
	lists   atomic.Pointer[listIndex]
	user    atomic.Pointer[userRules]
	enabled atomic.Bool

	// pausedUntil : fin d'une pause temporaire (nanosecondes Unix, 0 = pas de pause).
	pausedUntil atomic.Int64
}

// NewEngine crée un moteur actif, sans aucune règle.
func NewEngine() *Engine {
	e := &Engine{}
	e.lists.Store(&listIndex{block: Set{}, allow: Set{}})
	e.user.Store(&userRules{allow: Set{}, block: Set{}})
	e.enabled.Store(true)
	return e
}

// SetEnabled active ou désactive globalement le blocage (sans limite de durée).
func (e *Engine) SetEnabled(on bool) { e.enabled.Store(on) }

// Pause désactive le blocage pendant d, puis il reprend tout seul.
// Pratique quand un site légitime est cassé par un blocage : "pause 5 min".
func (e *Engine) Pause(d time.Duration) { e.pausedUntil.Store(time.Now().Add(d).UnixNano()) }

// Resume annule une pause et réactive le blocage.
func (e *Engine) Resume() {
	e.pausedUntil.Store(0)
	e.enabled.Store(true)
}

// State retourne l'interrupteur général et la fin de la pause en cours
// (zéro si aucune pause n'est en cours).
func (e *Engine) State() (enabled bool, pausedUntil time.Time) {
	enabled = e.enabled.Load()
	if n := e.pausedUntil.Load(); n != 0 {
		if t := time.Unix(0, n); t.After(time.Now()) {
			pausedUntil = t
		}
	}
	return
}

// Enabled indique si le blocage est réellement actif à cet instant.
func (e *Engine) Enabled() bool {
	enabled, until := e.State()
	return enabled && until.IsZero()
}

// ValidateDomain normalise et valide un domaine saisi par l'utilisateur.
func ValidateDomain(s string) (string, bool) { return normalizeDomain(s) }

// SetUserRules remplace l'allowlist et la blocklist perso.
// Les entrées invalides sont ignorées ; le nombre d'entrées rejetées est retourné.
func (e *Engine) SetUserRules(allow, block []string) (rejected int) {
	u := &userRules{allow: Set{}, block: Set{}}
	for _, d := range allow {
		if n, ok := normalizeDomain(d); ok {
			u.allow.Add(n)
		} else {
			rejected++
		}
	}
	for _, d := range block {
		if n, ok := normalizeDomain(d); ok {
			u.block.Add(n)
		} else {
			rejected++
		}
	}
	e.user.Store(u)
	return rejected
}

// setLists publie un nouvel index de listes.
func (e *Engine) setLists(idx *listIndex) { e.lists.Store(idx) }

// Counts retourne le nombre de domaines bloqués et d'exceptions chargés depuis les listes.
func (e *Engine) Counts() (blocked, allowed int) {
	idx := e.lists.Load()
	return len(idx.block), len(idx.allow)
}

// Check décide du sort de name (un nom de requête DNS, avec ou sans point final).
//
// Priorité, de la plus forte à la plus faible :
//  1. allowlist perso        -> jamais bloqué
//  2. blocklist perso        -> bloqué
//  3. exceptions des listes  -> jamais bloqué
//  4. listes de filtres      -> bloqué
func (e *Engine) Check(name string) Verdict {
	if !e.Enabled() {
		return Verdict{}
	}
	name = NormalizeName(name)

	u := e.user.Load()
	if rule, ok := u.allow.Match(name); ok {
		return Verdict{Reason: ReasonUserAllow, Rule: rule}
	}
	if rule, ok := u.block.Match(name); ok {
		return Verdict{Blocked: true, Reason: ReasonUserBlock, Rule: rule}
	}

	idx := e.lists.Load()
	if rule, ok := idx.allow.Match(name); ok {
		return Verdict{Reason: ReasonListAllow, Rule: rule}
	}
	if rule, ok := idx.block.Match(name); ok {
		return Verdict{Blocked: true, Reason: ReasonList, Rule: rule}
	}
	return Verdict{}
}
