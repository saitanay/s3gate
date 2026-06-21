package web

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"s3gate/db"
)

var templates map[string]*template.Template
var staticDir string

func InitTemplates(dir string) {
	staticDir = filepath.Join(filepath.Dir(dir), "static")
	funcMap := template.FuncMap{
		"formatBytes": formatBytes,
		"formatPaise": formatPaise,
		"timeAgo":     timeAgo,
		"divf": func(a, b int64) float64 {
			if b == 0 {
				return 0
			}
			return float64(a) / float64(b)
		},
	}

	layoutFile := filepath.Join(dir, "layout.html")
	templates = make(map[string]*template.Template)

	pages := []string{
		"home.html", "login.html", "contact.html",
		"terms.html", "refunds.html", "404.html",
		"dashboard.html", "buckets.html", "keys.html",
		"billing.html", "settings.html", "checkout.html",
		"admin_login.html", "admin_dashboard.html",
		"admin_users.html", "admin_user_edit.html",
	}

	for _, page := range pages {
		templates[page] = template.Must(
			template.New("").Funcs(funcMap).ParseFiles(layoutFile, filepath.Join(dir, page)),
		)
	}
}

func RegisterRoutes(mux *http.ServeMux) {
	// Public
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(staticDir))))
	mux.HandleFunc("/", handleHome)
	mux.HandleFunc("/login", handleLogin)
	mux.HandleFunc("/auth/verify", handleVerify)
	mux.HandleFunc("/logout", handleLogout)
	mux.HandleFunc("/contact", handleContact)
	mux.HandleFunc("/terms", handleTerms)
	mux.HandleFunc("/refunds", handleRefunds)

	// Dashboard (auth required)
	mux.HandleFunc("/dashboard", AuthMiddleware(handleDashboard))
	mux.HandleFunc("/dashboard/buckets", AuthMiddleware(handleBuckets))
	mux.HandleFunc("/dashboard/buckets/create", AuthMiddleware(handleCreateBucket))
	mux.HandleFunc("/dashboard/keys", AuthMiddleware(handleKeys))
	mux.HandleFunc("/dashboard/keys/create", AuthMiddleware(handleCreateKey))
	mux.HandleFunc("/dashboard/keys/revoke", AuthMiddleware(handleRevokeKey))
	mux.HandleFunc("/dashboard/billing", AuthMiddleware(handleBilling))
	mux.HandleFunc("/dashboard/billing/recharge", AuthMiddleware(HandleRecharge))
	mux.HandleFunc("/dashboard/billing/callback", HandleBillingCallback)
	mux.HandleFunc("/dashboard/settings", AuthMiddleware(handleSettings))

	// Webhooks
	mux.HandleFunc("/webhooks/cashfree", HandleCashfreeWebhook)

	// Admin
	RegisterAdminRoutes(mux)
}

// --- Public Handlers ---

func handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		w.WriteHeader(http.StatusNotFound)
		render(w, "404.html", nil)
		return
	}
	render(w, "home.html", nil)
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	// Redirect to dashboard if already logged in
	if user := GetCurrentUser(r); user != nil {
		http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
		return
	}

	if r.Method == "GET" {
		render(w, "login.html", nil)
		return
	}

	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	if email == "" || !strings.Contains(email, "@") {
		render(w, "login.html", map[string]any{"Error": "Please enter a valid email"})
		return
	}

	token, err := db.CreateAuthToken(email)
	if err != nil {
		log.Printf("ERROR creating auth token: %v", err)
		render(w, "login.html", map[string]any{"Error": "Something went wrong. Please try again."})
		return
	}

	go func() {
		err := SendMagicLink(email, token)
		if err != nil {
			log.Printf("ERROR sending magic link to %s: %v", email, err)
		}
	}()

	render(w, "login.html", map[string]any{"Sent": true, "Email": email})
}

func handleVerify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	email, err := db.VerifyAuthToken(token)
	if err != nil {
		render(w, "login.html", map[string]any{"Error": "Invalid or expired link. Please request a new one."})
		return
	}

	// Get or create user
	user, err := db.GetUserByEmail(email)
	if err != nil {
		log.Printf("ERROR getting user: %v", err)
		http.Error(w, "Internal error", 500)
		return
	}
	if user == nil {
		user, err = db.CreateUser(email)
		if err != nil {
			log.Printf("ERROR creating user: %v", err)
			http.Error(w, "Internal error", 500)
			return
		}
	}

	// Create session
	sessionToken, err := db.CreateSession(user.ID)
	if err != nil {
		log.Printf("ERROR creating session: %v", err)
		http.Error(w, "Internal error", 500)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sessionToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   10 * 365 * 24 * 60 * 60, // ~10 years (permanent until logout)
	})

	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		db.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: "session", MaxAge: -1, Path: "/"})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func handleContact(w http.ResponseWriter, r *http.Request) {
	render(w, "contact.html", nil)
}

func handleTerms(w http.ResponseWriter, r *http.Request) {
	render(w, "terms.html", nil)
}

func handleRefunds(w http.ResponseWriter, r *http.Request) {
	render(w, "refunds.html", nil)
}

// --- Dashboard Handlers ---

