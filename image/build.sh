#!/usr/bin/env bash
# Construit l'ISO d'installation d'efBlock à partir de l'ISO « netinst » officielle de Debian.
#
#     image/build.sh [VERSION]
#
# Il faut une machine Linux avec : xorriso, curl, sha256sum, go (pour construire le paquet).
#     Debian / Ubuntu :  sudo apt install xorriso curl golang-go
# Variables facultatives :
#     BASE_ISO=/chemin/debian-netinst.iso    utilise cette ISO au lieu de télécharger la dernière
#     EFBLOCK_DEB=/chemin/efblock_X_amd64.deb  utilise ce paquet au lieu de le construire
# Résultat : dist/efblock-VERSION-amd64.iso (+ son empreinte .sha256)
set -euo pipefail

cd "$(dirname "$0")/.."
VERSION="${1:-}"
if [[ -z $VERSION ]]; then
	VERSION="$(git describe --tags --always 2>/dev/null | sed 's/^v//' || true)"
	[[ $VERSION =~ ^[0-9] ]] || VERSION="0.0.0+${VERSION:-local}"
fi
VERSION="${VERSION#v}"

for tool in xorriso curl sha256sum; do
	command -v "$tool" >/dev/null || { echo "outil manquant : $tool" >&2; exit 1; }
done
mkdir -p dist

# 1. Le paquet efBlock.
DEB="${EFBLOCK_DEB:-dist/efblock_${VERSION}_amd64.deb}"
if [[ ! -f $DEB ]]; then
	echo "==> construction du paquet efBlock $VERSION"
	packaging/build-deb.sh "$VERSION" amd64 >/dev/null
fi
[[ -f $DEB ]] || { echo "paquet introuvable : $DEB" >&2; exit 1; }

# 2. L'ISO de base : l'installateur officiel de Debian, vérifié par son empreinte SHA-256.
BASE="${BASE_ISO:-}"
if [[ -z $BASE ]]; then
	URL="https://cdimage.debian.org/debian-cd/current/amd64/iso-cd"
	echo "==> recherche de la dernière ISO netinst de Debian"
	curl -fsSL "$URL/SHA256SUMS" -o dist/debian-SHA256SUMS
	LINE="$(grep -E ' \*?debian-[0-9.]+-amd64-netinst\.iso$' dist/debian-SHA256SUMS | head -n1)"
	[[ -n $LINE ]] || { echo "ISO netinst introuvable dans $URL" >&2; exit 1; }
	NAME="$(echo "$LINE" | awk '{print $2}' | tr -d '*')"
	WANT="$(echo "$LINE" | awk '{print $1}')"
	BASE="dist/$NAME"
	if [[ ! -f $BASE ]] || [[ "$(sha256sum "$BASE" | cut -d' ' -f1)" != "$WANT" ]]; then
		echo "==> téléchargement de $NAME"
		curl -fL --progress-bar -o "$BASE" "$URL/$NAME"
	fi
	[[ "$(sha256sum "$BASE" | cut -d' ' -f1)" == "$WANT" ]] || { echo "empreinte incorrecte pour $NAME : fichier rejeté" >&2; rm -f "$BASE"; exit 1; }
fi
[[ -f $BASE ]] || { echo "ISO de base introuvable : $BASE" >&2; exit 1; }

# Le support doit avoir la structure attendue : on préfère s'arrêter que produire une ISO qui ne démarre pas.
has() { xorriso -indev "$BASE" -find "$1" -name "$(basename "$1")" 2>/dev/null | grep -q "$1"; }
has /install.amd/vmlinuz || { echo "$BASE n'a pas /install.amd/vmlinuz : ISO non gérée (amd64 netinst attendue)" >&2; exit 1; }
has /boot/grub/grub.cfg || { echo "$BASE n'a pas /boot/grub/grub.cfg : démarrage UEFI impossible" >&2; exit 1; }
MAP_ISOLINUX=1
if ! has /isolinux/txt.cfg; then
	echo "avertissement : pas de menu BIOS (isolinux) dans cette ISO : seul le démarrage UEFI sera modifié" >&2
	MAP_ISOLINUX=0
fi

# 2b. Le kit : les paquets Debian utiles (outils, serveur SSH...) avec leurs dépendances, pour que
# l'installation et le déploiement fonctionnent sans internet. On les télécharge ici, une fois, dans
# un conteneur Debian propre ; l'installateur les prend ensuite sur l'ISO. KIT_DIR permet d'en fournir un.
KIT="${KIT_DIR:-}"
if [[ -z $KIT ]]; then
	if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
		KIT="dist/kit"
		rm -rf "$KIT"; mkdir -p "$KIT"
		echo "==> téléchargement du kit de paquets Debian"
		docker run --rm -v "$PWD/$KIT:/out" debian:stable bash -s <<'KITSCRIPT'
set -e
apt-get update -qq
dl() { # dl <dossier> <paquets...> : télécharge les paquets et leurs dépendances, sans les installer
	d=$1; shift
	mkdir -p "/out/$d/partial"
	apt-get install -y --no-install-recommends --download-only -o "Dir::Cache::archives=/out/$d" "$@" >/dev/null
	rm -rf "/out/$d/partial" "/out/$d/lock"
}
dl base ca-certificates systemd-timesyncd bind9-dnsutils iproute2 iputils-ping less nano curl
dl ssh openssh-server
KITSCRIPT
	else
		echo "avertissement : docker indisponible : l'ISO sera construite SANS le kit de paquets (outils et serveur SSH absents hors ligne)" >&2
		KIT=""
	fi
fi
if [[ -n $KIT ]]; then
	for d in base ssh; do
		ls "$KIT/$d"/*.deb >/dev/null 2>&1 || { echo "kit incomplet : $KIT/$d sans paquet" >&2; exit 1; }
	done
fi

# 3. Ce qu'on ajoute au support.
STAGE="dist/iso-stage"
rm -rf "$STAGE"
mkdir -p "$STAGE/efblock"
cat image/files/preseed-common.cfg image/files/preseed-partman-auto.cfg >"$STAGE/efblock/preseed-auto.cfg"
cp image/files/late.sh "$STAGE/efblock/late.sh"
cp "$DEB" "$STAGE/efblock/efblock_${VERSION}_amd64.deb"
[[ -n $KIT ]] && cp -r "$KIT" "$STAGE/efblock/kit"

# 4. On refait l'ISO. « -boot_image any replay » reproduit à l'identique le démarrage BIOS, UEFI
# et la clé USB de l'ISO d'origine : on ne touche qu'aux fichiers ci-dessous.
OUT="dist/efblock-${VERSION}-amd64.iso"
rm -f "$OUT"
echo "==> assemblage de $OUT"
ARGS=(-indev "$BASE" -outdev "$OUT" -boot_image any replay
	-map "$STAGE/efblock" /efblock
	-map image/files/grub.cfg /boot/grub/grub.cfg)
if [[ $MAP_ISOLINUX == 1 ]]; then
	ARGS+=(-map image/files/txt.cfg /isolinux/txt.cfg)
	# Menu BIOS réduit à l'entrée efBlock (sans « Graphical install », options avancées, etc.).
	# Seulement si la base a le fichier de style que ce menu inclut.
	if has /isolinux/menu.cfg && has /isolinux/stdmenu.cfg; then
		ARGS+=(-map image/files/menu.cfg /isolinux/menu.cfg)
	fi
fi
xorriso "${ARGS[@]}" -commit >/dev/null

( cd dist && sha256sum "$(basename "$OUT")" >"$(basename "$OUT").sha256" )
echo "==> terminé : $OUT ($(du -h "$OUT" | cut -f1))"
