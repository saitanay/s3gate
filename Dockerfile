# Stage 1: Build Go proxy
FROM golang:1.22-alpine AS builder
WORKDIR /build
COPY proxy/go.mod proxy/main.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o s3proxy main.go

# Stage 2: Runtime with rclone
FROM rclone/rclone:latest
RUN apk add --no-cache openssh-client
COPY --from=builder /build/s3proxy /usr/local/bin/s3proxy
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
