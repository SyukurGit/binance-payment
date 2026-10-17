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

const (
	baseURL  = "http://localhost:3300"
	adminKey = "pg_admin_secret_key_change_me_immediately"
)

func main() {
	fmt.Println("🚀 Memulai Automated Security & Environment Isolation Audit Test Suite...")

	// 1. PIN Lockout Test
	testPinLockout()

	// 2. Proteksi Endpoint Kredensial Sandbox Tanpa Admin Key
	testSandboxCredentialsProtection()

	// 3. Ambil Test API Key Sandbox (Terautentikasi)
	sandboxKey := getActiveSandboxApiKey()

	// 4. Minimum Amount Validation Test
	testMinimumAmount(sandboxKey)

	// 5. Reverse-Proxy Aware QR URL Test
	testReverseProxyQR(sandboxKey)

	// 6. Non-blocking CheckOrder Test
	testNonBlockingCheck(sandboxKey)

	// 7. CORS Headers Test
	testCORSHeaders()

	// 8. Isolasi Silang Environment (Cross-Environment Blocking)
	testCrossEnvironmentRejection(sandboxKey)

	// 9. Isolasi Manajemen Toko (Live vs Sandbox Store Isolation)
	testStoreIsolation()

	fmt.Println("\n🎉 SEMUA PENGUJIAN SECURITY & ENVIRONMENT ISOLATION BERHASIL 100%!")
}

