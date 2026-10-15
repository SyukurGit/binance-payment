package middleware

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

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
