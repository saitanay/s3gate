package main

import (
	"io"
	"log"
	"net/http"
	"time"
)

const (
	listenAddr  = ":9000"
	backendAddr = "http://127.0.0.1:9001"
)

func main() {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     90 * time.Second,
		// No timeout on response header — backend may be slow (SFTP)
		ResponseHeaderTimeout: 0,
		// Disable compression to preserve Content-Length
		DisableCompression: true,
		// Send Expect: 100-continue to backend, wait up to 1s
		ExpectContinueTimeout: 1 * time.Second,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Build upstream request
		upstreamURL := backendAddr + r.URL.RequestURI()
		proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, r.Body)
		if err != nil {
			http.Error(w, "Bad request", http.StatusBadRequest)
			return
		}

		// Copy all headers — preserves Content-Length, Content-Type, Authorization, etc.
		for key, values := range r.Header {
			for _, v := range values {
				proxyReq.Header.Add(key, v)
			}
		}

		// Preserve Content-Length explicitly (Go may drop it otherwise)
		if r.ContentLength >= 0 {
			proxyReq.ContentLength = r.ContentLength
		}

		// Strip Expect header — we handle 100-continue at this layer
		// Backend (rclone) doesn't support it
		proxyReq.Header.Del("Expect")

		// Forward Host
		proxyReq.Host = r.Host

		// Execute
		resp, err := transport.RoundTrip(proxyReq)
		if err != nil {
			log.Printf("upstream error: %v", err)
			http.Error(w, "Bad Gateway", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// Copy response headers
		for key, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(key, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		// Stream response body
		io.Copy(w, resp.Body)
	})

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// No read/write timeout — large transfers take minutes
		ReadTimeout:  0,
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
		// Allow large request bodies
		MaxHeaderBytes: 1 << 20, // 1MB headers
	}

	log.Printf("S3 proxy listening on %s → %s", listenAddr, backendAddr)
	log.Fatal(server.ListenAndServe())
}
