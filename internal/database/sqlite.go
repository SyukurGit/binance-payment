package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type DBHub struct {
	LiveDB    *sql.DB
	SandboxDB *sql.DB
}

const schemaDDL = `
CREATE TABLE IF NOT EXISTS system_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS apps (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    api_key TEXT NOT NULL UNIQUE,
    webhook_url TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS orders (
    id TEXT PRIMARY KEY,
    app_id TEXT NOT NULL,
    reference_id TEXT NOT NULL,
    currency TEXT NOT NULL DEFAULT 'USDT',
    original_amount REAL NOT NULL,
    original_amount_milli INTEGER NOT NULL,
    unique_code INTEGER NOT NULL,
    total_amount REAL NOT NULL,
    total_amount_str TEXT NOT NULL,
    total_amount_milli INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'PENDING',
    binance_tx_id TEXT UNIQUE,
    payer_name TEXT DEFAULT '',
    qr_path TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    cooldown_until INTEGER NOT NULL,
    paid_at INTEGER DEFAULT 0,
    webhook_delivered INTEGER DEFAULT 0,
    webhook_attempts INTEGER DEFAULT 0,
    next_webhook_retry_at INTEGER DEFAULT 0,
    FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_orders_matching 
ON orders(currency, total_amount_milli, status, cooldown_until);

CREATE INDEX IF NOT EXISTS idx_orders_status_created 
ON orders(status, created_at, expires_at);

CREATE TABLE IF NOT EXISTS unmatched_transactions (
    binance_tx_id TEXT PRIMARY KEY,
    currency TEXT NOT NULL,
    amount_str TEXT NOT NULL,
    amount_milli INTEGER NOT NULL,
    payer_name TEXT DEFAULT '',
    tx_time INTEGER NOT NULL,
    recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

func InitDB() (*DBHub, error) {
	// Pastikan direktori data ada
	if err := os.MkdirAll("data/qr", 0755); err != nil {
		return nil, fmt.Errorf("gagal membuat folder data/qr: %w", err)
	}
	if err := os.MkdirAll("data/sandbox/qr", 0755); err != nil {
		return nil, fmt.Errorf("gagal membuat folder data/sandbox/qr: %w", err)
	}

	liveDB, err := openSQLite("data/payment.db")
	if err != nil {
		return nil, fmt.Errorf("gagal membuka live db: %w", err)
	}

	sandboxDB, err := openSQLite("data/sandbox/payment.db")
	if err != nil {
		liveDB.Close()
		return nil, fmt.Errorf("gagal membuka sandbox db: %w", err)
	}

	// Migrasi Skema
	if _, err := liveDB.Exec(schemaDDL); err != nil {
		return nil, fmt.Errorf("migrasi skema live db gagal: %w", err)
	}
	if _, err := sandboxDB.Exec(schemaDDL); err != nil {
		return nil, fmt.Errorf("migrasi skema sandbox db gagal: %w", err)
	}

	hub := &DBHub{
		LiveDB:    liveDB,
		SandboxDB: sandboxDB,
	}

	return hub, nil
}

func openSQLite(dbPath string) (*sql.DB, error) {
	absPath, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}

	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)", absPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}

	// SetMaxOpenConns(1) untuk menghindari error SQLITE_BUSY pada modernc.org/sqlite
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return nil, err
	}

	return db, nil
}

// GetSystemConfig mengambil nilai konfigurasi runtime
func GetSystemConfig(db *sql.DB, key string, fallback string) string {
	var val string
	err := db.QueryRow("SELECT value FROM system_config WHERE key = ?", key).Scan(&val)
	if err != nil || val == "" {
		return fallback
	}
	return val
}

// SetSystemConfig menyimpan/memperbarui konfigurasi runtime
func SetSystemConfig(db *sql.DB, key, val string) error {
	_, err := db.Exec(`
		INSERT INTO system_config (key, value, updated_at) 
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = CURRENT_TIMESTAMP
	`, key, val)
	return err
}

// CreateApp menyimpan app baru ke database spesifik (LiveDB atau SandboxDB secara terisolasi)
func CreateApp(db *sql.DB, id, name, apiKey, webhookURL string) error {
	query := `
		INSERT INTO apps (id, name, api_key, webhook_url) 
		VALUES (?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name = excluded.name, api_key = excluded.api_key, webhook_url = excluded.webhook_url
	`
	if _, err := db.Exec(query, id, name, apiKey, webhookURL); err != nil {
		return fmt.Errorf("db error: %w", err)
	}
	return nil
}

// DeleteApp menghapus app dari database spesifik secara terisolasi
func DeleteApp(db *sql.DB, id string) error {
	query := `DELETE FROM apps WHERE id = ?`
	if _, err := db.Exec(query, id); err != nil {
		return fmt.Errorf("db error: %w", err)
	}
	return nil
}
