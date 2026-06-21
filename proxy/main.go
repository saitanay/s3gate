package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"
)

const (
	listenAddr  = ":9000"
	backendAddr = "http://127.0.0.1:9001"
)

// contentLengthTransport wraps an http.RoundTripper and ensures
// Content-Length header is explicitly set when ContentLength is known.
// This prevents Go from switching to chunked transfer encoding.
type contentLengthTransport struct {
	rt http.RoundTripper
}

func (t *contentLengthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.ContentLength > 0 {
		req.Header.Set("Content-Length", strconv.FormatInt(req.ContentLength, 10))
		req.TransferEncoding = nil // clear any chunked encoding
	}
	return t.rt.RoundTrip(req)
}

func main() {
	backend, _ := url.Parse(backendAddr)

	baseTransport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 0, // no timeout — SFTP backend can be slow
		DisableCompression:    true,
		ExpectContinueTimeout: 1 * time.Second,
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = backend.Scheme
			req.URL.Host = backend.Host
			// req.Host left unchanged — preserves original Host for S3 signature verification

			// Strip Expect: 100-continue — rclone doesn't handle it
			req.Header.Del("Expect")
		},
		Transport:     &contentLengthTransport{rt: baseTransport},
		FlushInterval: -1, // flush immediately for streaming
	}

	server := &http.Server{
		Addr:           listenAddr,
		Handler:        proxy,
		ReadTimeout:    0, // no limit — large uploads take minutes
		WriteTimeout:   0, // no limit — large downloads take minutes
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20, // 1MB headers
	}

	log.Printf("S3 proxy listening on %s → %s", listenAddr, backendAddr)
	log.Fatal(server.ListenAndServe())
}
