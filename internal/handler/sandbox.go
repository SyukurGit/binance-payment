package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"time"

	"paymentg-binance/internal/config"
	"paymentg-binance/internal/database"
	"paymentg-binance/internal/service"

	"github.com/gin-gonic/gin"
)

type SandboxHandler struct {
	db  *sql.DB
	hub *database.DBHub
	cfg *config.Config
}

func NewSandboxHandler(hub *database.DBHub, cfg *config.Config) *SandboxHandler {
	return &SandboxHandler{
		db:  hub.SandboxDB,
		hub: hub,
		cfg: cfg,
	}
}

// GetSandboxStats mengambil statistik omset dan ringkasan order di Sandbox
func (h *SandboxHandler) GetSandboxStats(c *gin.Context) {
	var totalOmset float64
	var countPaid, countPending, countExpired int
	now := time.Now().UnixMilli()

	_ = h.db.QueryRow("SELECT COALESCE(SUM(total_amount), 0) FROM orders WHERE status = 'PAID'").Scan(&totalOmset)
	_ = h.db.QueryRow("SELECT COUNT(1) FROM orders WHERE status = 'PAID'").Scan(&countPaid)
	_ = h.db.QueryRow("SELECT COUNT(1) FROM orders WHERE status = 'PENDING' AND expires_at >= ?", now).Scan(&countPending)
	_ = h.db.QueryRow("SELECT COUNT(1) FROM orders WHERE status = 'EXPIRED' OR (status = 'PENDING' AND expires_at < ?)", now).Scan(&countExpired)

	var countApps int
	_ = h.db.QueryRow("SELECT COUNT(1) FROM apps").Scan(&countApps)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"total_omset_usdt": totalOmset,
			"count_paid":       countPaid,
			"count_pending":    countPending,
			"count_expired":    countExpired,
			"count_apps":       countApps,
		},
	})
}

// SimulatePay mensimulasikan pembayaran lunas 1-klik di Sandbox
func (h *SandboxHandler) SimulatePay(c *gin.Context) {
	id := c.Param("id")

	var status string
	err := h.db.QueryRow("SELECT status FROM orders WHERE id = ?", id).Scan(&status)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Order sandbox tidak ditemukan"})
		return
	}

	if status == "PAID" {
		c.JSON(http.StatusOK, gin.H{"success": true, "message": "Order sandbox sudah berstatus PAID sebelumnya", "order_id": id})
		return
	}

	now := time.Now().UnixMilli()
	fakeTxID := fmt.Sprintf("SBX_%d", now)
	fakePayer := "SandboxTester"

	_, err = h.db.Exec(`
		UPDATE orders 
		SET status = 'PAID', binance_tx_id = ?, payer_name = ?, paid_at = ?
		WHERE id = ?
	`, fakeTxID, fakePayer, now, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Gagal update status order sandbox"})
		return
	}

	// Picu webhook sandbox
	go service.SendOrderWebhook(h.db, id, true)

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"message":       "Order sandbox berhasil disimulasikan lunas! Webhook telah dikirim ke toko.",
		"order_id":      id,
		"binance_tx_id": fakeTxID,
	})
}

// CreateDemoOrder membuat tagihan test cepat langsung dari UI sandbox
func (h *SandboxHandler) CreateDemoOrder(c *gin.Context) {
	var appID string
	err := h.db.QueryRow("SELECT id FROM apps LIMIT 1").Scan(&appID)
	if err != nil || appID == "" {
		// Buat demo app otomatis jika belum ada toko
		appID = "app_demo_sandbox"
		apiKey := "ak_test_" + service.RandomString(32)
		_ = database.CreateApp(h.db, appID, "Toko Demo Sandbox", apiKey, "https://webhook.site/placeholder-testing")
	}

	refID := fmt.Sprintf("DEMO-%d-%s", time.Now().Unix()%100000, service.RandomString(4))
	amount := 10.0

	uniQR := database.GetSystemConfig(h.db, "BINANCE_UNI_QR_URL", h.cfg.BinanceUniQRURL)
	payID := database.GetSystemConfig(h.db, "BINANCE_PAY_ID", h.cfg.BinancePayID)
	accountName := database.GetSystemConfig(h.db, "BINANCE_ACCOUNT_NAME", h.cfg.BinanceAccountName)

	orderData, err := service.CreateOrder(
		h.db,
		appID,
		refID,
		"USDT",
		amount,
		15,
		h.cfg.CooldownMinutes,
		uniQR,
		payID,
		accountName,
		"data/sandbox/qr",
		"/api/sandbox/orders",
		true,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    orderData,
	})
}

