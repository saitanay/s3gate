package web

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"s3gate/db"
)

func RegisterAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/admin/login", handleAdminLogin)
	mux.HandleFunc("/admin", adminAuth(handleAdminDashboard))
	mux.HandleFunc("/admin/users", adminAuth(handleAdminUsers))
	mux.HandleFunc("/admin/users/edit", adminAuth(handleAdminUserEdit))
	mux.HandleFunc("/admin/users/action", adminAuth(handleAdminUserAction))
}

func adminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("admin_session")
		if err != nil || cookie.Value != "authenticated" {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

func handleAdminLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		render(w, "admin_login.html", nil)
		return
	}

	email := r.FormValue("email")
	password := r.FormValue("password")

	if email == os.Getenv("ADMIN_EMAIL") && password == os.Getenv("ADMIN_PASSWORD") {
		http.SetCookie(w, &http.Cookie{
			Name:     "admin_session",
			Value:    "authenticated",
			Path:     "/admin",
			HttpOnly: true,
			Secure:   true,
			MaxAge:   24 * 60 * 60, // 1 day
		})
		http.Redirect(w, r, "/admin", http.StatusSeeOther)
		return
	}

	render(w, "admin_login.html", map[string]any{"Error": "Invalid credentials"})
}

func handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	var totalUsers, activeUsers, trialUsers, suspendedUsers int64
	db.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&totalUsers)
	db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE status='active'`).Scan(&activeUsers)
	db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE status='trial'`).Scan(&trialUsers)
	db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE status='suspended'`).Scan(&suspendedUsers)

	var totalRevenue int64
	db.DB.QueryRow(`SELECT COALESCE(SUM(amount_paise),0) FROM transactions WHERE type='recharge'`).Scan(&totalRevenue)

	render(w, "admin_dashboard.html", map[string]any{
		"TotalUsers":     totalUsers,
		"ActiveUsers":    activeUsers,
		"TrialUsers":     trialUsers,
		"SuspendedUsers": suspendedUsers,
		"TotalRevenue":   totalRevenue,
	})
}

func handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")

	query := `SELECT id, email, status, trial_starts_at, trial_expires_at, data_deletion_at, created_at FROM users`
	var args []any
	if statusFilter != "" {
		query += ` WHERE status = ?`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY created_at DESC`

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		log.Printf("ERROR admin users query: %v", err)
		http.Error(w, "Internal error", 500)
		return
	}
	defer rows.Close()

	var users []db.User
	for rows.Next() {
		var u db.User
		rows.Scan(&u.ID, &u.Email, &u.Status, &u.TrialStartsAt, &u.TrialExpiresAt, &u.DataDeletionAt, &u.CreatedAt)
		users = append(users, u)
	}

	render(w, "admin_users.html", map[string]any{
		"Users":  users,
		"Filter": statusFilter,
	})
}

func handleAdminUserEdit(w http.ResponseWriter, r *http.Request) {
	userID := r.URL.Query().Get("id")
	if userID == "" {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	user, err := db.GetUserByID(userID)
	if err != nil || user == nil {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	balance, _ := db.GetBalance(userID)
	bytesUsed, _ := db.GetStorageUsed(userID)
	keys, _ := db.GetAPIKeys(userID)
	txns, _ := db.GetTransactions(userID, 10)

	render(w, "admin_user_edit.html", map[string]any{
		"User":         user,
		"Balance":      balance,
		"BytesUsed":    bytesUsed,
		"Keys":         keys,
		"Transactions": txns,
	})
}

func handleAdminUserAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	userID := r.FormValue("user_id")
	action := r.FormValue("action")

	switch action {
	case "set_status":
		status := r.FormValue("status")
		db.DB.Exec(`UPDATE users SET status = ? WHERE id = ?`, status, userID)

	case "extend_trial":
		days, _ := strconv.Atoi(r.FormValue("days"))
		newExpiry := time.Now().Add(time.Duration(days) * 24 * time.Hour)
		db.DB.Exec(`UPDATE users SET trial_expires_at = ?, status = 'trial' WHERE id = ?`, newExpiry, userID)

	case "credit_wallet":
		amount, _ := strconv.ParseInt(r.FormValue("amount_paise"), 10, 64)
		if amount > 0 {
			db.CreditWallet(userID, amount, fmt.Sprintf("Admin credit: ₹%d.%02d", amount/100, amount%100), "admin")
		}

	case "delete_user":
		// Mark for deletion
		db.DB.Exec(`UPDATE users SET status = 'suspended', data_deletion_at = ? WHERE id = ?`, time.Now(), userID)
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/admin/users/edit?id=%s", userID), http.StatusSeeOther)
}
