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

# --- Console d'administration sur l'écran de la machine ---
in-target systemctl enable efblock efblock-resolv efblock-console

# --- Pas de message de démarrage bavard : l'écran appartient au menu ---
sed -i 's/^GRUB_CMDLINE_LINUX_DEFAULT=.*/GRUB_CMDLINE_LINUX_DEFAULT="quiet loglevel=3"/' /target/etc/default/grub
in-target update-grub || true

echo "== efBlock : terminé =="
exit 0
