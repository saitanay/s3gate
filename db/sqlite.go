package db

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var DB *sql.DB

func Init(dbPath string) {
	dir := filepath.Dir(dbPath)
	os.MkdirAll(dir, 0755)

	var err error
	DB, err = sql.Open("sqlite", dbPath+"?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	DB.SetMaxOpenConns(1) // SQLite single-writer
	DB.SetMaxIdleConns(1)

	migrate()
}

func migrate() {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		status TEXT NOT NULL DEFAULT 'trial',
		trial_starts_at DATETIME,
		trial_expires_at DATETIME,
		data_deletion_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS auth_tokens (
		id TEXT PRIMARY KEY,
		email TEXT NOT NULL,
		token TEXT UNIQUE NOT NULL,
		expires_at DATETIME NOT NULL,
		used_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sessions (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id),
		token TEXT UNIQUE NOT NULL,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS api_keys (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id),
		access_key TEXT UNIQUE NOT NULL,
		secret_key TEXT NOT NULL,
		label TEXT DEFAULT '',
		revoked_at DATETIME,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS wallet (
		user_id TEXT PRIMARY KEY REFERENCES users(id),
		balance_paise INTEGER NOT NULL DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS transactions (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id),
		type TEXT NOT NULL,
		amount_paise INTEGER NOT NULL,
		description TEXT,
		dodopay_ref TEXT UNIQUE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS usage_daily (
		user_id TEXT NOT NULL REFERENCES users(id),
		date TEXT NOT NULL,
		bytes_stored INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user_id, date)
	);

	CREATE TABLE IF NOT EXISTS buckets (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id),
		name TEXT NOT NULL,
		internal_name TEXT NOT NULL UNIQUE,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE UNIQUE INDEX IF NOT EXISTS idx_buckets_internal ON buckets(internal_name);

	CREATE INDEX IF NOT EXISTS idx_api_keys_access_key ON api_keys(access_key) WHERE revoked_at IS NULL;
	CREATE INDEX IF NOT EXISTS idx_auth_tokens_token ON auth_tokens(token);
	CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token);
	`

	_, err := DB.Exec(schema)
	if err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	// Migration: add unique index on dodopay_ref (handles existing DBs)
	DB.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_transactions_dodopay_ref ON transactions(dodopay_ref) WHERE dodopay_ref IS NOT NULL`)

	log.Println("Database migrated")
}
