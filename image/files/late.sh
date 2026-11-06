#!/bin/sh
# Exécuté par l'installateur à la fin de l'installation, avec le futur système monté dans /target.
# Il installe efBlock et règle le système en appliance : réseau, console d'administration, DNS.
# Les sorties sont gardées dans /var/log/efblock-install.log du système installé.
LOG=/target/var/log/efblock-install.log
exec >>"$LOG" 2>&1
echo "== efBlock : fin d'installation $(date) =="

DEB=$(ls /cdrom/efblock/efblock_*.deb 2>/dev/null | head -n1)
[ -n "$DEB" ] || { echo "ERREUR : paquet efBlock introuvable sur le support"; exit 0; }
cp "$DEB" /target/tmp/efblock.deb
in-target dpkg -i /tmp/efblock.deb || echo "ERREUR : installation du paquet efBlock"
rm -f /target/tmp/efblock.deb

# --- Kit hors ligne : outils (dig, curl, nano, ping, certificats) installés depuis l'ISO ---
# Le kit est aussi copié sur le disque (serveur SSH inclus) : la console l'utilise sans internet.
if [ -d /cdrom/efblock/kit ]; then
    mkdir -p /target/usr/share/efblock
    cp -r /cdrom/efblock/kit /target/usr/share/efblock/kit
    if in-target sh -c 'dpkg -i --skip-same-version /usr/share/efblock/kit/base/*.deb'; then
        echo "kit : outils installés"
    else
        echo "AVERTISSEMENT : installation du kit incomplète"
        in-target dpkg --configure -a || true
    fi
else
    echo "AVERTISSEMENT : pas de kit sur le support : outils et serveur SSH absents hors ligne"
fi

# --- Dépôt Debian pour un « apt install » ultérieur (facultatif : l'installation n'en a pas besoin) ---
mkdir -p /target/etc/apt/sources.list.d
cat >/target/etc/apt/sources.list.d/debian.sources <<'SRC'
Types: deb
URIs: http://deb.debian.org/debian
Suites: stable stable-updates
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg
SRC

# --- Réseau : systemd-networkd (c'est aussi lui que pilote le menu de la console) ---
if in-target test -x /usr/lib/systemd/systemd-networkd; then
    in-target apt-get -y purge ifupdown isc-dhcp-client isc-dhcp-common || true
    rm -f /target/etc/network/interfaces
    mkdir -p /target/etc/systemd/network
    cat >/target/etc/systemd/network/20-dhcp.network <<'NET'
[Match]
Name=en* eth*

[Network]
DHCP=ipv4
NET
    in-target systemctl enable systemd-networkd
else
    echo "AVERTISSEMENT : systemd-networkd absent : le réseau reste géré par ifupdown, l'option « Réseau » de la console ne fonctionnera pas"
fi

# --- DNS de la machine elle-même : efBlock, puis un serveur de secours ---
# Réécrit à chaque démarrage : rien d'autre (DHCP, hooks) ne doit en prendre le contrôle.
cat >/target/etc/systemd/system/efblock-resolv.service <<'UNIT'
[Unit]
Description=DNS de la machine : efBlock, puis un secours
Before=efblock.service network.target

[Service]
Type=oneshot
RemainAfterExit=yes
ExecStart=/bin/sh -c 'rm -f /etc/resolv.conf; printf "nameserver 127.0.0.1\nnameserver 1.1.1.1\n" > /etc/resolv.conf'

[Install]
WantedBy=multi-user.target
UNIT

# --- Allègement : retire ce qui ne sert pas sur un serveur DNS ---
# (outils de l'installateur, détection d'autres systèmes, pages de manuel, documentation)
for p in installation-report tasksel tasksel-data os-prober laptop-detect reportbug popularity-contest man-db manpages; do
    if in-target dpkg -s "$p" >/dev/null 2>&1; then
        in-target apt-get -y purge "$p" || echo "AVERTISSEMENT : $p non retiré"
    fi
done
in-target apt-get clean || true
rm -rf /target/usr/share/doc/*/changelog* /target/usr/share/man/* /target/usr/share/info/* 2>/dev/null || true

# --- Console d'administration sur l'écran de la machine ---
in-target systemctl enable efblock efblock-resolv efblock-console efblock-update.path

# --- Pas de message de démarrage bavard : l'écran appartient au menu ---
sed -i 's/^GRUB_CMDLINE_LINUX_DEFAULT=.*/GRUB_CMDLINE_LINUX_DEFAULT="quiet loglevel=3"/' /target/etc/default/grub
in-target update-grub || true

echo "== efBlock : terminé =="
exit 0
