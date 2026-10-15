package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type BinancePayerInfo struct {
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	BinanceID interface{} `json:"binanceId"`
	AccountID interface{} `json:"accountId"`
}

func (p BinancePayerInfo) GetBinanceID() string {
	if p.BinanceID == nil {
		return ""
	}
	return fmt.Sprintf("%v", p.BinanceID)
}

type BinancePayTransaction struct {
	OrderType       string           `json:"orderType"`
	TransactionID   interface{}      `json:"transactionId"`
	TransactionTime interface{}      `json:"transactionTime"`
	Amount          interface{}      `json:"amount"`
	Currency        string           `json:"currency"`
	FundsDetail     []interface{}    `json:"fundsDetail"`
	PayerInfo       BinancePayerInfo `json:"payerInfo"`
}

func (tx BinancePayTransaction) GetTransactionID() string {
	if tx.TransactionID == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", tx.TransactionID))
}

func (tx BinancePayTransaction) GetTransactionTime() int64 {
	if tx.TransactionTime == nil {
		return 0
	}
	switch v := tx.TransactionTime.(type) {
	case float64:
		return int64(v)
	case int64:
		return v
	case int:
		return int64(v)
	case string:
		if parsed, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return parsed
		}
	default:
		if parsed, err := strconv.ParseInt(fmt.Sprintf("%v", v), 10, 64); err == nil {
			return parsed
		}
	}
	return 0
}

func (tx BinancePayTransaction) GetAmountStr() string {
	if tx.Amount == nil {
		return "0"
	}
	return strings.TrimSpace(fmt.Sprintf("%v", tx.Amount))
}

func (tx BinancePayTransaction) GetAmountFloat() (float64, error) {
	str := tx.GetAmountStr()
	return strconv.ParseFloat(str, 64)
}

type BinancePayResponse struct {
	Code    interface{}             `json:"code"`
	Message string                  `json:"message"`
	Data    []BinancePayTransaction `json:"data"`
	Success bool                    `json:"success"`
}

func (r BinancePayResponse) GetCode() string {
	if r.Code == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprintf("%v", r.Code))
}

var (
	httpClient = &http.Client{
		Timeout: 10 * time.Second,
	}
	// serverTimeOffset menyimpan selisih milidetik antara jam lokal dengan jam server Binance
	serverTimeOffset int64
	// rateLimitBackoffUntil mencatat waktu jeda jika terkena HTTP 429
	rateLimitBackoffUntil int64
)

// ComputeHmac256 menghitung signature HMAC-SHA256
func ComputeHmac256(message string, secret string) string {
	h := hmac.New(sha256.New, []byte(strings.TrimSpace(secret)))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

// SyncServerTime menyinkronkan jam lokal dengan jam server Binance untuk mencegah error clock drift (-1021)
func SyncServerTime(baseURL string) (int64, error) {
	timeURL := strings.TrimRight(baseURL, "/") + "/api/v3/time"
	resp, err := httpClient.Get(timeURL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var res struct {
		ServerTime int64 `json:"serverTime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return 0, err
	}

	if res.ServerTime > 0 {
		diff := res.ServerTime - time.Now().UnixMilli()
		atomic.StoreInt64(&serverTimeOffset, diff)
		return diff, nil
	}
	return 0, nil
}

// FetchPayTransactions mengambil data mutasi masuk dari GET /sapi/v1/pay/transactions
func FetchPayTransactions(baseURL, apiKey, secretKey string, startTime int64) ([]BinancePayTransaction, error) {
	apiKey = strings.TrimSpace(apiKey)
	secretKey = strings.TrimSpace(secretKey)

	if apiKey == "" || secretKey == "" {
		return nil, fmt.Errorf("binance api_key atau secret_key belum dikonfigurasi")
	}

	// Cek apakah masih dalam masa backoff rate-limit 429
	backoffUntil := atomic.LoadInt64(&rateLimitBackoffUntil)
	if nowMilli := time.Now().UnixMilli(); nowMilli < backoffUntil {
		waitSec := (backoffUntil - nowMilli) / 1000
		return nil, fmt.Errorf("binance rate-limit cooling down (tunggu %d detik)", waitSec)
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/sapi/v1/pay/transactions"

	now := time.Now().UnixMilli() + atomic.LoadInt64(&serverTimeOffset)

	// Pastikan startTime tidak melanggar aturan Binance (maksimal 85 hari ke belakang & tidak melebihi now)
	if startTime <= 0 || startTime > now {
		startTime = now - (24 * 60 * 60 * 1000)
	}
	maxLookback := now - (85 * 24 * 60 * 60 * 1000)
	if startTime < maxLookback {
		startTime = maxLookback
	}

	params := url.Values{}
	params.Set("startTime", strconv.FormatInt(startTime, 10))
	params.Set("limit", "100")
	params.Set("recvWindow", "60000") // 60 detik batas toleransi clock skew maksimal
	params.Set("timestamp", strconv.FormatInt(now, 10))

	queryString := params.Encode()
	signature := ComputeHmac256(queryString, secretKey)
	fullURL := fmt.Sprintf("%s?%s&signature=%s", endpoint, queryString, signature)

	req, err := http.NewRequest(http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gagal membuat request binance: %w", err)
	}

	req.Header.Set("X-MBX-APIKEY", apiKey)
	req.Header.Set("User-Agent", "PaymentG-Binance-Engine/1.0")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request binance network error: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca respons binance: %w", err)
	}

	// Deteksi otomatis jika terjadi rate limit (HTTP 429 atau 418)
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == 418 {
		atomic.StoreInt64(&rateLimitBackoffUntil, time.Now().UnixMilli()+60000)
		return nil, fmt.Errorf("binance rate limit tercapai (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	// Deteksi otomatis jika terjadi timestamp clock skew error (-1021)
	if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(bodyBytes), "-1021") {
		// Sinkronkan jam dengan Binance dan coba ulang satu kali
		if _, syncErr := SyncServerTime(baseURL); syncErr == nil {
			return FetchPayTransactions(baseURL, apiKey, secretKey, startTime)
		}
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("binance api error (HTTP %d): %s", resp.StatusCode, string(bodyBytes))
	}

	var result BinancePayResponse
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		return nil, fmt.Errorf("gagal unmarshal json binance: %w (raw: %s)", err, string(bodyBytes))
	}

	// Cek kode error logis Binance jika ada
	code := result.GetCode()
	if code != "000000" && code != "0" && code != "" {
		return nil, fmt.Errorf("binance pay error (%s): %s", code, result.Message)
	}

	// Filter hanya transaksi masuk (abaikan amount negatif '-')
	var incoming []BinancePayTransaction
	for _, tx := range result.Data {
		amountStr := tx.GetAmountStr()
		if strings.HasPrefix(amountStr, "-") {
			continue
		}
		incoming = append(incoming, tx)
	}

	return incoming, nil
}
