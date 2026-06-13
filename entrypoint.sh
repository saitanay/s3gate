#!/bin/sh
set -e

HOST="${STORAGEBOX_HOST}"
USERNAME="${STORAGEBOX_USERNAME}"
PASSWORD="${STORAGEBOX_PASSWORD}"

ACCESS_KEY="${S3_ACCESS_KEY_ID:-minioadmin}"
SECRET_KEY="${S3_SECRET_ACCESS_KEY:-minioadmin}"
PORT="${S3_LISTEN_PORT:-9000}"

mkdir -p /root/.config/rclone

cat > /root/.config/rclone/rclone.conf <<EOF
[storagebox]
type = sftp
host = ${HOST}
port = 23
user = ${USERNAME}
pass = $(rclone obscure "${PASSWORD}")
shell_type = none
md5sum_command = none
sha1sum_command = none
known_hosts_file = /dev/null
EOF

echo "Starting S3 gateway on port ${PORT}..."
exec rclone serve s3 storagebox: \
  --addr ":${PORT}" \
  --auth-key "${ACCESS_KEY},${SECRET_KEY}" \
  --vfs-cache-mode full
