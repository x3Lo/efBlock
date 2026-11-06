#!/usr/bin/env bash
# Teste l'ISO pour de vrai : installation complète dans une machine virtuelle SANS internet,
# examen du disque installé, puis démarrage du système et vérification du serveur DNS.
#
#   image/test-install.sh dist/efblock-X.Y.Z-amd64.iso
#
# Requiert qemu-system-x86, xorriso, dig (bind9-dnsutils) ; KVM fortement conseillé (sinon très lent).
# Les journaux (console série de l'installateur et du système) vont dans dist/test-install/.
set -euo pipefail
ISO="${1:?usage : image/test-install.sh <iso>}"
OUT="dist/test-install"
INSTALL_TIMEOUT="${INSTALL_TIMEOUT:-2400}" # secondes
BOOT_TIMEOUT="${BOOT_TIMEOUT:-300}"
rm -rf "$OUT"; mkdir -p "$OUT"
fail() { echo "ÉCHEC : $*" >&2; exit 1; }

ACCEL=(-accel tcg)
if [[ -w /dev/kvm ]]; then ACCEL=(-enable-kvm -cpu host); else echo "avertissement : pas de KVM, l'installation sera lente" >&2; fi

# 1. Noyau et initrd de l'installateur, pour démarrer sans passer par le menu (qui attend un choix).
# Les paramètres sont ceux de l'entrée « Installer efBlock », plus les réponses aux questions de
# langue et de clavier (normalement posées à l'écran) et la console série pour suivre l'installation.
xorriso -osirrox on -indev "$ISO" -extract /install.amd/vmlinuz "$OUT/vmlinuz" -extract /install.amd/initrd.gz "$OUT/initrd.gz" >/dev/null 2>&1 ||
	fail "noyau ou initrd introuvable dans l'ISO"
ENTRY="$(xorriso -osirrox on -indev "$ISO" -extract /isolinux/txt.cfg /dev/stdout 2>/dev/null | grep -m1 'append' || true)"
grep -q 'preseed/file=/cdrom/efblock/preseed-auto.cfg' <<<"$ENTRY" || fail "l'entrée du menu ne charge pas preseed-auto.cfg : $ENTRY"
CMDLINE="auto=true priority=critical preseed/file=/cdrom/efblock/preseed-auto.cfg"
CMDLINE+=" debian-installer/locale=fr_FR.UTF-8 debian-installer/language=fr debian-installer/country=FR"
CMDLINE+=" keyboard-configuration/xkb-keymap=fr console-setup/ask_detect=false"
CMDLINE+=" console=ttyS0,115200n8 --- console=ttyS0,115200n8"

# 2. Installation. Réseau QEMU « restreint » : DHCP fourni, mais AUCUN accès à internet.
DISK="$OUT/disk.img"
truncate -s 8G "$DISK"
echo "==> installation (sans internet, $INSTALL_TIMEOUT s au plus)"
set +e
timeout "$INSTALL_TIMEOUT" qemu-system-x86_64 "${ACCEL[@]}" -m 2048 -smp 2 -no-reboot -nographic \
	-drive file="$DISK",format=raw,if=virtio \
	-cdrom "$ISO" \
	-nic user,model=virtio-net-pci,restrict=on \
	-kernel "$OUT/vmlinuz" -initrd "$OUT/initrd.gz" -append "$CMDLINE" \
	>"$OUT/installation.log" 2>&1 </dev/null
code=$?
set -e
[[ $code -eq 124 ]] && { tail -40 "$OUT/installation.log" >&2; fail "installation trop longue (bloquée sur une question ?)"; }
[[ $code -ne 0 ]] && { tail -40 "$OUT/installation.log" >&2; fail "qemu s'est arrêté avec le code $code"; }
echo "    installation terminée"

# 3. Examen du disque installé.
echo "==> examen du disque"
LOOP="$(sudo losetup -P --find --show "$DISK")"
trap 'sudo umount "$OUT/mnt" 2>/dev/null || true; sudo losetup -d "$LOOP" 2>/dev/null || true' EXIT
mkdir -p "$OUT/mnt"
ROOT=""
for p in "$LOOP"p*; do
	if sudo mount -o ro "$p" "$OUT/mnt" 2>/dev/null; then
		if [[ -f "$OUT/mnt/etc/debian_version" ]]; then ROOT="$p"; break; fi
		sudo umount "$OUT/mnt"
	fi
done
[[ -n $ROOT ]] || fail "aucune partition système trouvée sur le disque installé"
M="$OUT/mnt"
sudo cp "$M/var/log/efblock-install.log" "$OUT/efblock-install.log" 2>/dev/null || fail "journal d'installation d'efBlock absent : late.sh n'a pas tourné"
sudo chmod 644 "$OUT/efblock-install.log"
echo "    --- efblock-install.log ---"; sed 's/^/    /' "$OUT/efblock-install.log"
grep -q "ERREUR" "$OUT/efblock-install.log" && fail "erreur dans le journal d'installation"
grep -q "kit : outils installés" "$OUT/efblock-install.log" || fail "le kit hors ligne n'a pas été installé"
installed() { sudo grep -A2 "^Package: $1\$" "$M/var/lib/dpkg/status" | grep -q "Status: install ok installed"; }
for pkg in efblock bind9-dnsutils curl nano ca-certificates; do
	installed "$pkg" || fail "paquet $pkg non installé"
	echo "    paquet $pkg : installé"
done
for unit in efblock efblock-console efblock-update.path systemd-networkd; do
	sudo find "$M/etc/systemd/system" -name "$unit*" | grep -q . || fail "service $unit non activé"
	echo "    service $unit : activé"
done
[[ -f "$M/usr/share/efblock/kit/ssh/"openssh-server_*.deb ]] || ls "$M"/usr/share/efblock/kit/ssh/openssh-server_*.deb >/dev/null 2>&1 || fail "kit SSH absent du disque"
sudo umount "$M"; sudo losetup -d "$LOOP"; trap - EXIT

# 4. Démarrage du système installé (toujours sans internet) et vérification du DNS.
echo "==> démarrage du système installé"
qemu-system-x86_64 "${ACCEL[@]}" -m 1024 -smp 2 -nographic \
	-drive file="$DISK",format=raw,if=virtio \
	-nic user,model=virtio-net-pci,restrict=on,hostfwd=udp:127.0.0.1:5353-:53,hostfwd=tcp:127.0.0.1:5353-:53 \
	>"$OUT/demarrage.log" 2>&1 </dev/null &
QPID=$!
trap 'kill $QPID 2>/dev/null || true' EXIT
ok=""
for _ in $(seq 1 "$BOOT_TIMEOUT"); do
	# Une recherche inverse d'adresse privée reçoit toujours sa réponse d'efBlock lui-même, tout de suite
	# (ces noms ne partent jamais sur internet) : une réponse prouve qu'efBlock tourne et répond.
	if out="$(dig +time=2 +tries=1 @127.0.0.1 -p 5353 -x 192.168.1.1 2>/dev/null)" && grep -q "status:" <<<"$out"; then
		ok="$out"; break
	fi
	sleep 1
done
[[ -n $ok ]] || { tail -30 "$OUT/demarrage.log" >&2; fail "le serveur DNS d'efBlock ne répond pas après le démarrage"; }
echo "    DNS : $(grep -o 'status: [A-Z]*' <<<"$ok")"
kill $QPID 2>/dev/null || true; trap - EXIT
echo "==> réussi : l'ISO s'installe sans internet et efBlock répond"
