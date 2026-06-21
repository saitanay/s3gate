#!/bin/sh
set -e

HOST="${STORAGEBOX_HOST}"
USERNAME="${STORAGEBOX_USERNAME}"
PASSWORD="${STORAGEBOX_PASSWORD}"

ACCESS_KEY="${S3_ACCESS_KEY_ID:-minioadmin}"
SECRET_KEY="${S3_SECRET_ACCESS_KEY:-minioadmin}"

mkdir -p /root/.config/rclone /root/.ssh /tmp/vfs-cache

# ssh-keyscan with retries (flaky on port 23)
echo "Scanning SSH host keys for ${HOST}:23..."
for i in 1 2 3 4 5; do
  ssh-keyscan -p 23 "${HOST}" > /root/.ssh/known_hosts 2>/tmp/keyscan.log
  if [ -s /root/.ssh/known_hosts ]; then
    echo "Host keys acquired (attempt $i)"
    break
  fi
  echo "keyscan attempt $i failed, retrying in 3s..."
  cat /tmp/keyscan.log
  sleep 3
done

if [ ! -s /root/.ssh/known_hosts ]; then
  echo "FATAL: Could not get host keys after 5 attempts"
  cat /tmp/keyscan.log
  exit 1
fi

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
known_hosts_file = /root/.ssh/known_hosts
EOF

# Start nginx (streaming proxy — no buffering, preserves Content-Length)
echo "Starting nginx streaming proxy on port 9000..."
nginx
echo "Nginx proxy started"

# Start rclone on internal port 9001
echo "Starting rclone S3 gateway on port 9001..."
exec rclone serve s3 storagebox:./ \
  --addr ":9001" \
  --auth-key "${ACCESS_KEY},${SECRET_KEY}" \
  --vfs-cache-mode writes \
  --vfs-cache-max-size 10G \
  --vfs-write-back 0s \
  --cache-dir /tmp/vfs-cache \
  --transfers 8 \
  --checkers 8 \
  --sftp-concurrency 8 \
  --low-level-retries 10 \
  --retries 3 \
  --contimeout 30s \
  --no-checksum \
  --log-level INFO
