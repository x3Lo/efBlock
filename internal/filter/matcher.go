package filter

import "strings"

// Set est un ensemble de domaines normalisés (voir NormalizeName).
type Set map[string]struct{}

// Add ajoute un domaine déjà normalisé.
func (s Set) Add(domain string) { s[domain] = struct{}{} }

// Match indique si name, ou l'un de ses domaines parents, est dans l'ensemble.
// Il retourne la règle qui a matché.
//
// Concept DNS : les noms sont hiérarchiques, lus de droite à gauche.
// "a.b.example.com" appartient à "b.example.com", qui appartient à
// "example.com". Bloquer "example.com" bloque donc tous ses sous-domaines.
// On teste ainsi le nom complet puis on retire un label à gauche à chaque
// tour : au plus ~5 recherches dans la map, quelle que soit la taille de la
// liste (300 000 domaines ou 3 millions : même vitesse).
func (s Set) Match(name string) (rule string, ok bool) {
	for {
		if _, found := s[name]; found {
			return name, true
		}
		i := strings.IndexByte(name, '.')
		if i < 0 {
			return "", false
		}
		name = name[i+1:]
	}
}
