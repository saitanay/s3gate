#!/bin/sh
set -e

CONFIG_FILE="/app/config.json"

HOST=$(jq -r '.storagebox.host' "$CONFIG_FILE")
USERNAME=$(jq -r '.storagebox.username' "$CONFIG_FILE")
PASSWORD=$(jq -r '.storagebox.password' "$CONFIG_FILE")
PROTOCOL=$(jq -r '.storagebox.protocol' "$CONFIG_FILE")

ACCESS_KEY=$(jq -r '.s3.access_key_id' "$CONFIG_FILE")
SECRET_KEY=$(jq -r '.s3.secret_access_key' "$CONFIG_FILE")
PORT=$(jq -r '.s3.listen_port' "$CONFIG_FILE")

mkdir -p /root/.config/rclone

cat > /root/.config/rclone/rclone.conf <<EOF
[storagebox]
type = webdav
url = https://${HOST}
vendor = other
user = ${USERNAME}
pass = $(rclone obscure "${PASSWORD}")
EOF

echo "Starting S3 gateway on port ${PORT}..."
exec rclone serve s3 storagebox: \
  --addr ":${PORT}" \
  --auth-key "${ACCESS_KEY}" \
  --auth-secret "${SECRET_KEY}" \
  --vfs-cache-mode full
