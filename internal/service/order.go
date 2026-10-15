package service

import (
	"crypto/rand"
	"database/sql"
	"fmt"
	"math"
	"math/big"
	mrand "math/rand"
	"strings"
	"sync"
	"time"
)

type OrderData struct {
	OrderID            string  `json:"order_id"`
	ReferenceID        string  `json:"reference_id"`
	Currency           string  `json:"currency"`
	OriginalAmount     float64 `json:"original_amount"`
	UniqueCode         int     `json:"unique_code"`
	TotalAmount        float64 `json:"total_amount"`
	TotalAmountStr     string  `json:"total_amount_str"`
	BinancePayID       string  `json:"binance_pay_id"`
	BinanceAccountName string  `json:"binance_account_name"`
	BinanceUniQRURL    string  `json:"binance_uni_qr_url"`
	Status             string  `json:"status"`
	BinanceTxID        string  `json:"binance_tx_id,omitempty"`
	PayerName          string  `json:"payer_name,omitempty"`
	PaidAt             string  `json:"paid_at,omitempty"`
	QRURL              string  `json:"qr_url"`
	ExpiresAt          string  `json:"expires_at"`
	ExpiresInSeconds   int64   `json:"expires_in_seconds"`
}

// RandomString menghasilkan string alfanumerik acak sepanjang n karakter
func RandomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, n)
	for i := range b {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		b[i] = letters[num.Int64()]
	}
	return string(b)
}

var orderMutex sync.Mutex

// CreateOrder mengalokasikan kode unik milli-units dan membuat order baru
func CreateOrder(
	db *sql.DB,
	appID, referenceID, currency string,
	amount float64,
	expiryMinutes, cooldownMinutes int,
	uniQRURL, payID, accountName, qrBaseDir, qrURLPrefix string,
	isSandbox bool,
) (*OrderData, error) {
	if currency == "" {
		currency = "USDT"
	}
	if expiryMinutes <= 0 {
		expiryMinutes = 15
	}
	if cooldownMinutes <= 0 {
		cooldownMinutes = 60
	}

	cleanCurrency := strings.ToUpper(strings.TrimSpace(currency))
	originalAmountMilli := int64(math.Round(amount * 1000))
	nowMillis := time.Now().UnixMilli()

	// Kunci alokasi kode unik agar tidak terjadi tabrakan pada request paralel
	orderMutex.Lock()
	defer orderMutex.Unlock()

	// 1 Query cepat untuk mengambil semua kode unik yang sedang terpakai untuk nominal ini
	rows, err := db.Query(`
		SELECT unique_code FROM orders
		WHERE UPPER(TRIM(currency)) = ?
		  AND original_amount_milli = ?
		  AND (status = 'PENDING' OR cooldown_until > ?)
	`, cleanCurrency, originalAmountMilli, nowMillis)
	if err != nil {
		return nil, fmt.Errorf("gagal memeriksa ketersediaan slot kode unik: %w", err)
	}
	defer rows.Close()

	usedCodes := make(map[int]bool)
	for rows.Next() {
		var uCode int
		if err := rows.Scan(&uCode); err == nil {
			usedCodes[uCode] = true
		}
	}
	rows.Close()

	// Tentukan batas maksimal kode unik berdasarkan besaran nominal (Mikro Adaptif)
	// Agar untuk nominal kecil (< 5 USDT) kenaikan harga hanya berkisar 0.001 - 0.025 USDT (Rp 15 - Rp 375),
	// sehingga pembeli tidak merasa kemahalan / rugi.
	var maxUniqueCode int
	if amount < 5.0 {
		maxUniqueCode = 25
	} else if amount < 50.0 {
		maxUniqueCode = 50
	} else {
		maxUniqueCode = 99
	}

	// Acak kandidat kode unik dalam batas mikro
	codes := mrand.Perm(maxUniqueCode)
	var selectedCode int = -1
	var candidateTotalMilli int64 = 0

	for _, c := range codes {
		code := c + 1 // 1..maxUniqueCode
		if !usedCodes[code] {
			selectedCode = code
			candidateTotalMilli = originalAmountMilli + int64(code)
			break
		}
	}

	// Fallback darurat: Jika semua slot mikro sedang terpakai, perluas rentang hingga 150 agar transaksi tidak gagal
	if selectedCode == -1 {
		fallbackLimit := 150
		for code := maxUniqueCode + 1; code <= fallbackLimit; code++ {
			if !usedCodes[code] {
				selectedCode = code
				candidateTotalMilli = originalAmountMilli + int64(code)
				break
			}
		}
	}

	if selectedCode == -1 {
		return nil, fmt.Errorf("semua slot kode unik sedang terpakai untuk nominal ini, silakan coba beberapa saat lagi")
	}

	totalAmount := float64(candidateTotalMilli) / 1000.0
	totalAmountStr := fmt.Sprintf("%.3f", totalAmount)

	orderID := "ord_" + RandomString(12)
	expiresAtMillis := nowMillis + int64(expiryMinutes*60000)
	cooldownUntilMillis := expiresAtMillis + int64(cooldownMinutes*60000)

	qrPath := fmt.Sprintf("%s/%s.png", strings.TrimRight(qrBaseDir, "/"), orderID)
	qrContent := uniQRURL
	if qrContent == "" {
		qrContent = "https://app.binance.com/uni-qr/pay_" + payID
	}

	if err := GenerateQRCode(qrContent, qrPath); err != nil {
		return nil, fmt.Errorf("gagal membuat qr code: %w", err)
	}

	_, err = db.Exec(`
		INSERT INTO orders (
			id, app_id, reference_id, currency, original_amount, original_amount_milli,
			unique_code, total_amount, total_amount_str, total_amount_milli, status,
			qr_path, created_at, expires_at, cooldown_until
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'PENDING', ?, ?, ?, ?)
	`,
		orderID, appID, referenceID, cleanCurrency, amount, originalAmountMilli,
		selectedCode, totalAmount, totalAmountStr, candidateTotalMilli,
		qrPath, nowMillis, expiresAtMillis, cooldownUntilMillis,
	)
	if err != nil {
		return nil, fmt.Errorf("gagal menyimpan order ke database: %w", err)
	}

	expiresAtISO := time.UnixMilli(expiresAtMillis).UTC().Format(time.RFC3339)
	expiresInSec := int64(expiryMinutes * 60)

	return &OrderData{
		OrderID:            orderID,
		ReferenceID:        referenceID,
		Currency:           cleanCurrency,
		OriginalAmount:     amount,
		UniqueCode:         selectedCode,
		TotalAmount:        totalAmount,
		TotalAmountStr:     totalAmountStr,
		BinancePayID:       payID,
		BinanceAccountName: accountName,
		BinanceUniQRURL:    qrContent,
		Status:             "PENDING",
		QRURL:              fmt.Sprintf("%s/%s/qr.png", strings.TrimRight(qrURLPrefix, "/"), orderID),
		ExpiresAt:          expiresAtISO,
		ExpiresInSeconds:   expiresInSec,
	}, nil
}

