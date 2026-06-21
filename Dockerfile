FROM rclone/rclone:latest

RUN apk add --no-cache openssh-client

# Copy pre-built Pingora proxy binary (cross-compiled for linux/amd64 musl)
COPY proxy/s3gate-proxy /usr/local/bin/s3gate-proxy
RUN chmod +x /usr/local/bin/s3gate-proxy

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 9000

ENTRYPOINT ["/entrypoint.sh"]
