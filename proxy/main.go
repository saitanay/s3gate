package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

const (
	listenAddr  = ":9000"
	backendAddr = "http://127.0.0.1:9001"
	// Max body to buffer: 100MB (multipart parts are 8MB, single PUTs up to 5GB won't use this path)
	maxBufferSize = 100 * 1024 * 1024
)

type readCloserBuffer struct {
	*bytes.Reader
}

func (r *readCloserBuffer) Close() error { return nil }
func (r *readCloserBuffer) Len() int     { return r.Reader.Len() }

func bufferBody(r *http.Request) (*readCloserBuffer, error) {
	buf, err := io.ReadAll(io.LimitReader(r.Body, maxBufferSize))
	r.Body.Close()
	if err != nil {
		return nil, err
	}
	return &readCloserBuffer{bytes.NewReader(buf)}, nil
}

func main() {
	backend, _ := url.Parse(backendAddr)

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = backend.Scheme
			req.URL.Host = backend.Host
			// req.Host unchanged — preserves S3 signature

			// Strip Expect: 100-continue — rclone doesn't handle it
			req.Header.Del("Expect")
		},
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 3600 * time.Second, // 1hr for slow SFTP
			DisableCompression:    true,
			ExpectContinueTimeout: 1 * time.Second,
		},
		FlushInterval: -1, // flush response immediately for streaming downloads
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength < 0 && r.Body != nil && (r.Method == "PUT" || r.Method == "POST") {
			// Body arrived chunked (Traefik strips Content-Length).
			// Buffer to determine length — needed because rclone requires Content-Length.
			// AWS CLI multipart parts are 8MB default, max 100MB — safe to buffer.
			body, err := bufferBody(r)
			if err != nil {
				log.Printf("WARN buffering body: %v (client likely disconnected)", err)
				return // don't send error response — client is gone
			}
			r.Body = body
			r.ContentLength = int64(body.Len())
			r.Header.Set("Content-Length", fmt.Sprintf("%d", r.ContentLength))
			r.TransferEncoding = nil
		}
		proxy.ServeHTTP(w, r)
	})

	server := &http.Server{
		Addr:           listenAddr,
		Handler:        handler,
		ReadTimeout:    0,
		WriteTimeout:   0,
		IdleTimeout:    120 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	log.Printf("S3 proxy listening on %s → %s", listenAddr, backendAddr)
	log.Fatal(server.ListenAndServe())
}