func handleDashboard(w http.ResponseWriter, r *http.Request) {
	user := GetCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	bytesUsed, _ := db.GetStorageUsed(user.ID)
	balance, _ := db.GetBalance(user.ID)

	data := map[string]any{
		"User":      user,
		"BytesUsed": bytesUsed,
		"BytesMax":  int64(1024 * 1024 * 1024 * 1024), // 1TB
		"Balance":   balance,
		"UsedPct":   float64(bytesUsed) / float64(1024*1024*1024*1024) * 100,
	}
	render(w, "dashboard.html", data)
}

func handleBuckets(w http.ResponseWriter, r *http.Request) {
	user := GetCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	buckets, _ := db.GetUserBuckets(user.ID)
	render(w, "buckets.html", map[string]any{"User": user, "Buckets": buckets})
}

func handleCreateBucket(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/dashboard/buckets", http.StatusSeeOther)
		return
	}

	user := GetCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	name := strings.TrimSpace(strings.ToLower(r.FormValue("name")))

	// Validate bucket name
	if len(name) < 3 || len(name) > 63 {
		buckets, _ := db.GetUserBuckets(user.ID)
		render(w, "buckets.html", map[string]any{"User": user, "Buckets": buckets, "Error": "Bucket name must be 3-63 characters"})
		return
	}
	if !isValidBucketName(name) {
		buckets, _ := db.GetUserBuckets(user.ID)
		render(w, "buckets.html", map[string]any{"User": user, "Buckets": buckets, "Error": "Bucket name can only contain lowercase letters, numbers, and hyphens"})
		return
	}

	internalName := user.ID + "--" + name

	// Check if already exists in DB
	if db.BucketExists(internalName) {
		buckets, _ := db.GetUserBuckets(user.ID)
		render(w, "buckets.html", map[string]any{"User": user, "Buckets": buckets, "Error": "Bucket '" + name + "' already exists"})
		return
	}

	// Record in DB
	if err := db.CreateBucket(user.ID, name, internalName); err != nil {
		buckets, _ := db.GetUserBuckets(user.ID)
		render(w, "buckets.html", map[string]any{"User": user, "Buckets": buckets, "Error": "Failed to create bucket"})
		return
	}

	// Create on SFTP in background (rclone mkdir)
	go func() {
		cmd := exec.Command("rclone", "mkdir", "storagebox:./"+internalName)
		if err := cmd.Run(); err != nil {
			log.Printf("ERROR creating bucket on SFTP %s: %v", internalName, err)
		}
	}()

	log.Printf("Bucket created: %s (user=%s)", name, user.ID)
	http.Redirect(w, r, "/dashboard/buckets", http.StatusSeeOther)
}

func isValidBucketName(name string) bool {
	for _, c := range name {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	if name[0] == '-' || name[len(name)-1] == '-' {
		return false
	}
	return true
}

func handleKeys(w http.ResponseWriter, r *http.Request) {
	user := GetCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	keys, _ := db.GetAPIKeys(user.ID)
	render(w, "keys.html", map[string]any{"User": user, "Keys": keys})
}

func handleCreateKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/dashboard/keys", http.StatusSeeOther)
		return
	}
	user := GetCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	label := r.FormValue("label")
	key, err := db.CreateAPIKey(user.ID, label)
	if err != nil {
		log.Printf("ERROR creating API key: %v", err)
		http.Redirect(w, r, "/dashboard/keys", http.StatusSeeOther)
		return
	}

	keys, _ := db.GetAPIKeys(user.ID)
	render(w, "keys.html", map[string]any{"User": user, "Keys": keys, "NewKey": key})
}

func handleRevokeKey(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/dashboard/keys", http.StatusSeeOther)
		return
	}
	user := GetCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	keyID := r.FormValue("key_id")
	db.RevokeAPIKey(keyID, user.ID)
	http.Redirect(w, r, "/dashboard/keys", http.StatusSeeOther)
}

func handleBilling(w http.ResponseWriter, r *http.Request) {
	user := GetCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	balance, _ := db.GetBalance(user.ID)
	txns, _ := db.GetTransactions(user.ID, 20)

	render(w, "billing.html", map[string]any{
		"User":         user,
		"Balance":      balance,
		"Transactions": txns,
	})
}

func handleSettings(w http.ResponseWriter, r *http.Request) {
	user := GetCurrentUser(r)
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	render(w, "settings.html", map[string]any{"User": user})
}

// --- Helpers ---

func render(w http.ResponseWriter, name string, data any) {
	tmpl, ok := templates[name]
	if !ok {
		log.Printf("ERROR template %s not found", name)
		http.Error(w, "Internal error", 500)
		return
	}
	err := tmpl.ExecuteTemplate(w, "layout", data)
	if err != nil {
		log.Printf("ERROR rendering %s: %v", name, err)
		http.Error(w, "Internal error", 500)
	}
}

func formatBytes(b int64) string {
	if b < 1024 {
		return fmt.Sprintf("%d B", b)
	} else if b < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	} else if b < 1024*1024*1024 {
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	} else if b < 1024*1024*1024*1024 {
		return fmt.Sprintf("%.1f GB", float64(b)/(1024*1024*1024))
	}
	return fmt.Sprintf("%.2f TB", float64(b)/(1024*1024*1024*1024))
}

func formatPaise(p int64) string {
	return fmt.Sprintf("₹%d.%02d", p/100, p%100)
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
