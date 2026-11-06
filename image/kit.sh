#!/usr/bin/env bash
# Télécharge le kit de paquets Debian (avec leurs dépendances) dans le dossier donné :
#   <dossier>/base : outils (dig, curl, nano, ping...), certificats, heure
#   <dossier>/ssh  : serveur SSH
# Le téléchargement se fait dans un conteneur Debian propre : les dépendances sont donc
# calculées comme sur un système minimal. Docker est requis.
set -euo pipefail
OUT="${1:?usage : image/kit.sh <dossier>}"
rm -rf "$OUT"; mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"

docker run --rm -i -v "$OUT:/out" debian:stable bash -s <<'KITSCRIPT'
set -e
# L'image Docker de Debian supprime les paquets téléchargés (docker-clean) : on le désactive.
rm -f /etc/apt/apt.conf.d/docker-clean
apt-get update -qq
dl() { # dl <dossier> <paquets...> : télécharge les paquets et leurs dépendances, sans les installer
	d=$1; shift
	mkdir -p "/out/$d/partial"
	apt-get install -y -qq --no-install-recommends --download-only \
		-o APT::Keep-Downloaded-Packages=true -o "Dir::Cache::archives=/out/$d" "$@"
	rm -rf "/out/$d/partial" "/out/$d/lock"
	echo "kit $d : $(ls /out/$d/*.deb 2>/dev/null | wc -l) paquets"
}
dl base ca-certificates systemd-timesyncd bind9-dnsutils iproute2 iputils-ping less nano curl
dl ssh openssh-server
KITSCRIPT

for d in base ssh; do
	ls "$OUT/$d"/*.deb >/dev/null 2>&1 || { echo "kit incomplet : $d sans paquet" >&2; exit 1; }
done
ls "$OUT/ssh"/openssh-server_*.deb >/dev/null 2>&1 || { echo "openssh-server absent du kit ssh" >&2; exit 1; }
echo "kit prêt : $(du -sh "$OUT" | cut -f1)"
