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
	var totalUsers, activeUsers, trialUsers, suspendedUsers, expiredUsers int64
	db.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&totalUsers)
	db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE status='active'`).Scan(&activeUsers)
	db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE status='trial'`).Scan(&trialUsers)
	db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE status='suspended'`).Scan(&suspendedUsers)
	db.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE status='expired'`).Scan(&expiredUsers)

	var totalRevenue, totalDeductions int64
	db.DB.QueryRow(`SELECT COALESCE(SUM(amount_paise),0) FROM transactions WHERE type='recharge'`).Scan(&totalRevenue)
	db.DB.QueryRow(`SELECT COALESCE(SUM(amount_paise),0) FROM transactions WHERE type='monthly_deduction'`).Scan(&totalDeductions)

	var totalBuckets int64
	db.DB.QueryRow(`SELECT COUNT(*) FROM buckets`).Scan(&totalBuckets)

	var totalStorage int64
	db.DB.QueryRow(`SELECT COALESCE(SUM(ud.bytes_stored),0) FROM usage_daily ud INNER JOIN (SELECT user_id, MAX(date) as max_date FROM usage_daily GROUP BY user_id) latest ON ud.user_id = latest.user_id AND ud.date = latest.max_date`).Scan(&totalStorage)

	// Recent transactions (platform-wide)
	type AdminTxn struct {
		Email       string
		Type        string
		AmountPaise int64
		Description string
		CreatedAt   time.Time
	}
	var recentTxns []AdminTxn
	txnRows, err := db.DB.Query(`SELECT u.email, t.type, t.amount_paise, t.description, t.created_at FROM transactions t JOIN users u ON t.user_id = u.id ORDER BY t.created_at DESC LIMIT 15`)
	if err == nil {
		defer txnRows.Close()
		for txnRows.Next() {
			var t AdminTxn
			txnRows.Scan(&t.Email, &t.Type, &t.AmountPaise, &t.Description, &t.CreatedAt)
			recentTxns = append(recentTxns, t)
		}
	}

	// Recent signups
	var recentUsers []db.User
	userRows, err := db.DB.Query(`SELECT id, email, status, trial_starts_at, trial_expires_at, data_deletion_at, created_at FROM users ORDER BY created_at DESC LIMIT 10`)
	if err == nil {
		defer userRows.Close()
		for userRows.Next() {
			var u db.User
			userRows.Scan(&u.ID, &u.Email, &u.Status, &u.TrialStartsAt, &u.TrialExpiresAt, &u.DataDeletionAt, &u.CreatedAt)
			recentUsers = append(recentUsers, u)
		}
	}

	// Paying users (anyone with a recharge transaction)
	type PayingUser struct {
		Email       string
		Status      string
		TotalPaid   int64
		Balance     int64
		LastPayment time.Time
	}
	var payingUsers []PayingUser
	payRows, err := db.DB.Query(`SELECT u.email, u.status, SUM(t.amount_paise) as total_paid, COALESCE(w.balance_paise,0), MAX(t.created_at) FROM transactions t JOIN users u ON t.user_id = u.id LEFT JOIN wallet w ON u.id = w.user_id WHERE t.type='recharge' GROUP BY t.user_id ORDER BY total_paid DESC`)
	if err == nil {
		defer payRows.Close()
		for payRows.Next() {
			var p PayingUser
			payRows.Scan(&p.Email, &p.Status, &p.TotalPaid, &p.Balance, &p.LastPayment)
			payingUsers = append(payingUsers, p)
		}
	}

	render(w, "admin_dashboard.html", map[string]any{
		"TotalUsers":      totalUsers,
		"ActiveUsers":     activeUsers,
		"TrialUsers":      trialUsers,
		"SuspendedUsers":  suspendedUsers,
		"ExpiredUsers":    expiredUsers,
		"TotalRevenue":    totalRevenue,
		"TotalDeductions": totalDeductions,
		"TotalBuckets":    totalBuckets,
		"TotalStorage":    totalStorage,
		"RecentTxns":      recentTxns,
		"RecentUsers":     recentUsers,
		"PayingUsers":     payingUsers,
	})
}

// AdminUserRow carries enriched per-user data for the users list
type AdminUserRow struct {
	db.User
	Balance     int64
	BytesUsed   int64
	BucketCount int64
}

func handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	statusFilter := r.URL.Query().Get("status")

	query := `SELECT u.id, u.email, u.status, u.trial_starts_at, u.trial_expires_at, u.data_deletion_at, u.created_at,
		COALESCE(w.balance_paise, 0),
		COALESCE((SELECT bytes_stored FROM usage_daily WHERE user_id = u.id ORDER BY date DESC LIMIT 1), 0),
		(SELECT COUNT(*) FROM buckets WHERE user_id = u.id)
		FROM users u LEFT JOIN wallet w ON u.id = w.user_id`
	var args []any
	if statusFilter != "" {
		query += ` WHERE u.status = ?`
		args = append(args, statusFilter)
	}
	query += ` ORDER BY u.created_at DESC`

	rows, err := db.DB.Query(query, args...)
	if err != nil {
		log.Printf("ERROR admin users query: %v", err)
		http.Error(w, "Internal error", 500)
		return
	}
	defer rows.Close()

	var users []AdminUserRow
	for rows.Next() {
		var u AdminUserRow
		rows.Scan(&u.ID, &u.Email, &u.Status, &u.TrialStartsAt, &u.TrialExpiresAt, &u.DataDeletionAt, &u.CreatedAt,
			&u.Balance, &u.BytesUsed, &u.BucketCount)
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
	txns, _ := db.GetTransactions(userID, 20)
	buckets, _ := db.GetUserBuckets(userID)

	render(w, "admin_user_edit.html", map[string]any{
		"User":         user,
		"Balance":      balance,
		"BytesUsed":    bytesUsed,
		"Keys":         keys,
		"Transactions": txns,
		"Buckets":      buckets,
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
