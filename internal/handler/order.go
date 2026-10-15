package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"time"

	"paymentg-binance/internal/config"
	"paymentg-binance/internal/database"
	"paymentg-binance/internal/service"

	"github.com/gin-gonic/gin"
)

type CreateOrderRequest struct {
	ReferenceID   string  `json:"reference_id" binding:"required"`
	Amount        float64 `json:"amount" binding:"required,gt=0"`
	Currency      string  `json:"currency"`
	ExpiryMinutes int     `json:"expiry_minutes"`
}

type OrderHandler struct {
	db          *sql.DB
	cfg         *config.Config
	poller      *service.PollerService
	isSandbox   bool
	qrBaseDir   string
	qrURLPrefix string
}

func NewOrderHandler(db *sql.DB, cfg *config.Config, poller *service.PollerService, isSandbox bool) *OrderHandler {
	qrDir := "data/qr"
	qrPrefix := "/api/orders"
	if isSandbox {
		qrDir = "data/sandbox/qr"
		qrPrefix = "/api/sandbox/orders"
	}

	return &OrderHandler{
		db:          db,
		cfg:         cfg,
		poller:      poller,
		isSandbox:   isSandbox,
		qrBaseDir:   qrDir,
		qrURLPrefix: qrPrefix,
	}
}

// CreateOrder membuat tagihan baru
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	var req CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	if req.Amount < 0.01 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Nominal pembayaran minimal adalah 0.01 USDT (batas transfer minimum Binance Pay)",
		})
		return
	}

	appID := c.GetString("app_id")
	if appID == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "App ID missing"})
		return
	}

	currency := req.Currency
	if currency == "" {
		currency = h.cfg.DefaultCurrency
	}
	expiry := req.ExpiryMinutes
	if expiry <= 0 {
		expiry = h.cfg.DefaultExpiryMinutes
	}

	// Idempotency: Cek apakah sudah ada order PENDING aktif dengan reference_id yang sama untuk toko ini
	var existingID string
	checkErr := h.db.QueryRow(`
		SELECT id FROM orders 
		WHERE app_id = ? AND reference_id = ? AND status = 'PENDING' AND expires_at > ?
	`, appID, req.ReferenceID, time.Now().UnixMilli()).Scan(&existingID)
	if checkErr == nil && existingID != "" {
		existingOrder, qErr := h.queryOrderData(c, existingID)
		if qErr == nil {
			c.JSON(http.StatusOK, gin.H{
				"success": true,
				"data":    existingOrder,
			})
			return
		}
	}

	uniQR := database.GetSystemConfig(h.db, "BINANCE_UNI_QR_URL", h.cfg.BinanceUniQRURL)
	payID := database.GetSystemConfig(h.db, "BINANCE_PAY_ID", h.cfg.BinancePayID)
	accountName := database.GetSystemConfig(h.db, "BINANCE_ACCOUNT_NAME", h.cfg.BinanceAccountName)

	orderData, err := service.CreateOrder(
		h.db,
		appID,
		req.ReferenceID,
		currency,
		req.Amount,
		expiry,
		h.cfg.CooldownMinutes,
		uniQR,
		payID,
		accountName,
		h.qrBaseDir,
		h.qrURLPrefix,
		h.isSandbox,
	)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}

	orderData.QRURL = h.resolveQRURL(c, orderData.OrderID)

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"data":    orderData,
	})
}

// GetOrder mengambil status dan detail order
func (h *OrderHandler) GetOrder(c *gin.Context) {
	id := c.Param("id")

	orderData, err := h.queryOrderData(c, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Order tidak ditemukan"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    orderData,
	})
}

// GetQRCode menyajikan gambar PNG QR Code untuk tagihan (Publik)
func (h *OrderHandler) GetQRCode(c *gin.Context) {
	id := c.Param("id")

	var qrPath string
	err := h.db.QueryRow("SELECT qr_path FROM orders WHERE id = ?", id).Scan(&qrPath)
	if err != nil || qrPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "QR code tidak ditemukan"})
		return
	}

	if _, err := os.Stat(qrPath); os.IsNotExist(err) {
		uniQR := database.GetSystemConfig(h.db, "BINANCE_UNI_QR_URL", h.cfg.BinanceUniQRURL)
		payID := database.GetSystemConfig(h.db, "BINANCE_PAY_ID", h.cfg.BinancePayID)
		qrContent := uniQR
		if qrContent == "" {
			qrContent = "https://app.binance.com/uni-qr/pay_" + payID
		}
		if genErr := service.GenerateQRCode(qrContent, qrPath); genErr != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "File QR code tidak ditemukan"})
			return
		}
	}

	c.Header("Content-Type", "image/png")
	c.Header("Cache-Control", "public, max-age=86400")
	c.File(qrPath)
}

