package middleware

import (
	"crypto/subtle"
	"database/sql"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type pinAttempt struct {
	count       int
	firstFailed time.Time
	lockedUntil time.Time
}

var (
	pinAttemptsMu sync.Mutex
	pinAttempts   = make(map[string]*pinAttempt)
)

// CheckPinRateLimit memeriksa dan membatasi percobaan PIN per IP
func CheckPinRateLimit(ip string) (bool, int) {
	pinAttemptsMu.Lock()
	defer pinAttemptsMu.Unlock()

	now := time.Now()
	att, exists := pinAttempts[ip]
	if !exists {
		return true, 0
	}

	// Jika sedang terkunci
	if now.Before(att.lockedUntil) {
		remainingSec := int(att.lockedUntil.Sub(now).Seconds())
		return false, remainingSec
	}

	// Reset counter jika jeda 1 menit sudah lewat
	if now.Sub(att.firstFailed) > 1*time.Minute {
		delete(pinAttempts, ip)
		return true, 0
	}

	return true, 0
}

// RecordPinFailure mencatat kegagalan PIN dan mengunci IP jika melebihi 5 kali
func RecordPinFailure(ip string) int {
	pinAttemptsMu.Lock()
	defer pinAttemptsMu.Unlock()

	now := time.Now()
	att, exists := pinAttempts[ip]
	if !exists {
		pinAttempts[ip] = &pinAttempt{
			count:       1,
			firstFailed: now,
		}
		return 0
	}

	att.count++
	if att.count >= 5 {
		att.lockedUntil = now.Add(5 * time.Minute)
		return int(5 * 60)
	}

	return 0
}

// ResetPinAttempts mereset catatan gagal setelah PIN benar
func ResetPinAttempts(ip string) {
	pinAttemptsMu.Lock()
	defer pinAttemptsMu.Unlock()
	delete(pinAttempts, ip)
}

// SecureCompareString membandingkan string dengan konstan waktu (constant-time) untuk mencegah timing attack
func SecureCompareString(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// RequireAdmin memvalidasi X-Admin-Key atau Bearer Token
func RequireAdmin(adminKey string) gin.HandlerFunc {
	adminKey = strings.TrimSpace(adminKey)
	return func(c *gin.Context) {
		key := strings.TrimSpace(c.GetHeader("X-Admin-Key"))
		if key == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				key = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			}
		}

		if key == "" || key != adminKey {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "Unauthorized: Invalid or missing X-Admin-Key",
			})
			return
		}

		c.Next()
	}
}

// RequireAPIKey memvalidasi X-API-Key toko online terhadap database
func RequireAPIKey(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := strings.TrimSpace(c.GetHeader("X-API-Key"))
		if apiKey == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				apiKey = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			}
		}

		if apiKey == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "Unauthorized: Missing X-API-Key header",
			})
			return
		}

		var appID string
		var appName string
		var webhookURL string
		err := db.QueryRow("SELECT id, name, webhook_url FROM apps WHERE api_key = ?", apiKey).Scan(&appID, &appName, &webhookURL)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"error":   "Unauthorized: Invalid X-API-Key",
			})
			return
		}

		c.Set("app_id", appID)
		c.Set("app_name", appName)
		c.Set("app_webhook_url", webhookURL)
		c.Set("app_api_key", apiKey)

		c.Next()
	}
}

// OptionalAPIKey memvalidasi X-API-Key jika disertakan, namun tetap mengizinkan request jika tidak ada API key (untuk query status publik via Order ID)
func OptionalAPIKey(db *sql.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		apiKey := strings.TrimSpace(c.GetHeader("X-API-Key"))
		if apiKey == "" {
			authHeader := c.GetHeader("Authorization")
			if strings.HasPrefix(authHeader, "Bearer ") {
				apiKey = strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
			}
		}

		if apiKey != "" {
			var appID string
			var appName string
			var webhookURL string
			err := db.QueryRow("SELECT id, name, webhook_url FROM apps WHERE api_key = ?", apiKey).Scan(&appID, &appName, &webhookURL)
			if err == nil {
				c.Set("app_id", appID)
				c.Set("app_name", appName)
				c.Set("app_webhook_url", webhookURL)
				c.Set("app_api_key", apiKey)
			}
		}

		c.Next()
	}
}
