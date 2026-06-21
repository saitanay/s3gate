package proxy

import (
	"log"
	"net/http"
	"strings"
	"time"

	"s3gate/db"
)

const maxStorageBytes = 1024 * 1024 * 1024 * 1024 // 1TB

// TenantContext holds the resolved tenant info for a request
type TenantContext struct {
	UserID    string
	AccessKey string
	Status    string
}

// AuthenticateS3Request extracts access key from AWS signature and resolves tenant
func AuthenticateS3Request(r *http.Request) (*TenantContext, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return nil, nil // anonymous
	}

	// Parse AWS4-HMAC-SHA256 Credential=ACCESS_KEY/...
	accessKey := extractAccessKey(auth)
	if accessKey == "" {
		return nil, nil
	}

	// Lookup in database
	apiKey, err := db.LookupAPIKey(accessKey)
	if err != nil || apiKey == nil {
		return nil, nil
	}

	// Get user
	user, err := db.GetUserByID(apiKey.UserID)
	if err != nil || user == nil {
		return nil, nil
	}

	// Check trial expiry
	if user.Status == "trial" && user.TrialExpiresAt != nil && time.Now().After(*user.TrialExpiresAt) {
		// Auto-expire trial
		db.DB.Exec(`UPDATE users SET status = 'expired' WHERE id = ?`, user.ID)
		user.Status = "expired"
	}

	return &TenantContext{
		UserID:    user.ID,
		AccessKey: accessKey,
		Status:    user.Status,
	}, nil
}

// CheckWriteAllowed verifies the tenant can write (status + quota)
func CheckWriteAllowed(tenant *TenantContext) (allowed bool, reason string) {
	if tenant == nil {
		return false, "AccessDenied"
	}

	switch tenant.Status {
	case "active", "trial":
		// OK
	case "expired":
		return false, "AccountExpired"
	case "suspended":
		return false, "AccountSuspended"
	default:
		return false, "AccessDenied"
	}

	// Check quota
	bytesUsed, _ := db.GetStorageUsed(tenant.UserID)
	if bytesUsed >= maxStorageBytes {
		return false, "QuotaExceeded"
	}

	return true, ""
}

// RewritePathForTenant prefixes the bucket name with user ID
// Client: /my-bucket/key → SFTP: /userID__my-bucket/key
func RewritePathForTenant(r *http.Request, userID string) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)

	if len(parts) == 0 || parts[0] == "" {
		// ListBuckets — no rewrite needed, but we'll filter later
		return
	}

	// Prefix bucket name
	newBucket := userID + "--" + parts[0]
	if len(parts) == 2 {
		r.URL.Path = "/" + newBucket + "/" + parts[1]
	} else {
		r.URL.Path = "/" + newBucket
	}
	if r.URL.RawPath != "" {
		r.URL.RawPath = r.URL.Path
	}
}

// extractAccessKey parses the access key from AWS Authorization header
func extractAccessKey(auth string) string {
	// AWS4-HMAC-SHA256 Credential=ACCESSKEY/20260621/us-east-1/s3/aws4_request, ...
	if strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		idx := strings.Index(auth, "Credential=")
		if idx < 0 {
			return ""
		}
		cred := auth[idx+len("Credential="):]
		slash := strings.Index(cred, "/")
		if slash < 0 {
			return ""
		}
		return cred[:slash]
	}

	// AWS ACCESSKEY:signature (v2)
	if strings.HasPrefix(auth, "AWS ") {
		parts := strings.SplitN(auth[4:], ":", 2)
		if len(parts) == 2 {
			return parts[0]
		}
	}

	return ""
}

// S3ErrorResponse returns an XML S3 error
func S3ErrorResponse(w http.ResponseWriter, code, message string, httpStatus int) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(httpStatus)
	w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<Error><Code>` + code + `</Code><Message>` + message + `</Message></Error>`))
	log.Printf("S3 error: %s — %s", code, message)
}
