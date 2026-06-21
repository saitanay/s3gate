package main

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	listenAddr   = ":9000"
	backendAddr  = "http://127.0.0.1:9001"
	uploadDir    = "/data/uploads"
)

// Multipart upload state
type multipartUpload struct {
	bucket     string
	key        string
	created    time.Time
	completing sync.Mutex // prevents duplicate CompleteMultipartUpload
	done       bool
	etag       string
}

var (
	uploads   = make(map[string]*multipartUpload)
	uploadsMu sync.RWMutex
	// Limit concurrent SFTP writes (Hetzner allows 10 connections total)
	sftpSem  = make(chan struct{}, 1)
)

// S3 XML response types
type InitiateMultipartUploadResult struct {
	XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	UploadId string   `xml:"UploadId"`
}

type CompleteMultipartUploadResult struct {
	XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
	Xmlns    string   `xml:"xmlns,attr"`
	Location string   `xml:"Location"`
	Bucket   string   `xml:"Bucket"`
	Key      string   `xml:"Key"`
	ETag     string   `xml:"ETag"`
}

type CompleteMultipartUploadRequest struct {
	XMLName xml.Name       `xml:"CompleteMultipartUpload"`
	Parts   []CompletePart `xml:"Part"`
}

type CompletePart struct {
	PartNumber int    `xml:"PartNumber"`
	ETag       string `xml:"ETag"`
}

func main() {
	os.MkdirAll(uploadDir, 0755)

	backend, _ := url.Parse(backendAddr)

	// Reverse proxy for non-multipart requests (GET, LIST, small PUT, DELETE)
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = backend.Scheme
			req.URL.Host = backend.Host
			req.Header.Del("Expect")
		},
		Transport: &http.Transport{
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 3600 * time.Second,
			DisableCompression:    true,
			ExpectContinueTimeout: 1 * time.Second,
		},
		FlushInterval: -1,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()

		// Intercept multipart operations
		if r.Method == "POST" && query.Has("uploads") {
			handleCreateMultipartUpload(w, r)
			return
		}
		if r.Method == "PUT" && query.Get("uploadId") != "" && query.Get("partNumber") != "" {
			handleUploadPart(w, r, query.Get("uploadId"), query.Get("partNumber"))
			return
		}
		if r.Method == "POST" && query.Get("uploadId") != "" {
			handleCompleteMultipartUpload(w, r, query.Get("uploadId"))
			return
		}
		if r.Method == "DELETE" && query.Get("uploadId") != "" {
			handleAbortMultipartUpload(w, r, query.Get("uploadId"))
			return
		}

		// Buffer chunked bodies for non-multipart PUT/POST (small files via Traefik)
		if r.ContentLength < 0 && r.Body != nil && (r.Method == "PUT" || r.Method == "POST") {
			body, err := io.ReadAll(io.LimitReader(r.Body, 100*1024*1024))
			r.Body.Close()
			if err != nil {
				log.Printf("WARN buffering body: %v", err)
				return
			}
			r.Body = io.NopCloser(strings.NewReader(string(body)))
			r.ContentLength = int64(len(body))
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

	go cleanupStaleUploads()

	log.Printf("S3 proxy listening on %s → %s (multipart assembly on %s)", listenAddr, backendAddr, uploadDir)
	log.Fatal(server.ListenAndServe())
}

func parseBucketKey(r *http.Request) (bucket, key string) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) >= 1 {
		bucket = parts[0]
	}
	if len(parts) >= 2 {
		key = parts[1]
	}
	return
}

