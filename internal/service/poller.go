package service

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"paymentg-binance/internal/config"
	"paymentg-binance/internal/database"
)

type PollerService struct {
	pollMu          sync.Mutex
	stateMu         sync.RWMutex
	db              *sql.DB
	cfg             *config.Config
	lastBinanceCall time.Time
	lastBinanceErr  string
	minInterval     time.Duration
}

func NewPollerService(db *sql.DB, cfg *config.Config) *PollerService {
	intervalSec := cfg.MinBinancePollIntervalSec
	if intervalSec < 15 {
		intervalSec = 16
	}

	return &PollerService{
		db:          db,
		cfg:         cfg,
		minInterval: time.Duration(intervalSec) * time.Second,
	}
}

// StartBackgroundWorkers menjalankan pembersih expired dan background poller
func (p *PollerService) StartBackgroundWorkers() {
	// Worker 1: Ticker 5 detik untuk menandai order expired
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			p.expirePendingOrders()
		}
	}()

	// Worker 2: Ticker background sweep ke Binance (default 20 detik)
	sweepInterval := p.cfg.BackgroundSweepIntervalSec
	if sweepInterval < 15 {
		sweepInterval = 20
	}

	go func() {
		ticker := time.NewTicker(time.Duration(sweepInterval) * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			if p.shouldBackgroundPoll() {
				_, _ = p.ExecutePoll()
			}
		}
	}()
}

// expirePendingOrders menandai order yang melewati expires_at menjadi EXPIRED
func (p *PollerService) expirePendingOrders() {
	now := time.Now().UnixMilli()
	_, _ = p.db.Exec(`UPDATE orders SET status = 'EXPIRED' WHERE status = 'PENDING' AND expires_at < ?`, now)
}

// shouldBackgroundPoll memeriksa apakah ada order aktif yang butuh dicek
func (p *PollerService) shouldBackgroundPoll() bool {
	now := time.Now().UnixMilli()

	// Cek Aturan 1a: Ada order PENDING yang umurnya sudah lebih dari 30 detik
	var countPending int
	_ = p.db.QueryRow(`
		SELECT COUNT(1) FROM orders 
		WHERE status = 'PENDING' AND (? - created_at) >= 30000
	`, now).Scan(&countPending)

	if countPending > 0 {
		return true
	}

	// Cek Aturan 1b: Ada order yang baru saja EXPIRED dalam 10 menit terakhir (tail-polling)
	var countExpiredRecent int
	_ = p.db.QueryRow(`
		SELECT COUNT(1) FROM orders 
		WHERE status = 'EXPIRED' AND (? - expires_at) <= 600000
	`, now).Scan(&countExpiredRecent)

	return countExpiredRecent > 0
}

// ExecutePoll mengeksekusi panggilan sinkron ke Binance API dengan proteksi mutex serial
func (p *PollerService) ExecutePoll() (int, error) {
	p.pollMu.Lock()
	defer p.pollMu.Unlock()

	p.stateMu.RLock()
	lastCall := p.lastBinanceCall
	p.stateMu.RUnlock()

	// Cegah panggilan ganda jika baru saja dipanggil kurang dari 5 detik lalu oleh goroutine lain
	if !lastCall.IsZero() && time.Since(lastCall) < 5*time.Second {
		return 0, nil
	}

	apiKey := database.GetSystemConfig(p.db, "BINANCE_API_KEY", p.cfg.BinanceAPIKey)
	secretKey := database.GetSystemConfig(p.db, "BINANCE_SECRET_KEY", p.cfg.BinanceSecretKey)
	baseURL := database.GetSystemConfig(p.db, "BINANCE_BASE_URL", p.cfg.BinanceBaseURL)

	if apiKey == "" || secretKey == "" {
		errMsg := "Binance API Key / Secret Key belum dikonfigurasi"
		p.stateMu.Lock()
		p.lastBinanceErr = errMsg
		p.stateMu.Unlock()
		return 0, fmt.Errorf("%s", errMsg)
	}

	// Cari startTime: MIN(created_at) - 60000 dari order aktif atau 24 jam terakhir
	now := time.Now().UnixMilli()
	var minCreatedAt sql.NullInt64
	_ = p.db.QueryRow(`
		SELECT MIN(created_at) FROM orders 
		WHERE status = 'PENDING' OR (status = 'EXPIRED' AND (? - expires_at) <= 600000)
	`, now).Scan(&minCreatedAt)

	var startTime int64
	if minCreatedAt.Valid && minCreatedAt.Int64 > 0 {
		startTime = minCreatedAt.Int64 - 60000
	} else {
		startTime = now - (24 * 60 * 60 * 1000)
	}

	txs, err := FetchPayTransactions(baseURL, apiKey, secretKey, startTime)

	p.stateMu.Lock()
	p.lastBinanceCall = time.Now()
	if err != nil {
		p.lastBinanceErr = err.Error()
	} else {
		p.lastBinanceErr = ""
	}
	p.stateMu.Unlock()

	if err != nil {
		log.Printf("[Poller] Binance API call gagal: %v", err)
		return 0, err
	}

	matched, matchErr := MatchIncomingTransactions(p.db, txs)
	if matchErr != nil {
		log.Printf("[Poller] Pencocokan transaksi error: %v", matchErr)
	}

	return matched, nil
}

// CheckOrderNow menangani rate-gate on demand secara non-blocking saat tombol 'Saya Sudah Bayar' diklik
func (p *PollerService) CheckOrderNow(orderID string) (int, error) {
	// Cek status saat ini di SQLite
	var status string
	var expiresAt int64
	err := p.db.QueryRow("SELECT status, expires_at FROM orders WHERE id = ?", orderID).Scan(&status, &expiresAt)
	if err != nil {
		return 0, fmt.Errorf("order tidak ditemukan")
	}

	// Jika sudah lunas, langsung return 0 detik
	if status == "PAID" {
		return 0, nil
	}

	// Jika order sudah dibatalkan atau sudah expired lebih dari 10 menit, jangan poll ke Binance
	now := time.Now().UnixMilli()
	if status == "CANCELLED" || (status == "EXPIRED" && now-expiresAt > 600000) {
		return 0, fmt.Errorf("order berstatus %s dan sudah tidak aktif", status)
	}

	p.stateMu.RLock()
	lastCall := p.lastBinanceCall
	minInterval := p.minInterval
	p.stateMu.RUnlock()

	elapsed := time.Since(lastCall)
	if elapsed >= minInterval {
		_, err := p.ExecutePoll()
		return 0, err
	}

	// Jika rate limit Binance belum lewat, hitung sisa detik dan kembalikan langsung TANPA sleep
	remaining := minInterval - elapsed
	waitSec := int(remaining.Seconds())
	if waitSec <= 0 {
		waitSec = 1
	}

	return waitSec, nil
}

func (p *PollerService) GetStatus() (time.Time, string) {
	p.stateMu.RLock()
	defer p.stateMu.RUnlock()
	return p.lastBinanceCall, p.lastBinanceErr
}
