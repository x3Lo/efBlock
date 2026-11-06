// Listes de filtres connues, proposées en un clic sur la page Filtres. Toutes sont publiques,
// maintenues et dans un format qu'efBlock lit (hosts ou adblock).
export type RecommendedList = { name: string; url: string; description: string; recommended?: boolean };

export const recommendedLists: RecommendedList[] = [
  {
    name: "StevenBlack",
    url: "https://raw.githubusercontent.com/StevenBlack/hosts/master/hosts",
    description: "Publicités et logiciels malveillants. La liste de référence, très peu de faux positifs.",
    recommended: true,
  },
  {
    name: "AdGuard DNS filter",
    url: "https://adguardteam.github.io/HostlistsRegistry/assets/filter_1.txt",
    description: "La liste par défaut d'AdGuard Home : publicités, traqueurs, publicités des applications mobiles.",
  },
  {
    name: "OISD small",
    url: "https://small.oisd.nl/",
    description: "Publicités et traqueurs, conçue pour ne rien casser. Bon choix si un site ne marche plus.",
  },
  {
    name: "HaGeZi Multi Normal",
    url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/adblock/multi.txt",
    description: "Plus complète : publicités, traqueurs, télémétrie. Peut bloquer davantage de services.",
  },
  {
    name: "HaGeZi Threat Intelligence",
    url: "https://raw.githubusercontent.com/hagezi/dns-blocklists/main/adblock/tif.txt",
    description: "Sécurité : hameçonnage, logiciels malveillants, arnaques. À combiner avec une liste anti-publicité.",
  },
];
