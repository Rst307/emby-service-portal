#!/usr/bin/env bash
# Installs or updates a least-privilege systemd service. Run as root after
# placing the binary and .env in /opt/emby-service-portal.
set -euo pipefail

APP_DIR="/opt/emby-service-portal"
BINARY="$APP_DIR/emby-service-portal-linux-amd64"
ENV_FILE="$APP_DIR/.env"
DATA_DIR="$APP_DIR/data"
SERVICE_FILE="/etc/systemd/system/emby-service-portal.service"
SERVICE_USER="emby-service-portal"

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

install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0750 "$APP_DIR"
install -d -o "$SERVICE_USER" -g "$SERVICE_USER" -m 0700 "$DATA_DIR"
# The binary and .env stay root-owned; the service user only needs write
# access to the directory (not the files) so the built-in self-update can
# rename the binary into place.
chown root:root "$BINARY" "$ENV_FILE"
chmod 0755 "$BINARY"
chmod 0600 "$ENV_FILE"

cat >"$SERVICE_FILE" <<EOF
[Unit]
Description=Emby Service Portal
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
# $APP_DIR must be writable for the built-in self-update (binary swap).
ReadWritePaths=$APP_DIR $DATA_DIR

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable emby-service-portal
systemctl restart emby-service-portal
sleep 1
systemctl --no-pager --full status emby-service-portal
