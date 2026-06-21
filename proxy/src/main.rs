use async_trait::async_trait;
use pingora::prelude::*;
use pingora::proxy::{ProxyHttp, Session};
use pingora::upstreams::peer::HttpPeer;

/// S3Gate reverse proxy — streams requests/responses without buffering,
/// handles Expect: 100-continue by short-circuiting it to the client
/// and stripping it before forwarding to rclone upstream.
struct S3Proxy;

#[async_trait]
impl ProxyHttp for S3Proxy {
    type CTX = ();

    fn new_ctx(&self) -> Self::CTX {}

    async fn upstream_peer(
        &self,
        _session: &mut Session,
        _ctx: &mut Self::CTX,
    ) -> Result<Box<HttpPeer>> {
        // rclone serve s3 listens on 127.0.0.1:9001
        let peer = Box::new(HttpPeer::new(("127.0.0.1", 9001), false, String::new()));
        Ok(peer)
    }

    async fn upstream_request_filter(
        &self,
        _session: &mut Session,
        upstream_request: &mut RequestHeader,
        _ctx: &mut Self::CTX,
    ) -> Result<()> {
        // Strip Expect: 100-continue before forwarding to rclone.
        // Pingora already sent 100 Continue to the client when the body was polled.
        upstream_request.remove_header("Expect");
        Ok(())
    }
}

fn main() {
    env_logger::init();

    let mut server = Server::new(None).unwrap();
    server.bootstrap();

    let mut proxy = http_proxy_service(&server.configuration, S3Proxy);

    // Listen on 0.0.0.0:9000 — the container's exposed port
    proxy.add_tcp("0.0.0.0:9000");

    server.add_service(proxy);
    server.run_forever();
}