// ListSandboxOrders mengambil seluruh order di SandboxDB
func (h *SandboxHandler) ListSandboxOrders(c *gin.Context) {
	rows, err := h.db.Query(`
		SELECT id, reference_id, currency, original_amount, unique_code, total_amount, total_amount_str,
		       status, binance_tx_id, payer_name, created_at, expires_at, paid_at
		FROM orders 
		ORDER BY created_at DESC 
		LIMIT 100
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	defer rows.Close()

	var orders []gin.H
	now := time.Now().UnixMilli()

	for rows.Next() {
		var (
			id             string
			refID          string
			currency       string
			originalAmount float64
			uniqueCode     int
			totalAmount    float64
			totalAmountStr string
			status         string
			binanceTxID    sql.NullString
			payerName      sql.NullString
			createdAt      int64
			expiresAt      int64
			paidAt         int64
		)

		if err := rows.Scan(
			&id, &refID, &currency, &originalAmount, &uniqueCode, &totalAmount, &totalAmountStr,
			&status, &binanceTxID, &payerName, &createdAt, &expiresAt, &paidAt,
		); err == nil {
			paidAtStr := "-"
			if paidAt > 0 {
				paidAtStr = time.UnixMilli(paidAt).Format("02 Jan 15:04:05")
			}

			expiresInSec := (expiresAt - now) / 1000
			if expiresInSec < 0 {
				expiresInSec = 0
			}

			orders = append(orders, gin.H{
				"id":                 id,
				"reference_id":       refID,
				"currency":           currency,
				"original_amount":    originalAmount,
				"unique_code":        uniqueCode,
				"total_amount":       totalAmount,
				"total_amount_str":   totalAmountStr,
				"status":             status,
				"binance_tx_id":      binanceTxID.String,
				"payer_name":         payerName.String,
				"created_at":         time.UnixMilli(createdAt).Format("02 Jan 15:04:05"),
				"expires_in_seconds": expiresInSec,
				"paid_at":            paidAtStr,
				"qr_url":             "/api/sandbox/orders/" + id + "/qr.png",
			})
		}
	}

	if orders == nil {
		orders = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    orders,
	})
}

// GetSandboxCredentials mengembalikan API Key aktif dan Base URL untuk testing langsung
func (h *SandboxHandler) GetSandboxCredentials(c *gin.Context) {
	var id, name, apiKey, webhookURL string
	err := h.db.QueryRow("SELECT id, name, api_key, webhook_url FROM apps ORDER BY created_at ASC LIMIT 1").Scan(&id, &name, &apiKey, &webhookURL)
	if err != nil || apiKey == "" {
		// Buat app default otomatis jika belum ada toko di sandbox
		id = "app_demo_sandbox"
		name = "Toko Testing Sandbox"
		apiKey = "ak_test_" + service.RandomString(32)
		webhookURL = "https://webhook.site/test-sandbox-binance"
		_ = database.CreateApp(h.db, id, name, apiKey, webhookURL)
	}

	scheme := "http"
	if c.Request.TLS != nil {
		scheme = "https"
	}
	host := c.Request.Host
	if host == "" {
		host = "localhost:" + h.cfg.Port
	}
	baseURL := fmt.Sprintf("%s://%s/api/sandbox", scheme, host)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"app_id":           id,
			"app_name":         name,
			"api_key":          apiKey,
			"webhook_url":      webhookURL,
			"sandbox_base_url": baseURL,
			"example_curl": fmt.Sprintf(`curl -X POST %s/orders \
  -H "X-API-Key: %s" \
  -H "Content-Type: application/json" \
  -d '{"reference_id": "INV-001", "amount": 10}'`, baseURL, apiKey),
		},
	})
}

// ListSandboxApps mengambil daftar toko di Sandbox
func (h *SandboxHandler) ListSandboxApps(c *gin.Context) {
	rows, err := h.db.Query("SELECT id, name, api_key, webhook_url, created_at FROM apps ORDER BY created_at DESC")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	defer rows.Close()

	var apps []gin.H
	for rows.Next() {
		var id, name, apiKey, webhookURL, createdAt string
		if err := rows.Scan(&id, &name, &apiKey, &webhookURL, &createdAt); err == nil {
			apps = append(apps, gin.H{
				"id":          id,
				"name":        name,
				"api_key":     apiKey,
				"webhook_url": webhookURL,
				"created_at":  createdAt,
			})
		}
	}
	if apps == nil {
		apps = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": apps})
}

// CreateSandboxApp membuat toko baru dari UI Sandbox (hanya ke SandboxDB)
func (h *SandboxHandler) CreateSandboxApp(c *gin.Context) {
	var body struct {
		Name       string `json:"name" binding:"required"`
		WebhookURL string `json:"webhook_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Nama toko dan Webhook URL wajib diisi"})
		return
	}

	appID := "app_" + service.RandomString(12)
	apiKey := "ak_test_" + service.RandomString(32)

	if err := database.CreateApp(h.db, appID, body.Name, apiKey, body.WebhookURL); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data": gin.H{
			"id":          appID,
			"name":        body.Name,
			"api_key":     apiKey,
			"webhook_url": body.WebhookURL,
		},
	})
}

// DeleteSandboxApp menghapus toko dari UI Sandbox (hanya dari SandboxDB)
func (h *SandboxHandler) DeleteSandboxApp(c *gin.Context) {
	id := c.Param("id")
	if err := database.DeleteApp(h.db, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Toko sandbox berhasil dihapus"})
}
