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

# Start Go proxy in background (streams without buffering, preserves Content-Length)
echo "Starting S3Gate Go proxy on port 9000..."
s3proxy &
PROXY_PID=$!
sleep 1

if ! kill -0 $PROXY_PID 2>/dev/null; then
  echo "FATAL: Go proxy failed to start"
  exit 1
fi
echo "Go proxy started (PID $PROXY_PID)"

# Start rclone on internal port 9001
echo "Starting rclone S3 gateway on port 9001..."
exec rclone serve s3 storagebox:./ \
  --addr ":9001" \
  --vfs-cache-mode off \
  --transfers 8 \
  --checkers 8 \
  --sftp-concurrency 8 \
  --low-level-retries 10 \
  --retries 3 \
  --contimeout 30s \
  --no-checksum \
  --log-level INFO