// MatchIncomingTransactions mencocokkan mutasi Binance masuk dengan order aktif
func MatchIncomingTransactions(db *sql.DB, txs []BinancePayTransaction) (int, error) {
	matchedCount := 0
	now := time.Now().UnixMilli()
	dayAgo := now - (24 * 60 * 60 * 1000)

	for _, tx := range txs {
		parsedFloat, err := tx.GetAmountFloat()
		if err != nil || parsedFloat <= 0 {
			continue
		}

		txMilli := int64(math.Round(parsedFloat * 1000))
		txID := tx.GetTransactionID()
		if txID == "" {
			continue
		}

		txTime := tx.GetTransactionTime()
		if txTime <= 0 {
			txTime = now
		}

		// Cek apakah txId sudah pernah diproses
		var exists int
		_ = db.QueryRow("SELECT COUNT(1) FROM orders WHERE binance_tx_id = ?", txID).Scan(&exists)
		if exists > 0 {
			continue
		}

		// Format nama pengirim
		payerDisplay := strings.TrimSpace(tx.PayerInfo.Name)
		if payerDisplay == "" && tx.PayerInfo.GetBinanceID() != "" {
			payerDisplay = "ID:" + tx.PayerInfo.GetBinanceID()
		}

		cleanCurrency := strings.ToUpper(strings.TrimSpace(tx.Currency))

		// Cari order yang cocok (dengan buffer 3 menit untuk menyerap perbedaan jam lokal vs Binance)
		var (
			orderID        string
			appID          string
			referenceID    string
			currency       string
			originalAmount float64
			uniqueCode     int
			totalAmount    float64
			totalAmountStr string
		)

		err = db.QueryRow(`
			SELECT id, app_id, reference_id, currency, original_amount, unique_code, total_amount, total_amount_str
			FROM orders
			WHERE UPPER(TRIM(currency)) = ?
			  AND total_amount_milli = ?
			  AND created_at <= ?
			  AND (
			    status = 'PENDING'
			    OR (status = 'EXPIRED' AND cooldown_until > ?)
			  )
			ORDER BY created_at DESC
			LIMIT 1
		`, cleanCurrency, txMilli, txTime+180000, txTime).Scan(
			&orderID, &appID, &referenceID, &currency, &originalAmount, &uniqueCode, &totalAmount, &totalAmountStr,
		)

		if err == nil && orderID != "" {
			// Update status secara atomik (hanya jika belum PAID)
			res, updateErr := db.Exec(`
				UPDATE orders 
				SET status = 'PAID', binance_tx_id = ?, payer_name = ?, paid_at = ?
				WHERE id = ? AND status != 'PAID'
			`, txID, payerDisplay, txTime, orderID)

			if updateErr == nil {
				if rows, _ := res.RowsAffected(); rows > 0 {
					matchedCount++
					go SendOrderWebhook(db, orderID, false)
				}
			}
		} else {
			// Jika transaksi terjadi dalam 24 jam terakhir dan tidak cocok, simpan ke unmatched_transactions
			if txTime >= dayAgo {
				_, _ = db.Exec(`
					INSERT INTO unmatched_transactions (binance_tx_id, currency, amount_str, amount_milli, payer_name, tx_time)
					VALUES (?, ?, ?, ?, ?, ?)
					ON CONFLICT(binance_tx_id) DO NOTHING
				`, txID, cleanCurrency, tx.GetAmountStr(), txMilli, payerDisplay, txTime)
			}
		}
	}

	return matchedCount, nil
}
