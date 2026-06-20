# S3Gate

S3-compatible gateway that exposes a Hetzner StorageBox (SFTP) as an S3 endpoint using rclone.

## Architecture

- **Dockerfile**: Alpine-based rclone image + openssh-client for SFTP connectivity
- **entrypoint.sh**: Configures rclone SFTP remote at runtime, starts `rclone serve s3`
- **docker-compose.yml**: Single service, port 9000, env-driven config

## How It Works

1. Container starts → ssh-keyscan grabs host keys (port 23)
2. Generates rclone.conf with SFTP backend (password obscured via `rclone obscure`)
3. Serves S3 API on configured port with VFS write-back caching

## Key Details

- StorageBox SFTP runs on port 23 (Hetzner default)
- S3 auth uses access key / secret key pair (defaults: minioadmin/minioadmin)
- VFS cache: writes mode, 2G max, 0s write-back (immediate flush)
- Transfers: 4 concurrent, 2 SFTP concurrency, checksums disabled

## Configuration

All config via environment variables:

| Variable | Purpose |
|----------|---------|
| `STORAGEBOX_HOST` | SFTP hostname |
| `STORAGEBOX_USERNAME` | SFTP user |
| `STORAGEBOX_PASSWORD` | SFTP password (plaintext, obscured at runtime) |
| `S3_ACCESS_KEY_ID` | S3 access key (default: minioadmin) |
| `S3_SECRET_ACCESS_KEY` | S3 secret key (default: minioadmin) |
| `S3_LISTEN_PORT` | Listen port (default: 9000) |

## Development

```bash
docker compose up --build
```

S3 endpoint available at `http://localhost:9000`.

## Deployment (Coolify)

- **Live**: https://s3.x2u.in
- **UI**: https://app.coolify.io/
- **Project**: https://app.coolify.io/project/jwvefol0ae3qsb6qwya2civ6/environment/nsnv9isg7o8vkvguu0t0g7cm/application/qnrbsm635onphksnx5qg0nfb
- **API key**: stored in `.env` as `COOLIFY_API_KEY`
- **Application ID**: `qnrbsm635onphksnx5qg0nfb`

Check deployments via API:
```bash
curl -s -H "Authorization: Bearer $COOLIFY_API_KEY" \
  "$COOLIFY_BASE_URL/api/v1/applications/$COOLIFY_APPLICATION_ID" | jq
```

## Security Notes

- Credentials in docker-compose.yml are for local dev — use secrets management in production
- No TLS termination in container — put behind reverse proxy for HTTPS
- `.env` contains API keys — never commit (add to .gitignore)
