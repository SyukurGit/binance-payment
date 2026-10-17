package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"paymentg-binance/internal/config"
	"paymentg-binance/internal/database"
	"paymentg-binance/internal/middleware"
	"paymentg-binance/internal/service"

	"github.com/gin-gonic/gin"
)

type AdminHandler struct {
	hub    *database.DBHub
	cfg    *config.Config
	poller *service.PollerService
}

func NewAdminHandler(hub *database.DBHub, cfg *config.Config, poller *service.PollerService) *AdminHandler {
	return &AdminHandler{
		hub:    hub,
		cfg:    cfg,
		poller: poller,
	}
}

// HealthCheck mengembalikan status kesehatan server
func (h *AdminHandler) HealthCheck(c *gin.Context) {
	lastCall, lastErr := h.poller.GetStatus()
	lastCallStr := "Belum pernah dipanggil"
	if !lastCall.IsZero() {
		lastCallStr = lastCall.Format(time.RFC3339)
	}

	c.JSON(http.StatusOK, gin.H{
		"status":            "ok",
		"service":           "PaymentG - Binance Pay Edition",
		"server_time":       time.Now().Format(time.RFC3339),
		"binance_last_call": lastCallStr,
		"binance_last_err":  lastErr,
	})
}

// VerifyPIN memverifikasi PIN admin dashboard dengan proteksi brute-force
func (h *AdminHandler) VerifyPIN(c *gin.Context) {
	clientIP := c.ClientIP()

	// 1. Cek apakah IP sedang terkunci akibat terlalu banyak salah PIN
	allowed, waitSec := middleware.CheckPinRateLimit(clientIP)
	if !allowed {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Terlalu banyak percobaan PIN salah. Akses dikunci sementara. Coba lagi dalam %d detik.", waitSec),
		})
		return
	}

	var body struct {
		PIN string `json:"pin" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "PIN wajib diisi"})
		return
	}

	// 2. Bandingkan PIN dengan constant-time comparison
	if !middleware.SecureCompareString(strings.TrimSpace(body.PIN), strings.TrimSpace(h.cfg.AdminPIN)) {
		lockSec := middleware.RecordPinFailure(clientIP)
		time.Sleep(300 * time.Millisecond) // Tambah jeda mikro untuk memperlambat bot brute-force
		if lockSec > 0 {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"success": false,
				"error":   fmt.Sprintf("PIN salah 5 kali berturut-turut. IP Anda dikunci selama %d detik.", lockSec),
			})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "PIN akses salah"})
		return
	}

	// 3. Reset kegagalan setelah PIN benar
	middleware.ResetPinAttempts(clientIP)

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"admin_key": h.cfg.AdminKey,
	})
}

// GetStats mengambil statistik live omset dan ringkasan order
func (h *AdminHandler) GetStats(c *gin.Context) {
	var totalOmset float64
	var countPaid, countPending, countExpired int
	now := time.Now().UnixMilli()

	_ = h.hub.LiveDB.QueryRow("SELECT COALESCE(SUM(total_amount), 0) FROM orders WHERE status = 'PAID'").Scan(&totalOmset)
	_ = h.hub.LiveDB.QueryRow("SELECT COUNT(1) FROM orders WHERE status = 'PAID'").Scan(&countPaid)
	_ = h.hub.LiveDB.QueryRow("SELECT COUNT(1) FROM orders WHERE status = 'PENDING' AND expires_at >= ?", now).Scan(&countPending)
	_ = h.hub.LiveDB.QueryRow("SELECT COUNT(1) FROM orders WHERE status = 'EXPIRED' OR (status = 'PENDING' AND expires_at < ?)", now).Scan(&countExpired)

	var countUnmatched int
	_ = h.hub.LiveDB.QueryRow("SELECT COUNT(1) FROM unmatched_transactions").Scan(&countUnmatched)

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"total_omset_usdt": totalOmset,
			"count_paid":       countPaid,
			"count_pending":    countPending,
			"count_expired":    countExpired,
			"count_unmatched":  countUnmatched,
		},
	})
}

// GetOrders mengambil daftar order di LiveDB
func (h *AdminHandler) GetOrders(c *gin.Context) {
	statusFilter := c.Query("status")

	query := `
		SELECT id, reference_id, currency, original_amount, unique_code, total_amount, total_amount_str,
		       status, binance_tx_id, payer_name, created_at, expires_at, paid_at, webhook_delivered
		FROM orders
	`
	var args []interface{}
	if statusFilter != "" {
		query += " WHERE status = ?"
		args = append(args, statusFilter)
	}
	query += " ORDER BY created_at DESC LIMIT 100"

	rows, err := h.hub.LiveDB.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	defer rows.Close()

	var orders []gin.H
	now := time.Now().UnixMilli()

	for rows.Next() {
		var (
			id               string
			refID            string
			currency         string
			originalAmount   float64
			uniqueCode       int
			totalAmount      float64
			totalAmountStr   string
			status           string
			binanceTxID      sql.NullString
			payerName        sql.NullString
			createdAt        int64
			expiresAt        int64
			paidAt           int64
			webhookDelivered int
		)

		if err := rows.Scan(
			&id, &refID, &currency, &originalAmount, &uniqueCode, &totalAmount, &totalAmountStr,
			&status, &binanceTxID, &payerName, &createdAt, &expiresAt, &paidAt, &webhookDelivered,
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
				"webhook_delivered":  webhookDelivered == 1,
				"qr_url":             "/api/orders/" + id + "/qr.png",
			})
		}
	}

	if orders == nil {
		orders = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": orders})
}

// GetUnmatched mengambil daftar mutasi yang tidak cocok dengan order mana pun
func (h *AdminHandler) GetUnmatched(c *gin.Context) {
	rows, err := h.hub.LiveDB.Query(`
		SELECT binance_tx_id, currency, amount_str, payer_name, tx_time, recorded_at
		FROM unmatched_transactions
		ORDER BY tx_time DESC LIMIT 100
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}
	defer rows.Close()

	var list []gin.H
	for rows.Next() {
		var (
			txID       string
			currency   string
			amountStr  string
			payerName  string
			txTime     int64
			recordedAt string
		)
		if err := rows.Scan(&txID, &currency, &amountStr, &payerName, &txTime, &recordedAt); err == nil {
			list = append(list, gin.H{
				"binance_tx_id": txID,
				"currency":      currency,
				"amount_str":    amountStr,
				"payer_name":    payerName,
				"tx_time":       time.UnixMilli(txTime).Format("02 Jan 15:04:05"),
				"recorded_at":   recordedAt,
			})
		}
	}

	if list == nil {
		list = []gin.H{}
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// GetApps mengambil daftar web toko terdaftar
func (h *AdminHandler) GetApps(c *gin.Context) {
	rows, err := h.hub.LiveDB.Query("SELECT id, name, api_key, webhook_url, created_at FROM apps ORDER BY created_at DESC")
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

// CreateApp mendaftarkan toko baru hanya ke LiveDB dengan prefix ak_live_
func (h *AdminHandler) CreateApp(c *gin.Context) {
	var body struct {
		Name       string `json:"name" binding:"required"`
		WebhookURL string `json:"webhook_url" binding:"required"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Nama toko dan Webhook URL wajib diisi"})
		return
	}

	appID := "app_" + service.RandomString(12)
	apiKey := "ak_live_" + service.RandomString(32)

	if err := database.CreateApp(h.hub.LiveDB, appID, body.Name, apiKey, body.WebhookURL); err != nil {
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

// DeleteApp menghapus toko hanya dari LiveDB
func (h *AdminHandler) DeleteApp(c *gin.Context) {
	id := c.Param("id")

	if err := database.DeleteApp(h.hub.LiveDB, id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Toko berhasil dihapus"})
}

// GetBinanceConfig mengambil konfigurasi Binance Pay
func (h *AdminHandler) GetBinanceConfig(c *gin.Context) {
	apiKey := database.GetSystemConfig(h.hub.LiveDB, "BINANCE_API_KEY", h.cfg.BinanceAPIKey)
	secretKey := database.GetSystemConfig(h.hub.LiveDB, "BINANCE_SECRET_KEY", h.cfg.BinanceSecretKey)
	payID := database.GetSystemConfig(h.hub.LiveDB, "BINANCE_PAY_ID", h.cfg.BinancePayID)
	accountName := database.GetSystemConfig(h.hub.LiveDB, "BINANCE_ACCOUNT_NAME", h.cfg.BinanceAccountName)
	uniQR := database.GetSystemConfig(h.hub.LiveDB, "BINANCE_UNI_QR_URL", h.cfg.BinanceUniQRURL)

	maskedKey := ""
	if len(apiKey) > 8 {
		maskedKey = apiKey[:4] + "****" + apiKey[len(apiKey)-4:]
	} else if apiKey != "" {
		maskedKey = "****"
	}

	maskedSecret := ""
	if len(secretKey) > 8 {
		maskedSecret = secretKey[:4] + "****" + secretKey[len(secretKey)-4:]
	} else if secretKey != "" {
		maskedSecret = "****"
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"binance_pay_id":       payID,
			"binance_account_name": accountName,
			"binance_uni_qr_url":   uniQR,
			"binance_api_key":      maskedKey,
			"binance_secret_key":   maskedSecret,
			"has_api_key":          apiKey != "",
			"has_secret_key":       secretKey != "",
		},
	})
}

// UpdateBinanceConfig memperbarui konfigurasi Binance Pay di database runtime LiveDB
func (h *AdminHandler) UpdateBinanceConfig(c *gin.Context) {
	var body struct {
		BinancePayID       string `json:"binance_pay_id"`
		BinanceAccountName string `json:"binance_account_name"`
		BinanceUniQRURL    string `json:"binance_uni_qr_url"`
		BinanceAPIKey      string `json:"binance_api_key"`
		BinanceSecretKey   string `json:"binance_secret_key"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	if strings.TrimSpace(body.BinancePayID) != "" {
		_ = database.SetSystemConfig(h.hub.LiveDB, "BINANCE_PAY_ID", strings.TrimSpace(body.BinancePayID))
	}
	if strings.TrimSpace(body.BinanceAccountName) != "" {
		_ = database.SetSystemConfig(h.hub.LiveDB, "BINANCE_ACCOUNT_NAME", strings.TrimSpace(body.BinanceAccountName))
	}
	if strings.TrimSpace(body.BinanceUniQRURL) != "" {
		_ = database.SetSystemConfig(h.hub.LiveDB, "BINANCE_UNI_QR_URL", strings.TrimSpace(body.BinanceUniQRURL))
	}
	trimmedKey := strings.TrimSpace(body.BinanceAPIKey)
	if trimmedKey != "" && !strings.Contains(trimmedKey, "****") {
		_ = database.SetSystemConfig(h.hub.LiveDB, "BINANCE_API_KEY", trimmedKey)
	}
	trimmedSecret := strings.TrimSpace(body.BinanceSecretKey)
	if trimmedSecret != "" && !strings.Contains(trimmedSecret, "****") {
		_ = database.SetSystemConfig(h.hub.LiveDB, "BINANCE_SECRET_KEY", trimmedSecret)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Konfigurasi Binance Pay berhasil disimpan ke database",
	})
}

// TestBinanceConfig menguji koneksi API Binance Pay
func (h *AdminHandler) TestBinanceConfig(c *gin.Context) {
	apiKey := database.GetSystemConfig(h.hub.LiveDB, "BINANCE_API_KEY", h.cfg.BinanceAPIKey)
	secretKey := database.GetSystemConfig(h.hub.LiveDB, "BINANCE_SECRET_KEY", h.cfg.BinanceSecretKey)
	baseURL := database.GetSystemConfig(h.hub.LiveDB, "BINANCE_BASE_URL", h.cfg.BinanceBaseURL)

	if apiKey == "" || secretKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "API Key atau Secret Key belum disetel"})
		return
	}

	txs, err := service.FetchPayTransactions(baseURL, apiKey, secretKey, 0)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Tes koneksi gagal: " + err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"message":       "Koneksi ke Binance Pay API berhasil!",
		"incoming_count": len(txs),
	})
}
