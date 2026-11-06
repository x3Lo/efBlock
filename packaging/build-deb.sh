#!/usr/bin/env bash
# Construit le paquet Debian d'efBlock.
#     packaging/build-deb.sh [VERSION] [ARCH]      ex: packaging/build-deb.sh 1.0.0 amd64
# ARCH : amd64 (défaut) ou arm64. Le résultat est dans dist/efblock_VERSION_ARCH.deb
set -euo pipefail

cd "$(dirname "$0")/.."
VERSION="${1:-}"
ARCH="${2:-amd64}"

if [[ -z $VERSION ]]; then
	VERSION="$(git describe --tags --always 2>/dev/null | sed 's/^v//' || true)"
	[[ $VERSION =~ ^[0-9] ]] || VERSION="0.0.0+${VERSION:-local}"
fi
VERSION="${VERSION#v}"
[[ $ARCH == amd64 || $ARCH == arm64 ]] || { echo "architecture non gérée : $ARCH (amd64 ou arm64)" >&2; exit 1; }
command -v dpkg-deb >/dev/null || { echo "dpkg-deb est requis (paquet dpkg)" >&2; exit 1; }

ROOT="dist/deb-$ARCH"
rm -rf "$ROOT"
mkdir -p "$ROOT/DEBIAN" "$ROOT/usr/bin" "$ROOT/lib/systemd/system" "$ROOT/etc/efblock" "$ROOT/usr/share/doc/efblock"

# Binaires statiques : aucune dépendance à la bibliothèque C du système (CGO désactivé).
build() { # $1 = nom
	CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath \
		-ldflags "-s -w -X main.version=$VERSION" -o "$ROOT/usr/bin/$1" "./cmd/$1"
}
build efblock
build efblock-console

# Le service du paquet pointe sur /usr/bin (et non /usr/local/bin comme deploy/install.sh).
sed 's#/usr/local/bin/efblock#/usr/bin/efblock#' deploy/efblock.service >"$ROOT/lib/systemd/system/efblock.service"
install -m 0644 packaging/efblock-console.service "$ROOT/lib/systemd/system/efblock-console.service"
install -m 0640 packaging/config.default.yaml "$ROOT/etc/efblock/config.yaml"
install -m 0644 README.md "$ROOT/usr/share/doc/efblock/README.md"
cat >"$ROOT/usr/share/doc/efblock/copyright" <<COPY
Format: https://www.debian.org/doc/packaging-manuals/copyright-format/1.0/
Upstream-Name: efBlock
Source: https://github.com/x3Lo/efBlock

Files: *
Copyright: 2026 x3Lo
License: MIT
 Le texte complet de la licence MIT est dans /usr/share/doc/efblock/LICENSE.
COPY
install -m 0644 LICENSE "$ROOT/usr/share/doc/efblock/LICENSE"

install -m 0755 packaging/postinst packaging/prerm packaging/postrm "$ROOT/DEBIAN/"
echo "/etc/efblock/config.yaml" >"$ROOT/DEBIAN/conffiles" # jamais écrasé par une mise à jour

SIZE=$(du -sk "$ROOT" | cut -f1)
cat >"$ROOT/DEBIAN/control" <<CTRL
Package: efblock
Version: $VERSION
Architecture: $ARCH
Maintainer: efBlock <noreply@users.noreply.github.com>
Installed-Size: $SIZE
Recommends: ca-certificates, bind9-dnsutils
Section: net
Priority: optional
Homepage: https://github.com/x3Lo/efBlock
Description: bloqueur de publicités DNS avec interface web
 Serveur DNS qui bloque publicités et traqueurs pour tout le réseau. Listes de
 filtres mises à jour automatiquement, DNS chiffré (DoT, DoH), cache, groupes de
 clients, journal des requêtes en direct et interface web d'administration.
CTRL

OUT="dist/efblock_${VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$ROOT" "$OUT" >/dev/null
echo "$OUT"
