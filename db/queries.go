package db

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// User
type User struct {
	ID             string
	Email          string
	Status         string
	TrialStartsAt  *time.Time
	TrialExpiresAt *time.Time
	DataDeletionAt *time.Time
	CreatedAt      time.Time
}

type APIKey struct {
	ID        string
	UserID    string
	AccessKey string
	SecretKey string
	Label     string
	RevokedAt *time.Time
	CreatedAt time.Time
}

type Transaction struct {
	ID          string
	UserID      string
	Type        string
	AmountPaise int64
	Description string
	DodopayRef  string
	CreatedAt   time.Time
}

// --- Users ---

func CreateUser(email string) (*User, error) {
	id := uuid.New().String()
	now := time.Now()
	trialExpires := now.Add(7 * 24 * time.Hour)

	_, err := DB.Exec(`INSERT INTO users (id, email, status, trial_starts_at, trial_expires_at) VALUES (?, ?, 'trial', ?, ?)`,
		id, email, now, trialExpires)
	if err != nil {
		return nil, err
	}

	_, err = DB.Exec(`INSERT INTO wallet (user_id, balance_paise) VALUES (?, 0)`, id)
	if err != nil {
		return nil, err
	}

	return &User{ID: id, Email: email, Status: "trial", TrialStartsAt: &now, TrialExpiresAt: &trialExpires, CreatedAt: now}, nil
}

