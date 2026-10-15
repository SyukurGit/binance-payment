# 🔌 Panduan Integrasi Resmi: PaymentG (Binance Pay Edition)
> **Untuk AI Coding Agent & Software Engineer**:  
> Dokumen ini adalah panduan integrasi mandiri (*self-contained*). Anda dapat langsung mengimplementasikan pembayaran kripto Binance Pay pada website toko/e-commerce Anda (PHP, Node.js, Python, Go, dll.) dari awal hingga selesai **hanya dengan mengikuti instruksi di file ini**.

---

## 📑 Daftar Isi
1. [Prinsip Kerja & Konfigurasi Lingkungan](#1-prinsip-kerja--konfigurasi-lingkungan)
2. [Kebutuhan Database di Sisi Toko](#2-kebutuhan-database-di-sisi-toko)
3. [Implementasi Backend Toko](#3-implementasi-backend-toko)
   - [A. Endpoint Buat Pesanan (Checkout)](#a-endpoint-buat-pesanan-checkout)
   - [B. Endpoint Penerima Webhook (Wajib Signature Verification)](#b-endpoint-penerima-webhook-wajib-signature-verification)
   - [C. Endpoint Cek Status On-Demand (Tombol "Saya Sudah Bayar")](#c-endpoint-cek-status-on-demand-tombol-saya-sudah-bayar)
4. [Implementasi Frontend Pembeli (UI Component Siap Pakai)](#4-implementasi-frontend-pembeli-ui-component-siap-pakai)
5. [Contoh Implementasi Webhook di Berbagai Bahasa](#5-contoh-implementasi-webhook-di-berbagai-bahasa)
   - [PHP / Laravel](#a-php--laravel)
   - [Node.js / Express](#b-nodejs--express)
   - [Python / FastAPI](#c-python--fastapi)
   - [Go (Gin)](#d-go-gin)
6. [Panduan Pengujian Menggunakan Sandbox Simulator](#6-panduan-pengujian-menggunakan-sandbox-simulator)
7. [Checklist Kelayakan Go-Live](#7-checklist-kelayakan-go-live)

---

## 1. Prinsip Kerja & Konfigurasi Lingkungan

PaymentG menggunakan konsep **Zero-Code Switching**. Anda tidak perlu mengubah satu baris kode pun saat beralih dari pengujian (*Sandbox*) ke produksi (*Live*). Cukup ubah konfigurasi `.env` toko Anda:

```env
# URL Gateway PaymentG (Ganti saat pindah mode)
# Mode Sandbox (Simulator): http://localhost:3300/api/sandbox
# Mode Live (Uang Nyata)   : http://localhost:3300/api (atau https://pay.domainanda.com/api)
PAYMENTG_BASE_URL=http://localhost:3300/api/sandbox

# API Key Toko Anda (Didapat saat mendaftarkan toko di /dashboard/ atau /sandbox/)
# Format: ak_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
PAYMENTG_API_KEY=ak_kXzMwSPLcSoa72rSCuaH4rnHpFZiV4rf
```

> [!IMPORTANT]
> API Key toko Anda otomatis aktif dan valid di **kedua mode sekaligus** (Live & Sandbox).

---

## 2. Kebutuhan Database di Sisi Toko

Pada database tabel pesanan toko Anda (misal `orders` atau `invoices`), pastikan Anda menyimpan kolom-kolom berikut:

| Nama Kolom | Tipe Data | Keterangan |
| :--- | :--- | :--- |
| `id` / `invoice_number` | VARCHAR(64) | ID Unik invoice toko Anda (misal `INV-1001`). Dikirimkan sebagai `reference_id`. |
| `paymentg_order_id` | VARCHAR(32) | ID pesanan yang dikembalikan PaymentG (misal `ord_2I8gk3xQvfcA`). |
| `crypto_amount_str` | VARCHAR(16) | Nominal kripto eksak (misal `"0.814"` atau `"10.033"`). **PENTING: Simpan sebagai string** agar desimal receh tidak terpotong. |
| `crypto_currency` | VARCHAR(10) | Default: `"USDT"`. |
| `status` | VARCHAR(20) | Status pesanan toko: `PENDING`, `PAID`, `EXPIRED`, `CANCELLED`. |
| `binance_tx_id` | VARCHAR(64) | ID transaksi Binance resmi yang didapat saat webhook lunas masuk. |

---

## 3. Implementasi Backend Toko

### A. Endpoint Buat Pesanan (Checkout)
Ketika pembeli menekan tombol **"Beli Sekarang"** atau **"Bayar via Binance Pay"**, Backend toko Anda menembak API PaymentG:

* **Endpoint:** `POST {PAYMENTG_BASE_URL}/orders`
* **Headers:**
  - `Content-Type: application/json`
  - `X-API-Key: {PAYMENTG_API_KEY}`
* **Request Payload:**
  ```json
  {
    "reference_id": "INV-2026-001",
    "amount": 0.8,
    "currency": "USDT",
    "expiry_minutes": 15
  }
  ```

* **Response dari PaymentG (`HTTP 201 Created`):**
  ```json
  {
    "success": true,
    "data": {
      "order_id": "ord_2I8gk3xQvfcA",
      "reference_id": "INV-2026-001",
      "currency": "USDT",
      "original_amount": 0.8,
      "unique_code": 14,
      "total_amount": 0.814,
      "total_amount_str": "0.814",
      "binance_pay_id": "857242044",
      "binance_account_name": "AirpayX7",
      "binance_uni_qr_url": "https://app.binance.com/uni-qr/...",
      "status": "PENDING",
      "qr_url": "/api/sandbox/orders/ord_2I8gk3xQvfcA/qr.png",
      "expires_at": "2026-10-15T13:15:00Z",
      "expires_in_seconds": 900
    }
  }
  ```

* **Tindakan Backend Toko:**
  1. Simpan `order_id` (`ord_2I8gk3xQvfcA`) dan `total_amount_str` (`"0.814"`) ke database pesanan Anda.
  2. Teruskan objek `data` di atas ke Frontend pembeli untuk dirender.

---

### B. Endpoint Penerima Webhook (Wajib Signature Verification)
Toko Anda **WAJIB** menyediakan satu endpoint HTTP POST publik (misal: `https://tokomu.com/api/payment/binance-webhook`) untuk menerima kabar pelunasan otomatis.

#### 1. Header yang Dikirimkan oleh PaymentG:
* `Content-Type: application/json`
* `X-Webhook-Event: payment.success`
* `X-Webhook-Signature: <string_hex_hmac_sha256>`
* `User-Agent: PaymentG-Binance-Webhook/1.0`

#### 2. Format Body Payload Webhook:
```json
{
  "event": "payment.success",
  "is_sandbox": false,
  "order_id": "ord_2I8gk3xQvfcA",
  "reference_id": "INV-2026-001",
  "currency": "USDT",
  "original_amount": 0.8,
  "unique_code": 14,
  "total_amount": 0.814,
  "total_amount_str": "0.814",
  "paid_at": "2026-10-15T12:45:10Z",
  "binance_tx_id": "M_P_71505104267788288"
}
```

#### 3. Logika Verifikasi Signature (Aturan Keamanan):
Hitung HMAC-SHA256 dari **Raw Body Request** menggunakan `PAYMENTG_API_KEY` toko Anda. Jika signature tidak cocok, tolak request dengan status `HTTP 401 Unauthorized`.

```text
expectedSignature = HMAC_SHA256(raw_request_body, secret_key=PAYMENTG_API_KEY)
```

#### 4. Tindakan Setelah Valid:
1. Cari pesanan di DB toko berdasarkan `reference_id`.
2. Jika pesanan sudah berstatus `PAID`, abaikan (idempotensi) dan tetap return `HTTP 200 OK`.
3. Jika belum lunas, ubah status pesanan menjadi `PAID`, simpan `binance_tx_id`, lalu kirim produk digital / aktifkan layanan pelanggan.
4. Kembalikan respon `HTTP 200 OK` (dengan JSON `{"status": "received"}`).

---

### C. Endpoint Cek Status On-Demand (Tombol "Saya Sudah Bayar")
Ketika pembeli mengeklik tombol *"Saya Sudah Bayar"*, Frontend dapat langsung mengecek status ke:

* **Endpoint:** `POST {PAYMENTG_BASE_URL}/orders/:order_id/check`
* **Response Jika Lunas (`HTTP 200 OK`):**
  ```json
  {
    "success": true,
    "data": {
      "order_id": "ord_2I8gk3xQvfcA",
      "status": "PAID",
      "binance_tx_id": "M_P_71505104267788288",
      "paid_at": "2026-10-15T12:45:10Z"
    }
  }
  ```
* **Response Jika Masih Pending:**
  ```json
  {
    "success": true,
    "data": {
      "order_id": "ord_2I8gk3xQvfcA",
      "status": "PENDING"
    },
    "next_check_in_seconds": 12
  }
  ```

---

## 4. Implementasi Frontend Pembeli (UI Component Siap Pakai)

Berikut adalah komponen HTML + Tailwind CSS + Vanilla JS siap pakai yang bisa langsung dipasang pada halaman checkout toko Anda:

```html
<!-- Modal / Box Pembayaran Binance Pay -->
<div id="binancePaymentBox" class="max-w-md mx-auto bg-[#181A20] text-[#EAECEF] border border-[#2B313A] rounded-2xl p-6 shadow-2xl font-sans">
    
    <!-- Header -->
    <div class="flex items-center justify-between border-b border-[#2B313A] pb-3 mb-4">
        <div class="flex items-center gap-2">
            <span class="w-7 h-7 bg-[#F3BA2F] text-black font-bold flex items-center justify-center rounded-lg text-xs">⚡</span>
            <h3 class="font-bold text-white text-sm">Pembayaran Binance Pay</h3>
        </div>
        <div class="text-xs text-amber-400 font-mono font-semibold" id="timerDisplay">15:00</div>
    </div>

    <!-- QR Code -->
    <div class="text-center mb-4">
        <div class="bg-white p-3 rounded-xl inline-block shadow">
            <img id="binanceQrImg" src="" alt="Scan QR Binance" class="w-44 h-44 mx-auto">
        </div>
        <p class="text-[11px] text-[#848E9C] mt-1.5">Scan langsung menggunakan Aplikasi Binance</p>
    </div>

    <!-- Kotak Jumlah Wajib Ditransfer (Kritis) -->
    <div class="bg-[#0B0E11] p-3.5 rounded-xl border border-amber-500/30 mb-3 text-center">
        <div class="text-[11px] text-[#848E9C]">TOTAL TRANSFER PERSIS:</div>
        <div class="text-2xl font-black text-[#F3BA2F] font-mono my-0.5 tracking-wider" id="totalAmountText">0.000 USDT</div>
        <button onclick="copyToClipboard('totalAmountText')" class="mt-1 text-[11px] bg-[#181A20] hover:bg-[#2B313A] text-white px-3 py-1 rounded-lg border border-[#2B313A] transition">
            📋 Salin Nominal Persis
        </button>
        <p class="text-[10px] text-amber-400/90 mt-1.5 leading-tight">⚠️ Wajib transfer nominal persis sampai digit terakhir agar otomatis lunas!</p>
    </div>

    <!-- Alternatif Pay ID Manual -->
    <div class="bg-[#0B0E11] p-3 rounded-xl border border-[#2B313A] mb-4 text-xs text-center">
        <span class="text-[#848E9C]">Atau kirim ke Pay ID: </span>
        <strong class="text-white font-mono select-all" id="binancePayIdText">857242044</strong>
        <span class="text-[#848E9C]" id="binanceAccountNameText"> (AirpayX7)</span>
        <button onclick="copyToClipboard('binancePayIdText')" class="ml-1 text-[11px] text-emerald-400 hover:underline">Salin</button>
    </div>

    <!-- Tombol Aksi -->
    <div class="space-y-2">
        <button id="btnCheckStatus" onclick="handleCheckPayment()" class="w-full bg-[#F3BA2F] hover:bg-[#DCA219] text-black font-bold text-xs py-2.5 rounded-xl transition shadow">
            🔄 Saya Sudah Bayar / Cek Status
        </button>
    </div>
</div>

<script>
    // Konfigurasi dari Backend Toko
    let orderData = {
        orderId: "ord_2I8gk3xQvfcA",
        qrUrl: "http://localhost:3300/api/sandbox/orders/ord_2I8gk3xQvfcA/qr.png",
        totalAmountStr: "0.814",
        currency: "USDT",
        payId: "857242044",
        accountName: "AirpayX7",
        expiresInSeconds: 900
    };

    function initPaymentUI(data) {
        orderData = data;
        document.getElementById('binanceQrImg').src = data.qrUrl;
        document.getElementById('totalAmountText').innerText = data.totalAmountStr + " " + data.currency;
        document.getElementById('binancePayIdText').innerText = data.payId;
        document.getElementById('binanceAccountNameText').innerText = " (" + data.accountName + ")";
        startCountdown(data.expiresInSeconds);
    }

    async function handleCheckPayment() {
        const btn = document.getElementById('btnCheckStatus');
        btn.innerText = "Memeriksa...";
        btn.disabled = true;

        try {
            // Panggil API PaymentG (bisa diakses publik via order_id tanpa mengekspos API Key)
            const res = await fetch(`http://localhost:3300/api/sandbox/orders/${orderData.orderId}/check`, { method: 'POST' });
            const json = await res.json();
            
            if (json.success && json.data.status === 'PAID') {
                alert("🎉 Pembayaran Sukses Dikonfirmasi! Mengalihkan...");
                window.location.href = "/checkout/success?ref=" + json.data.reference_id;
            } else {
                alert("⏳ Pembayaran belum terdeteksi. Silakan pastikan transfer Binance Pay Anda telah selesai.");
            }
        } catch (e) {
            alert("Gagal menghubungi server");
        } finally {
            btn.innerText = "🔄 Saya Sudah Bayar / Cek Status";
            btn.disabled = false;
        }
    }

    function startCountdown(seconds) {
        let remaining = seconds;
        const el = document.getElementById('timerDisplay');
        const interval = setInterval(() => {
            remaining--;
            if (remaining <= 0) {
                clearInterval(interval);
                el.innerText = "EXPIRED";
                alert("Waktu pembayaran telah habis. Silakan buat pesanan baru.");
                return;
            }
            const m = Math.floor(remaining / 60);
            const s = remaining % 60;
            el.innerText = `${m < 10 ? '0' : ''}${m}:${s < 10 ? '0' : ''}${s}`;
        }, 1000);
    }

    function copyToClipboard(elementId) {
        const text = document.getElementById(elementId).innerText.split(" ")[0];
        navigator.clipboard.writeText(text);
        alert("Teks disalin: " + text);
    }
</script>
```

---

## 5. Contoh Implementasi Webhook di Berbagai Bahasa

### A. PHP / Laravel

```php
<?php
// File: routes/api.php atau controller webhook Anda

use Illuminate\Http\Request;
use Illuminate\Support\Facades\Route;

Route::post('/webhook/binance', function (Request $request) {
    $apiKey = env('PAYMENTG_API_KEY'); // API Key toko Anda (ak_...)
    $receivedSignature = $request->header('X-Webhook-Signature');
    $rawPayload = $request->getContent();

    // 1. Verifikasi HMAC-SHA256 Signature
    $expectedSignature = hash_hmac('sha256', $rawPayload, $apiKey);
    if (!hash_equals($expectedSignature, $receivedSignature)) {
        return response()->json(['error' => 'Invalid signature'], 401);
    }

    // 2. Baca Payload
    $data = json_decode($rawPayload, true);
    if (($data['event'] ?? '') === 'payment.success') {
        $invoiceId = $data['reference_id'];
        $binanceTxId = $data['binance_tx_id'];
        $paidAmount = $data['total_amount_str'];

        // 3. Cari pesanan di DB Toko & update menjadi LUNAS
        $order = \App\Models\Order::where('invoice_number', $invoiceId)->first();
        if ($order && $order->status !== 'PAID') {
            $order->update([
                'status' => 'PAID',
                'binance_tx_id' => $binanceTxId,
                'paid_at' => now(),
            ]);

            // Kirim produk / aktifkan lisensi pelanggan di sini...
        }
    }

    return response()->json(['status' => 'received'], 200);
});
```

---

### B. Node.js / Express

```javascript
// File: webhook.js
const express = require('express');
const crypto = require('crypto');
const app = express();

// PENTING: Gunakan express.raw() agar body tidak termutasi sebelum dihitung HMAC
app.post('/api/webhook/binance', express.raw({ type: 'application/json' }), async (req, res) => {
    const apiKey = process.env.PAYMENTG_API_KEY;
    const signature = req.headers['x-webhook-signature'];
    const rawBody = req.body.toString('utf8');

    // 1. Hitung HMAC Signature
    const expected = crypto.createHmac('sha256', apiKey).update(rawBody).digest('hex');
    if (signature !== expected) {
        return res.status(401).json({ error: 'Invalid signature' });
    }

    // 2. Parse JSON
    const payload = JSON.parse(rawBody);
    if (payload.event === 'payment.success') {
        const { reference_id, binance_tx_id, total_amount_str } = payload;
        
        console.log(`[LUNAS] Invoice: ${reference_id}, TxID: ${binance_tx_id}, Nominal: ${total_amount_str}`);
        
        // 3. Update database toko Anda menjadi PAID...
    }

    return res.status(200).json({ status: 'received' });
});
```

---

### C. Python / FastAPI

```python
# File: webhook.py
import hmac
import hashlib
from fastapi import FastAPI, Request, Header, HTTPException

app = FastAPI()
PAYMENTG_API_KEY = "ak_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

@app.post("/api/webhook/binance")
async def binance_webhook(request: Request, x_webhook_signature: str = Header(None)):
    raw_body = await request.body()
    
    # 1. Verifikasi Signature
    expected_sig = hmac.new(PAYMENTG_API_KEY.encode('utf-8'), raw_body, hashlib.sha256).hexdigest()
    if not hmac.compare_digest(expected_sig, x_webhook_signature or ""):
        raise HTTPException(status_code=401, detail="Invalid signature")

    # 2. Proses Event
    payload = await request.json()
    if payload.get("event") == "payment.success":
        ref_id = payload.get("reference_id")
        tx_id = payload.get("binance_tx_id")
        print(f"Pesanan {ref_id} telah lunas dengan Binance TxID {tx_id}")
        
        # 3. Update database pesanan toko...

    return {"status": "received"}
```

---

### D. Go (Gin)

```go
package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
)

func BinanceWebhookHandler(c *gin.Context) {
	apiKey := "ak_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	sig := c.GetHeader("X-Webhook-Signature")

	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	h := hmac.New(sha256.New, []byte(apiKey))
	h.Write(rawBody)
	expectedSig := hex.EncodeToString(h.Sum(nil))

	if sig != expectedSig {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid signature"})
		return
	}

	var payload struct {
		Event       string `json:"event"`
		ReferenceID string `json:"reference_id"`
		BinanceTxID string `json:"binance_tx_id"`
	}
	_ = json.Unmarshal(rawBody, &payload)

	if payload.Event == "payment.success" {
		// Update DB Toko menjadi PAID...
	}

	c.JSON(http.StatusOK, gin.H{"status": "received"})
}
```

---

## 6. Panduan Pengujian Menggunakan Sandbox Simulator

Sebagai developer atau AI agent, Anda dapat menguji seluruh alur integrasi tanpa modal uang sepeser pun:

1. **Jalankan PaymentG** di background:
   ```powershell
   cd C:\laragon\www\paymentg\binance
   .\paymentg-binance.exe
   ```
2. **Koneksikan Toko Anda ke Sandbox:**
   Set `.env` toko Anda ke `PAYMENTG_BASE_URL=http://localhost:3300/api/sandbox`.
3. **Lakukan Checkout di Website Toko Anda:**
   Website toko Anda akan memanggil `POST /api/sandbox/orders` dan menampilkan kotak pembayaran.
4. **Buka Simulator Sandbox di Browser:**
   Buka `http://localhost:3300/sandbox/` (masukkan PIN: `2207`).
5. **Klik Tombol "⚡ 1-Klik Lunas":**
   Di tabel tagihan simulator, cari pesanan yang baru dibuat, lalu klik tombol hijau **[⚡ 1-Klik Lunas]**.
6. **Periksa Toko Anda:**
   - Webhook otomatis masuk ke endpoint toko Anda.
   - Status tagihan di database toko Anda berubah jadi `PAID`.
   - UI pembeli menampilkan pesan pembayaran sukses.

---

## 7. Checklist Kelayakan Go-Live

Sebelum mengalihkan `PAYMENTG_BASE_URL` dari Sandbox ke Live di website produksi:

- [ ] Kredensial Binance API Key & Secret Key personal telah disetel di dashboard admin (`http://localhost:3300/dashboard/`) dan tombol **"⚡ Tes Koneksi API"** menghasilkan status sukses.
- [ ] Endpoint webhook toko sudah memverifikasi header `X-Webhook-Signature` dengan benar.
- [ ] Database toko menyimpan `crypto_amount_str` bertipe string (misal `"0.814"`), bukan dibulatkan ke float 2 desimal.
- [ ] UI Checkout menampilkan pesan peringatan jelas agar pembeli mentransfer nominal persis beserta angka desimalnya.
- [ ] Tombol salin nominal persis (`📋 Salin`) berfungsi dengan baik di perangkat mobile/desktop.
