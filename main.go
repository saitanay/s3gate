package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"s3gate/db"
	"s3gate/jobs"
	"s3gate/proxy"
	"s3gate/web"
)

func main() {
	// Init database
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/db/bucketcheap.db"
	}
	db.Init(dbPath)

	// Init templates
	templateDir := os.Getenv("TEMPLATE_DIR")
	if templateDir == "" {
		templateDir = "/app/web/templates"
	}
	web.InitTemplates(templateDir)

	// Start background jobs
	jobs.StartScheduler()

	// Web routes
	webMux := http.NewServeMux()
	web.RegisterRoutes(webMux)

	// S3 proxy
	s3Handler := proxy.NewS3Handler()

	// Single server, route by Host header or AWS auth
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.Split(r.Host, ":")[0] // strip port

		// Route to S3 if host starts with s3. OR request has AWS auth signature
		if strings.HasPrefix(host, "s3.") || isS3Request(r) {
			s3Handler.ServeHTTP(w, r)
		} else {
			webMux.ServeHTTP(w, r)
		}
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "9000"
	}

	log.Printf("BucketCheap starting on :%s", port)
	log.Printf("  Web UI: http://localhost:%s", port)
	log.Printf("  S3 API: http://s3.localhost:%s", port)
	log.Fatal(http.ListenAndServe(":"+port, handler))
}

func isS3Request(r *http.Request) bool {
	// AWS SDK sends Authorization header with AWS4-HMAC-SHA256
	auth := r.Header.Get("Authorization")
	return strings.HasPrefix(auth, "AWS4-HMAC-SHA256") || strings.HasPrefix(auth, "AWS ")
}
