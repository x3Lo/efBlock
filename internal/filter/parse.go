package filter

import (
	"bufio"
	"io"
	"net"
	"strings"
)

// ParseReader lit une liste de filtres ligne par ligne et appelle fn pour
// chaque domaine trouvé. allow vaut true pour une règle d'exception (@@).
//
// Trois formats sont reconnus (les listes du monde réel mélangent souvent) :
//
//	hosts   :  0.0.0.0 ads.example.com        (StevenBlack...)
//	adblock :  ||ads.example.com^             (OISD, EasyList...)
//	          @@||ok.example.com^             (exception = allowlist)
//	simple  :  ads.example.com                (un domaine par ligne)
//
// Tout ce qui n'a pas de sens en DNS est ignoré : règles cosmétiques (##),
// chemins d'URL (||site.com/pub), jokers au milieu, options ($third-party)...
// Ignorer est plus sûr que mal interpréter : on préfère laisser passer une
// pub que bloquer un site légitime par erreur.
func ParseReader(r io.Reader, fn func(domain string, allow bool)) error {
	sc := bufio.NewScanner(r)
	// Certaines lignes de listes sont très longues : on agrandit le tampon.
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		domains, allow := parseLine(sc.Text())
		for _, d := range domains {
			fn(d, allow)
		}
	}
	return sc.Err()
}

// ignoredHosts : entrées "techniques" présentes dans les fichiers hosts
// qui ne doivent surtout pas être bloquées.
var ignoredHosts = map[string]bool{
	"localhost": true, "localhost.localdomain": true, "local": true,
	"broadcasthost": true, "ip6-localhost": true, "ip6-loopback": true,
	"ip6-localnet": true, "ip6-mcastprefix": true, "ip6-allnodes": true,
	"ip6-allrouters": true, "ip6-allhosts": true, "0.0.0.0": true,
}

// parseLine extrait les domaines d'une ligne. Retourne nil si rien d'exploitable.
func parseLine(line string) (domains []string, allow bool) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] == '#' || line[0] == '!' || line[0] == '[' {
		return nil, false // commentaire, ou en-tête type "[Adblock Plus 2.0]"
	}

	// --- Format adblock : ||domaine^ ou @@||domaine^ ---
	if strings.HasPrefix(line, "@@||") {
		allow = true
		line = line[2:]
	}
	if strings.HasPrefix(line, "||") {
		rule := line[2:]
		end := strings.IndexByte(rule, '^')
		if end < 0 {
			return nil, false // pas de "^" : règle sur une URL, pas un domaine
		}
		// Après "^" on n'accepte rien, ou "$important". Les autres options
		// ($third-party, $script...) dépendent de la page web : inapplicable en DNS.
		if tail := rule[end+1:]; tail != "" && tail != "$important" {
			return nil, false
		}
		if d, ok := normalizeDomain(rule[:end]); ok {
			return []string{d}, allow
		}
		return nil, false
	}
	if allow {
		return nil, false // "@@" qui n'est pas suivi de "||" : on ignore
	}

	// --- Retire un éventuel commentaire en fin de ligne (hosts) ---
	// Seul un "#" précédé d'un espace/tabulation est un commentaire. Un "#"
	// collé au nom ("example.com##.banner") est une règle cosmétique adblock :
	// elle n'a pas de sens en DNS, et surtout on ne doit PAS en déduire qu'il
	// faut bloquer "example.com". Le nom contiendra "#" et sera rejeté.
	line = strings.Join(strings.Fields(line), " ") // espaces/tabulations -> un seul espace
	if k := strings.Index(line, " #"); k >= 0 {
		line = line[:k]
	}
	fields := strings.Fields(line)

	switch {
	// --- Format hosts : "IP domaine [domaine...]" ---
	case len(fields) >= 2 && net.ParseIP(fields[0]) != nil:
		for _, f := range fields[1:] {
			if ignoredHosts[strings.ToLower(f)] {
				continue
			}
			if d, ok := normalizeDomain(f); ok {
				domains = append(domains, d)
			}
		}
		return domains, false

	// --- Format simple : un domaine seul (accepte "*.domaine" des listes OISD) ---
	case len(fields) == 1:
		if d, ok := normalizeDomain(strings.TrimPrefix(fields[0], "*.")); ok {
			return []string{d}, false
		}
	}
	return nil, false
}

// NormalizeName met un nom de domaine sous forme canonique : minuscules et
// sans le point final. En DNS, "Example.COM." et "example.com" désignent le
// même nom : on compare toujours des noms normalisés.
func NormalizeName(name string) string {
	return strings.TrimSuffix(strings.ToLower(name), ".")
}

// normalizeDomain normalise puis valide un domaine. ok=false si ce n'est pas
// un nom d'hôte DNS plausible.
func normalizeDomain(s string) (string, bool) {
	s = NormalizeName(s)
	if len(s) == 0 || len(s) > 253 {
		return "", false
	}
	if net.ParseIP(s) != nil {
		return "", false // une adresse IP n'est pas un domaine
	}
	labels := strings.Split(s, ".")
	if len(labels) < 2 {
		return "", false // "com" seul : bloquer un TLD entier serait un accident
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 {
			return "", false
		}
		for i := 0; i < len(l); i++ {
			c := l[i]
			ok := c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_'
			if !ok {
				return "", false
			}
		}
	}
	return s, true
}
