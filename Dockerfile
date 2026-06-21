# Stage 1: Build Go binary
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o bucketcheap .

# Stage 2: Runtime with rclone
FROM rclone/rclone:latest
RUN apk add --no-cache openssh-client sqlite
COPY --from=builder /build/bucketcheap /usr/local/bin/bucketcheap
COPY --from=builder /build/web/templates /app/web/templates
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
