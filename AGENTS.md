# S3Gate v1.0

S3-compatible gateway that exposes a Hetzner StorageBox (SFTP) as an S3 endpoint via a custom Go reverse proxy + rclone.

## Architecture

```
Client → Traefik (HTTPS) → Go proxy (:9000) → rclone serve s3 (:9001) → SFTP → Hetzner StorageBox
                                ↓ (multipart uploads only)
                           /data/uploads (disk assembly)
                                ↓
                           rclone rcat → SFTP direct write
```

### Components

| File | Purpose |
|------|---------|
| `proxy/main.go` | Custom Go reverse proxy — handles multipart assembly, Expect:100-continue, chunked→Content-Length conversion |
| `entrypoint.sh` | Container entrypoint — SSH keyscan, rclone config, starts proxy + rclone |
| `Dockerfile` | Multi-stage build: Go proxy binary + rclone:latest |
| `docker-compose.yml` | Service definition with upload-cache volume |

### How Multipart Upload Works

1. `CreateMultipartUpload` → Go proxy creates temp dir `/data/uploads/{uploadId}/`
2. `UploadPart` → streams each part to disk (no RAM buffering)
3. `CompleteMultipartUpload` → concatenates parts, `rclone rcat` writes assembled file to SFTP
4. Non-multipart ops (GET, LIST, small PUT, DELETE) → proxied directly to rclone serve s3

### Why This Design

- **rclone serve s3 multipart is broken** (issue #7453) — buffers all parts in memory, loses state on large files
- **Go proxy solves**: disk-based assembly, Expect:100-continue stripping, chunked body buffering (Traefik strips Content-Length)
- **rclone rcat** bypasses serve s3's VFS layer for writes, avoids connection pool conflicts
- **SFTP write semaphore** (sem=1) prevents exceeding Hetzner's 10 connection limit

## Configuration

Environment variables:

| Variable | Purpose | Default |
|----------|---------|---------|
| `STORAGEBOX_HOST` | SFTP hostname | — |
| `STORAGEBOX_USERNAME` | SFTP user | — |
| `STORAGEBOX_PASSWORD` | SFTP password (obscured at runtime) | — |
| `S3_ACCESS_KEY_ID` | S3 access key | minioadmin |
| `S3_SECRET_ACCESS_KEY` | S3 secret key | minioadmin |

## Tested Capabilities

| Capability | Status |
|-----------|--------|
| Bucket CRUD | ✅ |
| File upload/download (small) | ✅ |
| 10MB upload with checksum verification | ✅ |
| Nested paths, special characters | ✅ |
| Copy, move, delete (single + bulk) | ✅ |
| 10 concurrent 5MB uploads | ✅ |
| 1GB multipart upload (single) | ✅ |
| 5 x 1GB parallel uploads | ✅ |
| Error handling (404, NoSuchBucket, BucketNotEmpty) | ✅ |
| Container stability under load | ✅ (0 restarts) |

## Limits

| Dimension | Value | Reason |
|-----------|-------|--------|
| Max file size | ~12GB | upload-cache volume size |
| SFTP write concurrency | 1 (serialized) | Hetzner 10 conn limit shared with reads |
| Upload throughput (same DC) | ~12 MiB/s per file | StorageBox SFTP limit |
| Max concurrent large uploads | 5 tested | limited by disk buffer, not connections |

## Development

```bash
docker compose up --build
# S3 endpoint at http://localhost:9000
aws s3 ls --endpoint-url http://localhost:9000 --region us-east-1
```

## Deployment

- **Live**: https://s3.x2u.in
- **Platform**: Coolify (auto-deploy on push to main)
- **Server**: Hetzner CX21 (8GB RAM, 75GB disk) at 195.201.131.232
- **UI**: https://app.coolify.io/project/jwvefol0ae3qsb6qwya2civ6
- **App ID**: `qnrbsm635onphksnx5qg0nfb`

## Volumes

- `upload-cache` → `/data/uploads` — multipart assembly workspace (12GB recommended)

## Security Notes

- rclone serve s3 runs without auth (internal only, Go proxy is the external gate)
- S3 auth via Go proxy (validates signatures on all requests except internal rcat)
- `.env` contains Coolify API key — never commit
- Credentials in docker-compose.yml are for local dev — use secrets in production
