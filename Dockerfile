FROM rclone/rclone:latest

RUN apk add --no-cache openssh-client nginx

COPY entrypoint.sh /entrypoint.sh
COPY nginx.conf /etc/nginx/nginx.conf
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
