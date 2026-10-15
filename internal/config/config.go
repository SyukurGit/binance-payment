package config

import (
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

type Config struct {
	Port                       string
	GinMode                    string
	AdminPIN                   string
	AdminKey                   string
	BinanceAPIKey              string
	BinanceSecretKey           string
	BinanceBaseURL             string
	BinancePayID               string
	BinanceAccountName         string
	BinanceUniQRURL            string
	DefaultCurrency            string
	DefaultExpiryMinutes       int
	CooldownMinutes            int
	MinBinancePollIntervalSec  int
	BackgroundSweepIntervalSec int
}

func LoadConfig() *Config {
	_ = godotenv.Load(".env")

	cfg := &Config{
		Port:                       getEnv("PORT", "3300"),
		GinMode:                    getEnv("GIN_MODE", "release"),
		AdminPIN:                   getEnv("ADMIN_PIN", "220700"),
		AdminKey:                   getEnv("ADMIN_KEY", "pg_admin_secret_key_change_me_immediately"),
		BinanceAPIKey:              getEnv("BINANCE_API_KEY", ""),
		BinanceSecretKey:           getEnv("BINANCE_SECRET_KEY", ""),
		BinanceBaseURL:             getEnv("BINANCE_BASE_URL", "https://api.binance.com"),
		BinancePayID:               getEnv("BINANCE_PAY_ID", "81726354"),
		BinanceAccountName:         getEnv("BINANCE_ACCOUNT_NAME", "StoreOfficial"),
		BinanceUniQRURL:            getEnv("BINANCE_UNI_QR_URL", "https://app.binance.com/uni-qr/c2c_example"),
		DefaultCurrency:            getEnv("DEFAULT_CURRENCY", "USDT"),
		DefaultExpiryMinutes:       getEnvAsInt("DEFAULT_EXPIRY_MINUTES", 15),
		CooldownMinutes:            getEnvAsInt("COOLDOWN_MINUTES", 60),
		MinBinancePollIntervalSec:  getEnvAsInt("MIN_BINANCE_POLL_INTERVAL_SEC", 16),
		BackgroundSweepIntervalSec: getEnvAsInt("BACKGROUND_SWEEP_INTERVAL_SEC", 20),
	}

	return cfg
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvAsInt(key string, defaultVal int) int {
	valStr := getEnv(key, "")
	if val, err := strconv.Atoi(valStr); err == nil {
		return val
	}
	return defaultVal
}