func GetUserByEmail(email string) (*User, error) {
	u := &User{}
	err := DB.QueryRow(`SELECT id, email, status, trial_starts_at, trial_expires_at, data_deletion_at, created_at FROM users WHERE email = ?`, email).
		Scan(&u.ID, &u.Email, &u.Status, &u.TrialStartsAt, &u.TrialExpiresAt, &u.DataDeletionAt, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

func GetUserByID(id string) (*User, error) {
	u := &User{}
	err := DB.QueryRow(`SELECT id, email, status, trial_starts_at, trial_expires_at, data_deletion_at, created_at FROM users WHERE id = ?`, id).
		Scan(&u.ID, &u.Email, &u.Status, &u.TrialStartsAt, &u.TrialExpiresAt, &u.DataDeletionAt, &u.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// --- Auth Tokens ---

func CreateAuthToken(email string) (string, error) {
	id := uuid.New().String()
	token := generateToken(32)
	expires := time.Now().Add(15 * time.Minute)

	_, err := DB.Exec(`INSERT INTO auth_tokens (id, email, token, expires_at) VALUES (?, ?, ?, ?)`,
		id, email, token, expires)
	return token, err
}

func VerifyAuthToken(token string) (string, error) {
	var id, email string
	var expiresAt time.Time
	var usedAt *time.Time

	err := DB.QueryRow(`SELECT id, email, expires_at, used_at FROM auth_tokens WHERE token = ?`, token).
		Scan(&id, &email, &expiresAt, &usedAt)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("invalid token")
	}
	if err != nil {
		return "", err
	}
	if usedAt != nil {
		return "", fmt.Errorf("token already used")
	}
	if time.Now().After(expiresAt) {
		return "", fmt.Errorf("token expired")
	}

	_, err = DB.Exec(`UPDATE auth_tokens SET used_at = ? WHERE id = ?`, time.Now(), id)
	return email, err
}

// --- Sessions ---

func CreateSession(userID string) (string, error) {
	id := uuid.New().String()
	token := generateToken(32)
	expires := time.Now().Add(30 * 24 * time.Hour) // 30 days

	_, err := DB.Exec(`INSERT INTO sessions (id, user_id, token, expires_at) VALUES (?, ?, ?, ?)`,
		id, userID, token, expires)
	return token, err
}

func GetSessionUser(token string) (*User, error) {
	var userID string
	var expiresAt time.Time

	err := DB.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token = ?`, token).
		Scan(&userID, &expiresAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if time.Now().After(expiresAt) {
		DB.Exec(`DELETE FROM sessions WHERE token = ?`, token)
		return nil, nil
	}

	return GetUserByID(userID)
}

func DeleteSession(token string) {
	DB.Exec(`DELETE FROM sessions WHERE token = ?`, token)
}

// --- API Keys ---

func CreateAPIKey(userID, label string) (*APIKey, error) {
	id := uuid.New().String()
	accessKey := generateToken(10) // 20 hex chars
	secretKey := generateToken(20) // 40 hex chars

	_, err := DB.Exec(`INSERT INTO api_keys (id, user_id, access_key, secret_key, label) VALUES (?, ?, ?, ?, ?)`,
		id, userID, accessKey, secretKey, label)
	if err != nil {
		return nil, err
	}

	return &APIKey{ID: id, UserID: userID, AccessKey: accessKey, SecretKey: secretKey, Label: label, CreatedAt: time.Now()}, nil
}

func GetAPIKeys(userID string) ([]APIKey, error) {
	rows, err := DB.Query(`SELECT id, user_id, access_key, secret_key, label, revoked_at, created_at FROM api_keys WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		rows.Scan(&k.ID, &k.UserID, &k.AccessKey, &k.SecretKey, &k.Label, &k.RevokedAt, &k.CreatedAt)
		keys = append(keys, k)
	}
	return keys, nil
}

func RevokeAPIKey(id, userID string) error {
	_, err := DB.Exec(`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND user_id = ?`, time.Now(), id, userID)
	return err
}

func LookupAPIKey(accessKey string) (*APIKey, error) {
	k := &APIKey{}
	err := DB.QueryRow(`SELECT id, user_id, access_key, secret_key, label FROM api_keys WHERE access_key = ? AND revoked_at IS NULL`, accessKey).
		Scan(&k.ID, &k.UserID, &k.AccessKey, &k.SecretKey, &k.Label)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return k, err
}

// --- Wallet ---

func GetBalance(userID string) (int64, error) {
	var balance int64
	err := DB.QueryRow(`SELECT balance_paise FROM wallet WHERE user_id = ?`, userID).Scan(&balance)
	return balance, err
}

func CreditWallet(userID string, amountPaise int64, description, dodopayRef string) error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}

	_, err = tx.Exec(`UPDATE wallet SET balance_paise = balance_paise + ? WHERE user_id = ?`, amountPaise, userID)
	if err != nil {
		tx.Rollback()
		return err
	}

	id := uuid.New().String()
	_, err = tx.Exec(`INSERT INTO transactions (id, user_id, type, amount_paise, description, dodopay_ref) VALUES (?, ?, 'recharge', ?, ?, ?)`,
		id, userID, amountPaise, description, dodopayRef)
	if err != nil {
		tx.Rollback()
		return err
	}

	// Activate user if suspended
	_, err = tx.Exec(`UPDATE users SET status = 'active', data_deletion_at = NULL WHERE id = ? AND status IN ('suspended', 'expired')`, userID)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

func DebitWallet(userID string, amountPaise int64, description string) error {
	tx, err := DB.Begin()
	if err != nil {
		return err
	}

	var balance int64
	err = tx.QueryRow(`SELECT balance_paise FROM wallet WHERE user_id = ?`, userID).Scan(&balance)
	if err != nil {
		tx.Rollback()
		return err
	}

	if balance < amountPaise {
		tx.Rollback()
		return fmt.Errorf("insufficient balance")
	}

	_, err = tx.Exec(`UPDATE wallet SET balance_paise = balance_paise - ? WHERE user_id = ?`, amountPaise, userID)
	if err != nil {
		tx.Rollback()
		return err
	}

	id := uuid.New().String()
	_, err = tx.Exec(`INSERT INTO transactions (id, user_id, type, amount_paise, description) VALUES (?, ?, 'monthly_deduction', ?, ?)`,
		id, userID, amountPaise, description)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}

func GetTransactions(userID string, limit int) ([]Transaction, error) {
	rows, err := DB.Query(`SELECT id, user_id, type, amount_paise, description, COALESCE(dodopay_ref,''), created_at FROM transactions WHERE user_id = ? ORDER BY created_at DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var txns []Transaction
	for rows.Next() {
		var t Transaction
		rows.Scan(&t.ID, &t.UserID, &t.Type, &t.AmountPaise, &t.Description, &t.DodopayRef, &t.CreatedAt)
		txns = append(txns, t)
	}
	return txns, nil
}

// --- Usage ---

func GetStorageUsed(userID string) (int64, error) {
	var bytes int64
	err := DB.QueryRow(`SELECT COALESCE(bytes_stored, 0) FROM usage_daily WHERE user_id = ? ORDER BY date DESC LIMIT 1`, userID).Scan(&bytes)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return bytes, err
}

func RecordUsage(userID string, bytesStored int64) error {
	date := time.Now().Format("2006-01-02")
	_, err := DB.Exec(`INSERT INTO usage_daily (user_id, date, bytes_stored) VALUES (?, ?, ?) ON CONFLICT(user_id, date) DO UPDATE SET bytes_stored = ?`,
		userID, date, bytesStored, bytesStored)
	return err
}

// --- Helpers ---

func generateToken(bytes int) string {
	b := make([]byte, bytes)
	rand.Read(b)
	return hex.EncodeToString(b)
}
