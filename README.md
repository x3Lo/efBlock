# 🛡️ efBlock

Un **bloqueur de publicités et de traqueurs au niveau DNS**, écrit en Go, dans l'esprit de
Pi-hole / AdGuard Home. Il tourne sur une petite VM ou un Raspberry Pi et protège tous les
appareils du réseau (PC, téléphones, invités) sans rien installer sur eux.

![Tableau de bord](docs/dashboard.png)

## Fonctionnalités

- **Serveur DNS** UDP + TCP sur le port 53.
- **Blocage par listes** : formats *hosts* (StevenBlack…), *adblock* (`||domaine^`, OISD…) et un domaine par ligne.
  Réponse `0.0.0.0` / `::` ou `NXDOMAIN`. Un domaine bloqué bloque aussi ses sous-domaines.
- **Allowlist / blocklist personnelles**, prioritaires sur les listes.
- **Mise à jour automatique** des listes (24 h par défaut) ; les listes en cache sur disque permettent
  de démarrer même sans internet.
- **Upstreams chiffrés** : DNS-over-TLS (`tls://1.1.1.1`) et DNS-over-HTTPS (`https://…/dns-query`),
  avec basculement de l'un à l'autre.
- **Cache** qui respecte les TTL, avec cache négatif (RFC 2308) et éviction LRU.
- **Journal des requêtes** (SQLite) : domaine, client, résultat, durée. Purge automatique.
- **Interface web** (Next.js) : tableau de bord, pause du blocage (5 min / 30 min / 1 h), journal,
  gestion des listes et des domaines. Protégée par mot de passe, réservée au réseau local.
- **Léger** : un seul binaire (interface comprise) ; environ 27 Mo de RAM avec 78 000 domaines chargés (StevenBlack), un peu plus avec des listes plus grosses.
- **Service systemd** qui redémarre tout seul, sans droits root.

## Comment une requête est traitée

```
Client ──► efBlock (UDP/TCP :53)
             │
             ├─ 1. Filtre ──── domaine bloqué ? ──► réponse 0.0.0.0 (rien ne sort du réseau)
             │      allowlist > blocklist > exceptions des listes > listes
             ├─ 2. Cache ───── déjà connu et valide ? ──► réponse immédiate
             └─ 3. Upstream ── DoT / DoH / DNS classique ──► réponse mise en cache
                    │
                    └─ Journal (SQLite, écriture asynchrone)
```

## Installation sur Debian

Testé sur Debian 13. Les commandes s'exécutent en **root** (`su -`) ; `sudo` n'est pas nécessaire.

### 1. Prérequis

```bash
apt update && apt install -y git curl make
```

Go 1.25 ou plus récent (le paquet `golang` de Debian est trop ancien) :

```bash
curl -LO https://go.dev/dl/go1.25.0.linux-amd64.tar.gz
rm -rf /usr/local/go && tar -C /usr/local -xzf go1.25.0.linux-amd64.tar.gz
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.profile && source ~/.profile
go version
```

(Sur une machine ARM, remplace `amd64` par `arm64`.)

### 2. Récupérer et compiler

```bash
git clone https://github.com/x3Lo/efBlock.git
cd efBlock
make build
```

Le dépôt est privé : Git te demande ton identifiant, puis un *jeton d'accès personnel*
(GitHub → Settings → Developer settings → Fine-grained tokens, permission *Contents : lecture seule*)
à la place du mot de passe.

Node.js n'est **pas** nécessaire : l'interface est déjà compilée et embarquée dans le binaire.

### 3. Configurer

```bash
cp deploy/config.example.yaml config.yaml
./efblock -hash-password        # demande un mot de passe, affiche son hash
nano config.yaml
```

Au minimum, règle :

```yaml
dns:
  listen: "192.168.1.10:53"      # l'IP FIXE de ta VM, port 53
web:
  listen: "192.168.1.10:8080"
  password_hash: '$2a$10$...'    # le hash affiché, entre guillemets simples
```

### 4. Essayer à la main

```bash
./efblock -debug
```

