package service

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type WebhookPayload struct {
	Event          string  `json:"event"`
	IsSandbox      bool    `json:"is_sandbox"`
	OrderID        string  `json:"order_id"`
	ReferenceID    string  `json:"reference_id"`
	Currency       string  `json:"currency"`
	OriginalAmount float64 `json:"original_amount"`
	UniqueCode     int     `json:"unique_code"`
	TotalAmount    float64 `json:"total_amount"`
	TotalAmountStr string  `json:"total_amount_str"`
	PaidAt         string  `json:"paid_at"`
	BinanceTxID    string  `json:"binance_tx_id"`
	Timestamp      int64   `json:"timestamp"`
}

var webhookHTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	},
}

// SendOrderWebhook mengirimkan webhook untuk order yang baru lunas
func SendOrderWebhook(db *sql.DB, orderID string, isSandbox bool) {
	var (
		appID          string
		referenceID    string
		currency       string
		originalAmount float64
		uniqueCode     int
		totalAmount    float64
		totalAmountStr string
		paidAtMillis   int64
		binanceTxID    string
		attempts       int
		delivered      int
	)

	err := db.QueryRow(`
		SELECT app_id, reference_id, currency, original_amount, unique_code, 
		       total_amount, total_amount_str, paid_at, binance_tx_id, webhook_attempts, webhook_delivered
		FROM orders 
		WHERE id = ?
	`, orderID).Scan(
		&appID, &referenceID, &currency, &originalAmount, &uniqueCode,
		&totalAmount, &totalAmountStr, &paidAtMillis, &binanceTxID, &attempts, &delivered,
	)
	if err != nil {
		log.Printf("[Webhook] Order %s tidak ditemukan: %v", orderID, err)
		return
	}

	// Jangan kirim ulang jika sudah berstatus sukses terkirim atau melebihi kuota 5 kali
	if delivered == 1 || attempts >= 5 {
		return
	}

	var webhookURL, apiKey string
	err = db.QueryRow("SELECT webhook_url, api_key FROM apps WHERE id = ?", appID).Scan(&webhookURL, &apiKey)
	if err != nil {
		log.Printf("[Webhook] App %s tidak ditemukan untuk order %s: %v", appID, orderID, err)
		return
	}

	if webhookURL == "" {
		return
	}

	now := time.Now().UnixMilli()
	nowSec := time.Now().Unix()
	// Kunci sementara jeda retry 60 detik ke depan agar background retry tidak mengambil order yang sama
	_, _ = db.Exec("UPDATE orders SET next_webhook_retry_at = ? WHERE id = ?", now+60000, orderID)

	paidAtTime := time.UnixMilli(paidAtMillis).UTC().Format(time.RFC3339)
	payload := WebhookPayload{
		Event:          "payment.success",
		IsSandbox:      isSandbox,
		OrderID:        orderID,
		ReferenceID:    referenceID,
		Currency:       currency,
		OriginalAmount: originalAmount,
		UniqueCode:     uniqueCode,
		TotalAmount:    totalAmount,
		TotalAmountStr: totalAmountStr,
		PaidAt:         paidAtTime,
		BinanceTxID:    binanceTxID,
		Timestamp:      nowSec,
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("[Webhook] Gagal marshal payload order %s: %v", orderID, err)
		return
	}

	signature := ComputeHmac256(string(bodyBytes), apiKey)

	req, err := http.NewRequest(http.MethodPost, webhookURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Printf("[Webhook] Gagal membuat request order %s: %v", orderID, err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Event", "payment.success")
	req.Header.Set("X-Webhook-Signature", signature)
	req.Header.Set("X-Webhook-Timestamp", fmt.Sprintf("%d", nowSec))
	req.Header.Set("User-Agent", "PaymentG-Binance-Webhook/1.0")

	attempts++
	resp, err := webhookHTTPClient.Do(req)

	if err == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		_, _ = db.Exec("UPDATE orders SET webhook_delivered = 1, webhook_attempts = ? WHERE id = ?", attempts, orderID)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		log.Printf("[Webhook] Sukses kirim webhook order %s ke %s (HTTP %d)", orderID, webhookURL, resp.StatusCode)
		return
	}

	if resp != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	// Jadwal retry bertahap: 15s, 60s, 300s, max 5 kali
	var retryDelaySec int64 = 15
	if attempts == 2 {
		retryDelaySec = 60
	} else if attempts >= 3 {
		retryDelaySec = 300
	}

	nextRetry := time.Now().UnixMilli() + (retryDelaySec * 1000)
	_, _ = db.Exec("UPDATE orders SET webhook_attempts = ?, next_webhook_retry_at = ? WHERE id = ?", attempts, nextRetry, orderID)
	log.Printf("[Webhook] Gagal kirim webhook order %s (attempt %d). Dijadwalkan retry dalam %d detik", orderID, attempts, retryDelaySec)
}

// StartWebhookRetryWorker menjalankan background ticker untuk retry webhook yang tertunda
func StartWebhookRetryWorker(liveDB, sandboxDB *sql.DB) {
	go func() {
		ticker := time.NewTicker(15 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			processPendingWebhooks(liveDB, false)
			processPendingWebhooks(sandboxDB, true)
		}
	}()
}

func processPendingWebhooks(db *sql.DB, isSandbox bool) {
	now := time.Now().UnixMilli()
	rows, err := db.Query(`
		SELECT id FROM orders 
		WHERE status = 'PAID' AND webhook_delivered = 0 AND webhook_attempts < 5 AND next_webhook_retry_at <= ?
		LIMIT 10
	`, now)
	if err != nil {
		return
	}
	defer rows.Close()

	var orderIDs []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			orderIDs = append(orderIDs, id)
		}
	}

	for _, id := range orderIDs {
		go SendOrderWebhook(db, id, isSandbox)
	}
}
