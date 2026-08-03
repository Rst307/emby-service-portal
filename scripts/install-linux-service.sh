#!/usr/bin/env bash
# Installs or updates a least-privilege systemd service. Run as root after
# placing the binary and .env in /opt/embyUserManager.
set -euo pipefail

APP_DIR="/opt/embyUserManager"
BINARY="$APP_DIR/emby-user-manager-linux-amd64"
ENV_FILE="$APP_DIR/.env"
DATA_DIR="$APP_DIR/data"
SERVICE_FILE="/etc/systemd/system/emby-user-manager.service"
SERVICE_USER="emby-user-manager"

if [[ $EUID -ne 0 ]]; then
  echo "Please run as root: sudo bash $0" >&2
  exit 1
fi
for file in "$BINARY" "$ENV_FILE"; do
  if [[ ! -f "$file" ]]; then
    echo "Required file is missing: $file" >&2
    exit 1
  fi
done

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --home-dir /nonexistent --shell /usr/sbin/nologin "$SERVICE_USER"
fi

install -d -o root -g root -m 0755 "$APP_DIR"
install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0700 "$DATA_DIR"
chown root:root "$BINARY" "$ENV_FILE"
chmod 0755 "$BINARY"
chmod 0600 "$ENV_FILE"

cat >"$SERVICE_FILE" <<EOF
[Unit]
Description=Emby User Manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$APP_DIR
EnvironmentFile=$ENV_FILE
ExecStart=$BINARY
Restart=on-failure
RestartSec=5
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
PrivateDevices=true
ProtectHome=true
ProtectSystem=strict
ProtectControlGroups=true
ProtectKernelModules=true
ProtectKernelTunables=true
RestrictSUIDSGID=true
LockPersonality=true
CapabilityBoundingSet=
AmbientCapabilities=
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6
ReadWritePaths=$DATA_DIR

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable emby-user-manager
systemctl restart emby-user-manager
sleep 1
systemctl --no-pager --full status emby-user-manager