Dans un autre terminal (ou depuis un autre appareil) :

```bash
dig @192.168.1.10 example.com           # doit répondre avec une vraie IP
dig @192.168.1.10 ad.doubleclick.net    # doit répondre 0.0.0.0
```

Ouvre ensuite `http://192.168.1.10:8080`. Arrête avec `Ctrl+C`.

### 5. Installer comme service

```bash
make install
```

Le script crée l'utilisateur `efblock`, copie le binaire dans `/usr/local/bin`, ta config dans
`/etc/efblock/config.yaml`, installe le service et le démarre. Il est relançable sans risque.

```bash
systemctl status efblock          # doit afficher "active (running)"
journalctl -u efblock -f          # logs en direct
```

Le service redémarre tout seul en cas de plantage et au démarrage de la machine. Les données
(listes, journal, état) sont dans `/var/lib/efblock/data`.

### 6. Utiliser efBlock sur tout le réseau

1. Donne une **IP fixe** à la VM (ou une réservation DHCP sur ton routeur).
2. Dans la configuration **DHCP** de ton routeur/box, mets l'IP d'efBlock comme **serveur DNS**.
   Les appareils la reçoivent en se (re)connectant.
3. Garde un DNS de secours (ex. `1.1.1.1`) en 2ᵉ position tant que tu n'as pas confiance : si efBlock
   tombe, tu gardes internet (mais sans blocage pour les appareils qui basculent dessus).
4. **IPv6** : si ton routeur annonce son propre DNS en IPv6, certains appareils le préféreront à efBlock.
   Désactive-le ou annonce l'adresse IPv6 d'efBlock.

> Certaines box (Livebox, Freebox…) ne permettent pas de changer le DNS distribué. Dans ce cas, désactive
> leur serveur DHCP et utilise-en un autre (routeur secondaire, pfSense/OPNsense…), ou règle le DNS appareil par appareil.

## Interface web

- Adresse : `http://IP_DE_LA_VM:8080`.
- Le mot de passe n'est jamais stocké en clair, seulement son hash bcrypt.
- Seuls les réseaux de `web.allowed_cidrs` y accèdent (par défaut : les réseaux privés, jamais internet).
  Après 5 mots de passe faux, l'IP est bloquée 5 minutes.

**Attention : en HTTP, le mot de passe circule en clair sur le réseau.** Si d'autres personnes utilisent
ton wifi, fais au moins l'un des deux :

```yaml
web:
  allowed_cidrs: ["192.168.1.50/32"]   # seulement TON poste
```

ou active HTTPS avec un certificat auto-signé :

```bash
openssl req -x509 -newkey rsa:2048 -nodes -days 825 -subj "/CN=efblock" \
  -addext "subjectAltName=IP:192.168.1.10" \
  -keyout /etc/efblock/key.pem -out /etc/efblock/cert.pem
chown root:efblock /etc/efblock/key.pem /etc/efblock/cert.pem && chmod 640 /etc/efblock/key.pem
```

```yaml
web:
  tls_cert: "/etc/efblock/cert.pem"
  tls_key: "/etc/efblock/key.pem"
```

