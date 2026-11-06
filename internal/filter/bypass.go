package filter

// Contournements : des appareils peuvent ignorer le DNS du réseau (donc efBlock) et envoyer leurs
// questions à leur propre serveur, en chiffré. efBlock ne voit alors plus rien et ne bloque plus rien.
// On ne peut pas l'empêcher totalement par le DNS (une adresse IP écrite en dur passe toujours : il
// faut alors une règle de pare-feu), mais on couvre les cas courants :
//
//   - use-application-dns.net : « domaine témoin » de Firefox. S'il n'existe pas, Firefox comprend
//     que le réseau filtre le DNS et n'active pas son DNS chiffré automatique.
//   - mask.icloud.com, mask-h2.icloud.com : iCloud Private Relay. Sans réponse, l'iPhone ou le Mac
//     prévient l'utilisateur et passe par le DNS du réseau.
//   - les serveurs DNS chiffrés (DoH, DoT) les plus connus, configurés à la main dans un navigateur,
//     un téléphone (« DNS privé » d'Android) ou Windows. Sans leur adresse, ils retombent sur le DNS du réseau.
//
// Sous-domaines compris : bloquer cloudflare-dns.com bloque aussi mozilla.cloudflare-dns.com.
// Le DNS chiffré d'efBlock lui-même n'est pas concerné : il trouve ses serveurs sans passer par ce filtre.
var bypassDomains = func() Set {
	s := Set{}
	for _, d := range []string{
		// Domaine témoin de Firefox.
		"use-application-dns.net",
		// iCloud Private Relay.
		"mask.icloud.com", "mask-h2.icloud.com",
		// Serveurs DNS chiffrés publics.
		"dns.google", "dns64.dns.google",
		"cloudflare-dns.com", "one.one.one.one",
		"dns.quad9.net", "dns9.quad9.net", "dns10.quad9.net", "dns11.quad9.net",
		"doh.opendns.com", "doh.familyshield.opendns.com",
		"dns.nextdns.io",
		"dns.adguard-dns.com", "dns.adguard.com", "dns-family.adguard.com", "dns-unfiltered.adguard.com",
		"doh.cleanbrowsing.org",
		"doh.mullvad.net", "dns.mullvad.net",
		"freedns.controld.com",
		"doh.dns.sb",
		"dns.alidns.com", "doh.pub",
	} {
		s.Add(d)
	}
	return s
}()