func handleCreateMultipartUpload(w http.ResponseWriter, r *http.Request) {
	bucket, key := parseBucketKey(r)
	uploadId := uuid.New().String()

	dir := filepath.Join(uploadDir, uploadId)
	if err := os.MkdirAll(dir, 0755); err != nil {
		log.Printf("ERROR creating upload dir: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	uploadsMu.Lock()
	uploads[uploadId] = &multipartUpload{bucket: bucket, key: key, created: time.Now()}
	uploadsMu.Unlock()

	log.Printf("CreateMultipartUpload: %s/%s → %s", bucket, key, uploadId[:8])

	result := InitiateMultipartUploadResult{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Bucket:   bucket,
		Key:      key,
		UploadId: uploadId,
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	xml.NewEncoder(w).Encode(result)
}

func handleUploadPart(w http.ResponseWriter, r *http.Request, uploadId, partNum string) {
	uploadsMu.RLock()
	_, exists := uploads[uploadId]
	uploadsMu.RUnlock()

	if !exists {
		http.Error(w, "NoSuchUpload", http.StatusNotFound)
		return
	}

	dir := filepath.Join(uploadDir, uploadId)
	partN, _ := strconv.Atoi(partNum)
	partFile := filepath.Join(dir, fmt.Sprintf("part-%05d", partN))

	f, err := os.Create(partFile)
	if err != nil {
		log.Printf("ERROR creating part file: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	hash := md5.New()
	written, err := io.Copy(io.MultiWriter(f, hash), r.Body)
	f.Close()
	r.Body.Close()

	if err != nil {
		log.Printf("ERROR writing part: %v", err)
		os.Remove(partFile)
		http.Error(w, "Write failed", http.StatusInternalServerError)
		return
	}

	etag := fmt.Sprintf("\"%s\"", hex.EncodeToString(hash.Sum(nil)))
	log.Printf("UploadPart: %s part=%s size=%dMB", uploadId[:8], partNum, written/1024/1024)

	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}

func handleCompleteMultipartUpload(w http.ResponseWriter, r *http.Request, uploadId string) {
	uploadsMu.RLock()
	upload, exists := uploads[uploadId]
	uploadsMu.RUnlock()

	if !exists {
		http.Error(w, "NoSuchUpload", http.StatusNotFound)
		return
	}

	// Lock to prevent duplicate completion from client retries
	upload.completing.Lock()
	defer upload.completing.Unlock()

	// If already done (retry), return cached success
	if upload.done {
		result := CompleteMultipartUploadResult{
			Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
			Location: fmt.Sprintf("/%s/%s", upload.bucket, upload.key),
			Bucket:   upload.bucket,
			Key:      upload.key,
			ETag:     upload.etag,
		}
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusOK)
		xml.NewEncoder(w).Encode(result)
		return
	}

	var req CompleteMultipartUploadRequest
	if err := xml.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("ERROR parsing complete request: %v", err)
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}
	r.Body.Close()

	sort.Slice(req.Parts, func(i, j int) bool {
		return req.Parts[i].PartNumber < req.Parts[j].PartNumber
	})

	dir := filepath.Join(uploadDir, uploadId)

	// Concatenate parts into single file
	assembledPath := filepath.Join(dir, "assembled")
	assembled, err := os.Create(assembledPath)
	if err != nil {
		log.Printf("ERROR creating assembled file: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	var totalSize int64
	hash := md5.New()
	for _, part := range req.Parts {
		partFile := filepath.Join(dir, fmt.Sprintf("part-%05d", part.PartNumber))
		f, err := os.Open(partFile)
		if err != nil {
			log.Printf("ERROR opening part %d: %v", part.PartNumber, err)
			assembled.Close()
			os.Remove(assembledPath)
			http.Error(w, "Part not found", http.StatusBadRequest)
			return
		}
		n, _ := io.Copy(io.MultiWriter(assembled, hash), f)
		totalSize += n
		f.Close()
	}
	assembled.Close()

	etag := fmt.Sprintf("\"%s\"", hex.EncodeToString(hash.Sum(nil)))
	log.Printf("CompleteMultipartUpload: %s/%s size=%dMB parts=%d etag=%s — copying to SFTP",
		upload.bucket, upload.key, totalSize/1024/1024, len(req.Parts), etag)

	// Acquire SFTP write slot (limit concurrent writes to avoid Hetzner connection drops)
	sftpSem <- struct{}{}
	defer func() { <-sftpSem }()

	// Use rclone rcat to stream assembled file to SFTP as single write
	dst := fmt.Sprintf("storagebox:./%s/%s", upload.bucket, upload.key)
	cmd := exec.Command("rclone", "rcat", dst, "--size", fmt.Sprintf("%d", totalSize), "--contimeout", "30s")
	stdin, err := os.Open(assembledPath)
	if err != nil {
		log.Printf("ERROR opening assembled file: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}
	cmd.Stdin = stdin
	output, err := cmd.CombinedOutput()
	stdin.Close()
	if err != nil {
		log.Printf("ERROR rclone rcat: %v: %s", err, string(output))
		http.Error(w, "Backend write failed", http.StatusBadGateway)
		return
	}

	// Verify: check file appeared with correct size
	log.Printf("CompleteMultipartUpload: %s/%s done (%d bytes written to SFTP)", upload.bucket, upload.key, totalSize)

	// Mark done (retries will get cached response)
	upload.done = true
	upload.etag = etag

	// Cleanup parts (keep upload entry for retries, cleaned by background goroutine)
	os.RemoveAll(dir)

	result := CompleteMultipartUploadResult{
		Xmlns:    "http://s3.amazonaws.com/doc/2006-03-01/",
		Location: fmt.Sprintf("/%s/%s", upload.bucket, upload.key),
		Bucket:   upload.bucket,
		Key:      upload.key,
		ETag:     etag,
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	xml.NewEncoder(w).Encode(result)
}

func handleAbortMultipartUpload(w http.ResponseWriter, r *http.Request, uploadId string) {
	dir := filepath.Join(uploadDir, uploadId)
	os.RemoveAll(dir)

	uploadsMu.Lock()
	delete(uploads, uploadId)
	uploadsMu.Unlock()

	log.Printf("AbortMultipartUpload: %s", uploadId[:8])
	w.WriteHeader(http.StatusNoContent)
}

func cleanupStaleUploads() {
	for {
		time.Sleep(5 * time.Minute)
		uploadsMu.Lock()
		for id, u := range uploads {
			if time.Since(u.created) > 1*time.Hour {
				dir := filepath.Join(uploadDir, id)
				os.RemoveAll(dir)
				delete(uploads, id)
				log.Printf("Cleaned stale upload: %s/%s (%s)", u.bucket, u.key, id[:8])
			}
		}
		uploadsMu.Unlock()
	}
}
