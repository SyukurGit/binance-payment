package service

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestQRGenerationWithRealLink(t *testing.T) {
	tmpDir := t.TempDir()
	destPath := filepath.Join(tmpDir, "test_qr.png")

	realURL := "https://app.binance.com/uni-qr/NwksZSvZ"
	err := GenerateQRCode(realURL, destPath)
	if err != nil {
		t.Fatalf("GenerateQRCode failed: %v", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatalf("QR file was not created: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("QR file is empty")
	}
}

func TestOrderCreationWithSanitizedUniQR(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open test db: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE orders (
			id TEXT PRIMARY KEY,
			app_id TEXT NOT NULL,
			reference_id TEXT NOT NULL,
			currency TEXT NOT NULL,
			original_amount REAL NOT NULL,
			original_amount_milli INTEGER NOT NULL,
			unique_code INTEGER NOT NULL,
			total_amount REAL NOT NULL,
			total_amount_str TEXT NOT NULL,
			total_amount_milli INTEGER NOT NULL,
			status TEXT NOT NULL,
			binance_tx_id TEXT,
			payer_name TEXT,
			qr_path TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			cooldown_until INTEGER NOT NULL,
			paid_at INTEGER DEFAULT 0
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create orders table: %v", err)
	}

	// Kasus 1: Diberikan dummy example URL -> harus otomatis disanitasi ke real URL
	order1, err := CreateOrder(
		db, "app_test", "REF-001", "USDT", 10.0, 15, 60,
		"https://app.binance.com/uni-qr/c2c_example_your_static_qr",
		"857242044", "AirpayX7",
		tmpDir, "/api/orders", false,
	)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}
	if order1.BinanceUniQRURL != "https://app.binance.com/uni-qr/NwksZSvZ" {
		t.Errorf("Expected sanitized URL https://app.binance.com/uni-qr/NwksZSvZ, got: %s", order1.BinanceUniQRURL)
	}

	// Kasus 2: Diberikan URL asli langsung -> harus dipakai langsung
	order2, err := CreateOrder(
		db, "app_test", "REF-002", "USDT", 20.0, 15, 60,
		"https://app.binance.com/uni-qr/NwksZSvZ",
		"857242044", "AirpayX7",
		tmpDir, "/api/orders", false,
	)
	if err != nil {
		t.Fatalf("CreateOrder failed: %v", err)
	}
	if order2.BinanceUniQRURL != "https://app.binance.com/uni-qr/NwksZSvZ" {
		t.Errorf("Expected URL https://app.binance.com/uni-qr/NwksZSvZ, got: %s", order2.BinanceUniQRURL)
	}
}