(Ton navigateur affichera un avertissement la première fois : le certificat est le tien, pas celui d'une autorité.)

## Configuration

Tout est dans un fichier YAML ; voir [`deploy/config.example.yaml`](deploy/config.example.yaml), commenté.

| Section | Option | Défaut | Rôle |
|---|---|---|---|
| `dns` | `listen` | `:53` | adresse d'écoute (UDP + TCP) |
| | `upstreams` | *(obligatoire)* | `tls://IP`, `https://hôte/chemin` ou `IP[:port]` ; essayés dans l'ordre |
| | `bootstrap` | `1.1.1.1:53`, `9.9.9.9:53` | DNS classiques, seulement pour trouver l'IP d'un upstream donné par son nom |
| | `timeout` | `3s` | délai max par upstream |
| `filter` | `lists` | *(aucune)* | listes de filtres (URL ou fichier local) |
| | `allowlist` / `blocklist` | vides | domaines fixes (sous-domaines inclus) |
| | `block_mode` | `zero_ip` | `zero_ip` (0.0.0.0 / ::) ou `nxdomain` |
| | `update_interval` | `24h` | fréquence de mise à jour des listes |
| | `data_dir` | `./data` | cache des listes, état, journal |
| `cache` | `enabled` / `max_entries` / `max_ttl` | `true` / `10000` / `24h` | cache DNS |
| `querylog` | `enabled` / `retention_days` | `true` / `7` | journal (`-1` = illimité) |
| `web` | `listen` / `password_hash` | `:8080` / *(vide = désactivée)* | interface d'administration |
| | `allowed_cidrs` | réseaux privés | qui peut s'y connecter |
| | `tls_cert` / `tls_key` | *(vides)* | active HTTPS |

Les domaines ajoutés **depuis l'interface** sont sauvegardés à part (`state.json`) et s'additionnent à ceux du fichier ;
l'interface ne peut pas supprimer ceux de `config.yaml`.

Après une modification de `/etc/efblock/config.yaml` : `systemctl restart efblock`.

## Mettre à jour

```bash
cd ~/efBlock && git pull && make install
```

## Dépannage

**`address already in use` sur le port 53** — un autre programme écoute déjà (souvent `systemd-resolved`
sur Ubuntu). Identifie-le avec `ss -lunp | grep ':53 '`. Pour libérer le port :

```bash
systemctl disable --now systemd-resolved
rm /etc/resolv.conf && echo "nameserver 1.1.1.1" > /etc/resolv.conf
```

**`tls://… i/o timeout` dans les logs** — le port 853 (DoT) est bloqué par ton réseau (fréquent dans les
écoles et entreprises). Utilise un upstream DoH, qui passe par le port 443 :

```yaml
upstreams:
  - "https://cloudflare-dns.com/dns-query"
  - "https://dns.quad9.net/dns-query"
```

**Un site ne marche plus** — ouvre le *Journal* de l'interface, cherche le domaine bloqué et clique sur
« Autoriser », ou mets le blocage en pause 5 minutes depuis le tableau de bord.

**Windows affiche « pas d'accès à internet »** alors que ça marche — vérifie que efBlock écoute bien sur le
port **53** (et non 5353) ; Windows teste sa connexion via le DNS.

**Les logs** : `journalctl -u efblock -n 50 --no-pager`. Pour le détail de chaque requête, lance
`/usr/local/bin/efblock -debug -config /etc/efblock/config.yaml` à la main (service arrêté).

## Limites

- Le blocage DNS arrête les pubs servies par des domaines **tiers**. Il ne peut pas bloquer celles qui
  viennent du même domaine que le contenu (ex. YouTube), ni masquer les cadres vides qu'elles laissent.
- Un appareil qui utilise son propre DNS chiffré (DoH intégré au navigateur, *iCloud Private Relay*)
  contourne efBlock.
- Pas de validation DNSSEC (le bit DO est transmis à l'upstream, la validation est laissée au client).
- Pas de règles par client.

## Développement

```bash
make test        # go test -race ./...
make build       # compile le binaire
make ui          # recompile l'interface Next.js (nécessite Node 22) et l'embarque
```

```
cmd/efblock/          point d'entrée : lit la config, assemble les composants
internal/config/      chargement et validation du YAML
internal/dnsserver/   serveur UDP/TCP : filtre → cache → upstream, journal
internal/filter/      parsing des listes (hosts, adblock), matching des domaines, mise à jour
internal/cache/       cache TTL + LRU + cache négatif
internal/upstream/    clients DNS classique, DoT, DoH
internal/querylog/    journal SQLite et statistiques
internal/store/       réglages modifiables depuis l'interface (state.json)
internal/web/         API JSON sécurisée + interface embarquée (dist/)
web-ui/               source de l'interface (Next.js, export statique)
deploy/               service systemd, script d'installation, config d'exemple
```

`internal/web/dist/` contient l'interface **compilée**, versionnée pour que `go build` fonctionne sans Node.
Après une modification de `web-ui/`, relance `make ui` et commite le résultat.