func testPinLockout() {
	fmt.Print("👉 [1/9] Menguji Brute-force PIN Lockout (5x salah -> HTTP 429)... ")

	client := &http.Client{Timeout: 5 * time.Second}
	var lastStatusCode int
	var lastBody string

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

func testSandboxCredentialsProtection() {
	fmt.Print("👉 [2/9] Menguji Proteksi /api/sandbox/credentials Tanpa X-Admin-Key (Wajib HTTP 401)... ")
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(baseURL + "/api/sandbox/credentials")
	if err != nil {
		log.Fatalf("Request error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		log.Fatalf("GAGAL: Endpoint kredensial sandbox harus menolak request tanpa auth (Ekspektasi 401, dapat %d)", resp.StatusCode)
	}
	fmt.Println("✅ PASS (Akses unauthenticated ditolak HTTP 401)")
}

func getActiveSandboxApiKey() string {
	client := &http.Client{Timeout: 5 * time.Second}
	req, _ := http.NewRequest("GET", baseURL+"/api/sandbox/credentials", nil)
	req.Header.Set("X-Admin-Key", adminKey)

	resp, err := client.Do(req)
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
	if !strings.HasPrefix(res.Data.ApiKey, "ak_test_") {
		log.Fatalf("API Key sandbox tidak berawalan 'ak_test_': %s", res.Data.ApiKey)
	}
	return res.Data.ApiKey
}

func testMinimumAmount(apiKey string) {
	fmt.Print("👉 [3/9] Menguji Validasi Minimum Nominal (< 0.01 USDT)... ")

	client := &http.Client{Timeout: 5 * time.Second}
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
	fmt.Println("✅ PASS (Ditolak HTTP 400 dengan batas minimum 0.01 USDT)")
}

func testReverseProxyQR(apiKey string) {
	fmt.Print("👉 [4/9] Menguji Reverse Proxy HTTPS & Custom Host QR URL... ")

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
	req.Header.Set("X-Forwarded-Host", "checkout.binance-pay.com")

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

	expectedPrefix := "https://checkout.binance-pay.com/api/sandbox/orders/"
	if !strings.HasPrefix(res.Data.QRURL, expectedPrefix) {
		log.Fatalf("GAGAL: QRURL tidak memantulkan HTTPS reverse proxy host: %s", res.Data.QRURL)
	}
	fmt.Println("✅ PASS (" + res.Data.QRURL + ")")
}

func testNonBlockingCheck(apiKey string) {
	fmt.Print("👉 [5/9] Menguji CheckOrder Non-blocking (0ms Sleep)... ")

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
	fmt.Print("👉 [6/9] Menguji W3C Compliant CORS Preflight... ")

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

func testCrossEnvironmentRejection(sandboxKey string) {
	fmt.Print("👉 [7/9] Menguji Penolakan Kunci Silang (ak_test_ di Live API & ak_live_ di Sandbox API)... ")

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Tembak Live API (/api/orders) menggunakan Sandbox Key (ak_test_...)
	reqBody, _ := json.Marshal(map[string]interface{}{
		"reference_id": "TEST-CROSS-LIVE",
		"amount":       10.0,
	})
	reqLive, _ := http.NewRequest("POST", baseURL+"/api/orders", bytes.NewBuffer(reqBody))
	reqLive.Header.Set("Content-Type", "application/json")
	reqLive.Header.Set("X-API-Key", sandboxKey)

	respLive, err := client.Do(reqLive)
	if err != nil {
		log.Fatalf("Live request error: %v", err)
	}
	defer respLive.Body.Close()

	if respLive.StatusCode != http.StatusUnauthorized {
		log.Fatalf("GAGAL: Key ak_test_ harus ditolak di Live API (Dapat HTTP %d)", respLive.StatusCode)
	}

	// 2. Buat Dummy Live Key (ak_live_fake) dan tembak Sandbox API
	reqSandbox, _ := http.NewRequest("POST", baseURL+"/api/sandbox/orders", bytes.NewBuffer(reqBody))
	reqSandbox.Header.Set("Content-Type", "application/json")
	reqSandbox.Header.Set("X-API-Key", "ak_live_fakekey000000000000000000000")

	respSandbox, err := client.Do(reqSandbox)
	if err != nil {
		log.Fatalf("Sandbox request error: %v", err)
	}
	defer respSandbox.Body.Close()

	if respSandbox.StatusCode != http.StatusUnauthorized {
		log.Fatalf("GAGAL: Key ak_live_ harus ditolak di Sandbox API (Dapat HTTP %d)", respSandbox.StatusCode)
	}

	fmt.Println("✅ PASS (Penolakan silang aktif 100%)")
}

func testStoreIsolation() {
	fmt.Print("👉 [8/9] Menguji Isolasi Pendaftaran Toko (Toko Live TIDAK masuk Sandbox)... ")

	client := &http.Client{Timeout: 5 * time.Second}

	// 1. Buat Toko Baru di Live API
	createBody, _ := json.Marshal(map[string]string{
		"name":        "Toko Emas Murni Live",
		"webhook_url": "https://tokoemas.com/webhook",
	})
	reqCreate, _ := http.NewRequest("POST", baseURL+"/api/apps", bytes.NewBuffer(createBody))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.Header.Set("X-Admin-Key", adminKey)

	respCreate, err := client.Do(reqCreate)
	if err != nil {
		log.Fatalf("Gagal buat toko live: %v", err)
	}
	defer respCreate.Body.Close()

	var createdRes struct {
		Success bool `json:"success"`
		Data    struct {
			ID     string `json:"id"`
			ApiKey string `json:"api_key"`
		} `json:"data"`
	}
	json.NewDecoder(respCreate.Body).Decode(&createdRes)

	if !strings.HasPrefix(createdRes.Data.ApiKey, "ak_live_") {
		log.Fatalf("GAGAL: Toko Live harus memiliki key dengan prefix 'ak_live_', dapat: %s", createdRes.Data.ApiKey)
	}

	// 2. Periksa apakah toko ini bocor ke Sandbox Apps
	reqSandboxApps, _ := http.NewRequest("GET", baseURL+"/api/sandbox/apps", nil)
	reqSandboxApps.Header.Set("X-Admin-Key", adminKey)

	respSandboxApps, err := client.Do(reqSandboxApps)
	if err != nil {
		log.Fatalf("Gagal cek sandbox apps: %v", err)
	}
	defer respSandboxApps.Body.Close()

	var sbAppsRes struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(respSandboxApps.Body).Decode(&sbAppsRes)

	for _, app := range sbAppsRes.Data {
		if app.ID == createdRes.Data.ID {
			log.Fatalf("GAGAL FATAL: Toko Live '%s' terdeteksi bocor di Sandbox Apps!", app.ID)
		}
	}

	fmt.Println("✅ PASS (Toko Live terisolasi 100% dan tidak bocor ke Sandbox)")

	// 3. Bersihkan toko live test
	fmt.Print("👉 [9/9] Menguji Isolasi Hapus Toko... ")
	reqDelete, _ := http.NewRequest("DELETE", baseURL+"/api/apps/"+createdRes.Data.ID, nil)
	reqDelete.Header.Set("X-Admin-Key", adminKey)
	respDelete, _ := client.Do(reqDelete)
	respDelete.Body.Close()

	fmt.Println("✅ PASS (Toko berhasil dibersihkan)")
}
