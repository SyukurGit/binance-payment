package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const baseURL = "http://localhost:3300"

func main() {
	fmt.Println("🚀 Memulai Automated Pre-Live Audit Test Suite...")

	// 1. PIN Lockout Test
	testPinLockout()

	// 2. Minimum Amount Validation Test
	apiKey := getActiveApiKey()
	testMinimumAmount(apiKey)

	// 3. Reverse-Proxy Aware QR URL Test
	testReverseProxyQR(apiKey)

	// 4. Non-blocking CheckOrder Test
	testNonBlockingCheck(apiKey)

	// 5. CORS Headers Test
	testCORSHeaders()

	fmt.Println("\n🎉 SEMUA PENGUJIAN PRE-LIVE SECURITY & LOGIC BERHASIL 100%!")
}

func testPinLockout() {
	fmt.Print("👉 [1/5] Menguji Brute-force PIN Lockout (5x salah -> HTTP 429)... ")

	// Reset IP rate limiter or use fresh calls
	client := &http.Client{Timeout: 5 * time.Second}

	var lastStatusCode int
	var lastBody string

	// Kirim 6x PIN salah
	for i := 1; i <= 6; i++ {
		payload, _ := json.Marshal(map[string]string{"pin": "9999"})
		resp, err := client.Post(baseURL+"/api/auth/pin", "application/json", bytes.NewBuffer(payload))
		if err != nil {
			log.Fatalf("Gagal POST /api/auth/pin: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		lastStatusCode = resp.StatusCode
		lastBody = string(body)
	}

	if lastStatusCode != http.StatusTooManyRequests {
		log.Fatalf("GAGAL: Ekspektasi HTTP 429 setelah 6x salah, dapat HTTP %d: %s", lastStatusCode, lastBody)
	}
	if !strings.Contains(lastBody, "dikunci") {
		log.Fatalf("GAGAL: Pesan lockout tidak sesuai: %s", lastBody)
	}
	fmt.Println("✅ PASS (HTTP 429 Lockout aktif)")
}

func getActiveApiKey() string {
	resp, err := http.Get(baseURL + "/api/sandbox/credentials")
	if err != nil {
		log.Fatalf("Gagal ambil kredensial sandbox: %v", err)
	}
	defer resp.Body.Close()
	var res struct {
		Success bool `json:"success"`
		Data    struct {
			ApiKey string `json:"api_key"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&res)
	if res.Data.ApiKey == "" {
		log.Fatalf("API Key sandbox kosong")
	}
	return res.Data.ApiKey
}

func testMinimumAmount(apiKey string) {
	fmt.Print("👉 [2/5] Menguji Validasi Minimum Nominal (< 0.01 USDT)... ")

	client := &http.Client{Timeout: 5 * time.Second}

	// Coba amount = 0.005 USDT
	reqBody, _ := json.Marshal(map[string]interface{}{
		"reference_id": fmt.Sprintf("AUDIT-MIN-%d", time.Now().Unix()),
		"amount":       0.005,
		"currency":     "USDT",
	})
	req, _ := http.NewRequest("POST", baseURL+"/api/sandbox/orders", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Request error: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		log.Fatalf("GAGAL: 0.005 USDT harus ditolak HTTP 400, dapat %d: %s", resp.StatusCode, string(body))
	}
	if !strings.Contains(string(body), "0.01 USDT") {
		log.Fatalf("GAGAL: Pesan error tidak memuat batas 0.01 USDT: %s", string(body))
	}
	fmt.Println("✅ PASS (Ditolak HTTP 400 dengan pesan minimum 0.01 USDT)")
}

func testReverseProxyQR(apiKey string) {
	fmt.Print("👉 [3/5] Menguji Reverse Proxy HTTPS & Custom Host QR URL... ")

	client := &http.Client{Timeout: 5 * time.Second}

	reqBody, _ := json.Marshal(map[string]interface{}{
		"reference_id": fmt.Sprintf("AUDIT-PROXY-%d", time.Now().Unix()),
		"amount":       1.50,
		"currency":     "USDT",
	})
	req, _ := http.NewRequest("POST", baseURL+"/api/sandbox/orders", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("X-Forwarded-Host", "checkout.toko-kripto.com")

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("Request error: %v", err)
	}
	defer resp.Body.Close()

	var res struct {
		Success bool `json:"success"`
		Data    struct {
			OrderID string `json:"order_id"`
			QRURL   string `json:"qr_url"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&res)

	expectedPrefix := "https://checkout.toko-kripto.com/api/sandbox/orders/"
	if !strings.HasPrefix(res.Data.QRURL, expectedPrefix) {
		log.Fatalf("GAGAL: QRURL tidak memantulkan HTTPS reverse proxy host: %s", res.Data.QRURL)
	}
	fmt.Println("✅ PASS (" + res.Data.QRURL + ")")
}

func testNonBlockingCheck(apiKey string) {
	fmt.Print("👉 [4/5] Menguji CheckOrder Non-blocking (0ms Sleep)... ")

	// Buat order demo
	client := &http.Client{Timeout: 5 * time.Second}
	reqBody, _ := json.Marshal(map[string]interface{}{
		"reference_id": fmt.Sprintf("AUDIT-CHECK-%d", time.Now().Unix()),
		"amount":       2.00,
		"currency":     "USDT",
	})
	req, _ := http.NewRequest("POST", baseURL+"/api/sandbox/orders", bytes.NewBuffer(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	resp, _ := client.Do(req)
	var res struct {
		Data struct {
			OrderID string `json:"order_id"`
		} `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&res)
	resp.Body.Close()

	// Panggil /check 2 kali berturut-turut dan ukur latensi
	start := time.Now()
	checkReq, _ := http.NewRequest("POST", fmt.Sprintf("%s/api/sandbox/orders/%s/check", baseURL, res.Data.OrderID), nil)
	checkResp, err := client.Do(checkReq)
	duration := time.Since(start)
	if err != nil {
		log.Fatalf("CheckOrder error: %v", err)
	}
	checkResp.Body.Close()

	if duration > 500*time.Millisecond {
		log.Fatalf("GAGAL: CheckOrder memakan waktu terlalu lama (%v), terindikasi blocking sleep!", duration)
	}
	fmt.Printf("✅ PASS (Latensi: %v, non-blocking)\n", duration)
}

func testCORSHeaders() {
	fmt.Print("👉 [5/5] Menguji W3C Compliant CORS Preflight... ")

	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("OPTIONS", baseURL+"/api/orders", nil)
	req.Header.Set("Origin", "https://toko-online.com")
	req.Header.Set("Access-Control-Request-Method", "POST")

	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("OPTIONS request error: %v", err)
	}
	defer resp.Body.Close()

	corsOrigin := resp.Header.Get("Access-Control-Allow-Origin")
	if corsOrigin != "https://toko-online.com" && corsOrigin != "*" {
		log.Fatalf("GAGAL: Access-Control-Allow-Origin tidak valid: %s", corsOrigin)
	}
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		log.Fatalf("GAGAL: Preflight OPTIONS harus HTTP 204 atau 200, dapat %d", resp.StatusCode)
	}
	fmt.Println("✅ PASS (Allow-Origin: " + corsOrigin + ")")
}