// CheckOrder menangani tombol "Saya Sudah Bayar"
func (h *OrderHandler) CheckOrder(c *gin.Context) {
	id := c.Param("id")

	var nextWaitSec int = 0
	if !h.isSandbox && h.poller != nil {
		var err error
		nextWaitSec, err = h.poller.CheckOrderNow(id)
		if err != nil {
			// Jika ada error koneksi Binance, tetap lanjutkan baca status SQLite saat ini
		}
	}

	orderData, err := h.queryOrderData(c, id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "Order tidak ditemukan"})
		return
	}

	resp := gin.H{
		"success": true,
		"data":    orderData,
	}
	if nextWaitSec > 0 {
		resp["next_check_in_seconds"] = nextWaitSec
	}

	c.JSON(http.StatusOK, resp)
}

// CancelOrder membatalkan pesanan
func (h *OrderHandler) CancelOrder(c *gin.Context) {
	id := c.Param("id")

	res, err := h.db.Exec("UPDATE orders SET status = 'CANCELLED' WHERE id = ? AND status = 'PENDING'", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
		return
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Order tidak dapat dibatalkan (sudah lunas atau expired)"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "message": "Order berhasil dibatalkan"})
}

func (h *OrderHandler) resolveQRURL(c *gin.Context, orderID string) string {
	scheme := "http"
	if c.Request.TLS != nil || c.GetHeader("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	host := c.Request.Host
	if fwdHost := c.GetHeader("X-Forwarded-Host"); fwdHost != "" {
		host = fwdHost
	}
	if host != "" {
		return fmt.Sprintf("%s://%s%s/%s/qr.png", scheme, host, h.qrURLPrefix, orderID)
	}
	return fmt.Sprintf("%s/%s/qr.png", h.qrURLPrefix, orderID)
}

func (h *OrderHandler) queryOrderData(c *gin.Context, orderID string) (*service.OrderData, error) {
	var (
		refID          string
		currency       string
		originalAmount float64
		uniqueCode     int
		totalAmount    float64
		totalAmountStr string
		status         string
		binanceTxID    sql.NullString
		payerName      sql.NullString
		paidAtMillis   int64
		expiresAt      int64
	)

	err := h.db.QueryRow(`
		SELECT reference_id, currency, original_amount, unique_code, total_amount, total_amount_str,
		       status, binance_tx_id, payer_name, paid_at, expires_at
		FROM orders WHERE id = ?
	`, orderID).Scan(
		&refID, &currency, &originalAmount, &uniqueCode, &totalAmount, &totalAmountStr,
		&status, &binanceTxID, &payerName, &paidAtMillis, &expiresAt,
	)
	if err != nil {
		return nil, err
	}

	now := time.Now().UnixMilli()
	expiresInSec := (expiresAt - now) / 1000
	if expiresInSec <= 0 {
		expiresInSec = 0
		if status == "PENDING" {
			status = "EXPIRED"
			_, _ = h.db.Exec("UPDATE orders SET status = 'EXPIRED' WHERE id = ? AND status = 'PENDING'", orderID)
		}
	}

	paidAtStr := ""
	if paidAtMillis > 0 {
		paidAtStr = time.UnixMilli(paidAtMillis).UTC().Format(time.RFC3339)
	}

	uniQR := database.GetSystemConfig(h.db, "BINANCE_UNI_QR_URL", h.cfg.BinanceUniQRURL)
	payID := database.GetSystemConfig(h.db, "BINANCE_PAY_ID", h.cfg.BinancePayID)
	accountName := database.GetSystemConfig(h.db, "BINANCE_ACCOUNT_NAME", h.cfg.BinanceAccountName)

	return &service.OrderData{
		OrderID:            orderID,
		ReferenceID:        refID,
		Currency:           currency,
		OriginalAmount:     originalAmount,
		UniqueCode:         uniqueCode,
		TotalAmount:        totalAmount,
		TotalAmountStr:     totalAmountStr,
		BinancePayID:       payID,
		BinanceAccountName: accountName,
		BinanceUniQRURL:    uniQR,
		Status:             status,
		BinanceTxID:        binanceTxID.String,
		PayerName:          payerName.String,
		PaidAt:             paidAtStr,
		QRURL:              h.resolveQRURL(c, orderID),
		ExpiresAt:          time.UnixMilli(expiresAt).UTC().Format(time.RFC3339),
		ExpiresInSeconds:   expiresInSec,
	}, nil
}
