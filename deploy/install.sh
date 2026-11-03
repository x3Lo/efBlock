#!/usr/bin/env bash
# Installe (ou met à jour) efBlock comme service systemd.
# À lancer en root, depuis la racine du projet, après `make build` :
#     sudo ./deploy/install.sh
# Relançable sans risque : ta configuration existante n'est jamais écrasée.
set -euo pipefail

if [[ $EUID -ne 0 ]]; then
  echo "Lance ce script en root (sudo ./deploy/install.sh, ou su - puis ./deploy/install.sh)" >&2
  exit 1
fi

cd "$(dirname "$0")/.."
BIN=./efblock
[[ -x $BIN ]] || { echo "Binaire introuvable : lance d'abord 'make build'" >&2; exit 1; }

# 1. Utilisateur système dédié, sans shell ni dossier personnel.
if ! id efblock &>/dev/null; then
  useradd --system --no-create-home --home-dir /var/lib/efblock --shell /usr/sbin/nologin efblock
  echo "✓ utilisateur 'efblock' créé"
fi

# 2. Dossiers.
install -d -o efblock -g efblock -m 0750 /var/lib/efblock
install -d -o root -g efblock -m 0750 /etc/efblock

# 3. Binaire (remplacé à chaque mise à jour).
install -m 0755 "$BIN" /usr/local/bin/efblock
echo "✓ binaire installé dans /usr/local/bin/efblock"

# 4. Configuration : copiée seulement si elle n'existe pas encore.
if [[ ! -e /etc/efblock/config.yaml ]]; then
  if [[ -f config.yaml ]]; then
    install -o root -g efblock -m 0640 config.yaml /etc/efblock/config.yaml
    echo "✓ ton config.yaml actuel a été copié dans /etc/efblock/config.yaml"
  else
    install -o root -g efblock -m 0640 deploy/config.example.yaml /etc/efblock/config.yaml
    echo "! configuration d'exemple copiée : ÉDITE /etc/efblock/config.yaml (listen, password_hash...)"
  fi
else
  echo "✓ /etc/efblock/config.yaml conservé tel quel"
fi

# 5. Service.
install -m 0644 deploy/efblock.service /etc/systemd/system/efblock.service
systemctl daemon-reload
systemctl enable efblock >/dev/null
echo "✓ service installé et activé au démarrage"

# 6. Quelque chose occupe-t-il déjà le port 53 ? (typique : systemd-resolved sur Ubuntu)
if command -v ss >/dev/null && ss -lunp 2>/dev/null | grep -qE ':53\s' && ! systemctl is-active --quiet efblock; then
  echo "! Un autre programme écoute déjà sur le port 53 :"
  ss -lunp | grep -E ':53\s' || true
  echo "  Libère le port 53 avant de démarrer efBlock (voir README, section « Dépannage »)."
fi

# 7. (Re)démarrage.
systemctl restart efblock
sleep 1
if systemctl is-active --quiet efblock; then
  echo "✓ efBlock tourne. Logs : journalctl -u efblock -f"
else
  echo "✗ efBlock n'a pas démarré. Cause probable : journalctl -u efblock -n 30 --no-pager" >&2
  exit 1
fi
