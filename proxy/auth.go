package proxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"
	"sort"
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

// AuthenticateS3Request extracts access key from AWS signature, verifies signature, and resolves tenant
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

	// Verify AWS SigV4 signature
	if strings.HasPrefix(auth, "AWS4-HMAC-SHA256") {
		if !verifyAWSSigV4(r, auth, apiKey.SecretKey) {
			log.Printf("SigV4 verification failed for access key %s", accessKey)
			return nil, fmt.Errorf("signature mismatch")
		}
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

// verifyAWSSigV4 verifies AWS Signature Version 4
func verifyAWSSigV4(r *http.Request, auth, secretKey string) bool {
	// Parse: AWS4-HMAC-SHA256 Credential=key/date/region/s3/aws4_request, SignedHeaders=..., Signature=...
	credScope, signedHeadersStr, providedSig := parseSigV4Auth(auth)
	if credScope == "" || signedHeadersStr == "" || providedSig == "" {
		log.Printf("SigV4: failed to parse auth header")
		return false
	}

	// credScope = accessKey/20260101/us-east-1/s3/aws4_request
	scopeParts := strings.SplitN(credScope, "/", 2)
	if len(scopeParts) < 2 {
		return false
	}
	scope := scopeParts[1] // date/region/s3/aws4_request
	dateParts := strings.Split(scope, "/")
	if len(dateParts) < 4 {
		return false
	}
	dateStamp := dateParts[0]
	region := dateParts[1]
	service := dateParts[2]

	// Build canonical request
	signedHeaders := strings.Split(signedHeadersStr, ";")
	sort.Strings(signedHeaders)

	var canonicalHeaders strings.Builder
	for _, h := range signedHeaders {
		h = strings.TrimSpace(h)
		var val string
		if h == "host" {
			val = r.Host
		} else {
			val = r.Header.Get(h)
		}
		canonicalHeaders.WriteString(h + ":" + strings.TrimSpace(val) + "\n")
	}

	// Payload hash — use x-amz-content-sha256 header if present
	payloadHash := r.Header.Get("X-Amz-Content-Sha256")
	if payloadHash == "" {
		payloadHash = "UNSIGNED-PAYLOAD"
	}

	canonicalURI := r.URL.Path
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	// Canonical query string
	canonicalQueryString := buildCanonicalQueryString(r)

	canonicalRequest := strings.Join([]string{
		r.Method,
		canonicalURI,
		canonicalQueryString,
		canonicalHeaders.String(),
		signedHeadersStr,
		payloadHash,
	}, "\n")

	// String to sign
	amzDate := r.Header.Get("X-Amz-Date")
	if amzDate == "" {
		// Try Date header
		amzDate = dateStamp + "T000000Z"
	}

	canonicalRequestHash := sha256Hex([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		scope,
		canonicalRequestHash,
	}, "\n")

	// Derive signing key
	signingKey := deriveSigningKey(secretKey, dateStamp, region, service)

	// Calculate signature
	calculatedSig := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	if calculatedSig != providedSig {
		log.Printf("SigV4: signature mismatch (calculated=%s...  provided=%s...)", calculatedSig[:16], providedSig[:min(16, len(providedSig))])
		return false
	}

	return true
}

func parseSigV4Auth(auth string) (credential, signedHeaders, signature string) {
	// AWS4-HMAC-SHA256 Credential=..., SignedHeaders=..., Signature=...
	auth = strings.TrimPrefix(auth, "AWS4-HMAC-SHA256")
	auth = strings.TrimSpace(auth)

	for _, part := range strings.Split(auth, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "Credential=") {
			credential = strings.TrimPrefix(part, "Credential=")
		} else if strings.HasPrefix(part, "SignedHeaders=") {
			signedHeaders = strings.TrimPrefix(part, "SignedHeaders=")
		} else if strings.HasPrefix(part, "Signature=") {
			signature = strings.TrimPrefix(part, "Signature=")
		}
	}
	return
}

func buildCanonicalQueryString(r *http.Request) string {
	query := r.URL.Query()
	if len(query) == 0 {
		return ""
	}

	var keys []string
	for k := range query {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var parts []string
	for _, k := range keys {
		values := query[k]
		sort.Strings(values)
		for _, v := range values {
			parts = append(parts, uriEncode(k)+"="+uriEncode(v))
		}
	}
	return strings.Join(parts, "&")
}

func uriEncode(s string) string {
	var buf strings.Builder
	for _, b := range []byte(s) {
		if (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '-' || b == '_' || b == '.' || b == '~' {
			buf.WriteByte(b)
		} else {
			fmt.Fprintf(&buf, "%%%02X", b)
		}
	}
	return buf.String()
}

func deriveSigningKey(secretKey, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(dateStamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
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

	// Rewrite x-amz-copy-source header for CopyObject
	if copySource := r.Header.Get("X-Amz-Copy-Source"); copySource != "" {
		cs := strings.TrimPrefix(copySource, "/")
		csParts := strings.SplitN(cs, "/", 2)
		if len(csParts) >= 1 && csParts[0] != "" {
			newSource := "/" + userID + "--" + csParts[0]
			if len(csParts) == 2 {
				newSource += "/" + csParts[1]
			}
			r.Header.Set("X-Amz-Copy-Source", newSource)
		}
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
