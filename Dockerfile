# Stage 1: Build the Pingora proxy
FROM rust:1.82-alpine AS builder

RUN apk add --no-cache musl-dev openssl-dev openssl-libs-static pkgconf cmake make perl

WORKDIR /build
COPY proxy/Cargo.toml proxy/Cargo.lock* ./
COPY proxy/src ./src

# On Alpine, cargo builds against musl by default — produces static binary
ENV OPENSSL_STATIC=1
RUN cargo build --release

# Stage 2: Final image with rclone + proxy
FROM rclone/rclone:latest

RUN apk add --no-cache openssh-client

# Copy proxy binary from builder
COPY --from=builder /build/target/release/s3gate-proxy /usr/local/bin/s3gate-proxy

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 9000

ENTRYPOINT ["/entrypoint.sh"]
