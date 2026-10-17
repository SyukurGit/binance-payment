# ⚡ Binance Pay Gateway Engine

Binance Pay Gateway Engine adalah *self-hosted crypto payment gateway* mandiri berkinerja tinggi yang mendeteksi pembayaran masuk secara otomatis dari akun personal **Binance Pay** menggunakan API resmi Binance (`GET /sapi/v1/pay/transactions`), mencocokkannya menggunakan **3-Digit Kode Unik Desimal (Milli-Units)**, dan mengirimkan notifikasi Webhook instan ke website e-commerce Anda.

Dilengkapi antarmuka modern untuk pemantauan transaksi riil di `/dashboard` dan simulator pengujian terisolasi penuh di `/sandbox`.

---

## 🌟 Fitur Utama

- **Zero-Dependency Single Binary**: Dikompilasi murni dengan Go (`CGO_ENABLED=0`), footprint RAM < 25 MB, startup instan (< 50ms).
- **Official Personal Binance Pay Client**: Menggunakan API Key personal (Read-Only) resmi dari Binance dengan tanda tangan HMAC-SHA256. Tanpa scraping browser, tanpa resiko akun diblokir.
- **Smart Hold-Fire Poller & On-Demand Rate-Gate**: Mematuhi batas kuota IP Binance (Weight 3000) dengan menahan panggilan pada 30 detik pertama order dan merespons instan (< 1 detik) saat pembeli mengeklik tombol *"Saya Sudah Bayar"*.
- **Atomic Milli-Units Math**: Menggunakan representasi integer (`amount x 1000`) untuk penambahan kode unik 1 s.d 999, bebas floating-point rounding error.
- **Strict Environment Isolation**: Pemisahan 100% database dan kredensial antara Mode Live (`ak_live_...`) dan Sandbox Simulator (`ak_test_...`). Key test otomatis ditolak di Live dan sebaliknya, mencegah risiko manipulasi order real atau kebocoran data.
- **Direct JSON Webhook**: Notifikasi pembayaran otomatis dikirim ke webhook toko dengan payload JSON bersih dan signature keamanan HMAC-SHA256.

---

## 📁 Struktur Direktori

```text
binance/
├── data/
│   ├── payment.db           # SQLite Production (Transaksi Riil USDT)
│   ├── qr/                  # Cache gambar QR PNG Live
│   └── sandbox/
│       ├── payment.db       # SQLite Sandbox (Testing Terisolasi 100%)
│       └── qr/              # Cache gambar QR PNG Sandbox
├── dashboard/               # Frontend UI (Dashboard Live & Sandbox Simulator)
│   ├── index.html           # UI Dashboard Live (/dashboard/)
│   └── sandbox.html         # UI Simulator Sandbox (/sandbox/)
├── internal/                # Engine Go (Config, Database, Handler, Service, Middleware)
├── .env.example             # Template konfigurasi
├── go.mod
├── main.go                  # Entry point & HTTP router
├── INTEGRATION.md           # Panduan integrasi website toko
└── binance.md               # Master technical specification
```

---

## 🚀 Quick Start

### 1. Salin Konfigurasi & Jalankan Binary

```bash
# Salin konfigurasi environment
cp .env.example .env

# Jalankan dalam mode pengembangan
go run main.go

# Atau kompilasi ke binary produksi (Windows)
go build -ldflags="-s -w" -o paymentg-binance.exe main.go
.\paymentg-binance.exe

# Atau kompilasi untuk Linux VPS
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o paymentg-binance main.go
```

Layanan otomatis aktif di `http://localhost:3300` (atau port yang disetel di `.env`).

### 2. Buka Dashboard di Browser

- **Mode Live (Omset Uang Asli):** `http://localhost:3300/dashboard/`
- **Mode Sandbox (Simulator Testing):** `http://localhost:3300/sandbox/`
- **Default PIN Akses:** `220700` (dapat diubah di `.env` / `ADMIN_PIN`).

---

## 🔑 Kredensial & Autentikasi

| Header | Ditujukan Untuk | Fungsi |
| :--- | :--- | :--- |
| `X-Admin-Key` | Pemilik Server | Mengakses dashboard, statistik, mendaftarkan toko baru, dan mengatur Binance API key. |
| `X-API-Key` | Web Toko / Klien | Membuat tagihan Binance Pay, cek status, dan batalkan pesanan. |

---

## 📋 Endpoint API Reference

### 1. Toko Online / Client API (`X-API-Key`)

| Method | Endpoint Live | Endpoint Sandbox | Deskripsi |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/orders` | `/api/sandbox/orders` | Membuat tagihan Binance Pay baru |
| `GET` | `/api/orders/:id` | `/api/sandbox/orders/:id` | Cek status pembayaran pesanan |
| `GET` | `/api/orders/:id/qr.png` | `/api/sandbox/orders/:id/qr.png` | Ambil gambar QR PNG (Publik / `<img>`) |
| `POST` | `/api/orders/:id/check` | `/api/sandbox/orders/:id/check` | Paksa cek mutasi instan (Tombol "Saya Sudah Bayar") |
| `POST` | `/api/orders/:id/cancel` | `/api/sandbox/orders/:id/cancel` | Batalkan tagihan & masukkan kode unik ke karantina |

### 2. Admin & Simulator API

| Method | Endpoint | Auth | Deskripsi |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/health` | Publik | Status server & koneksi Binance |
| `POST` | `/api/auth/pin` | Publik | Login PIN admin |
| `GET` | `/api/stats` | `X-Admin-Key` | Statistik omset & total order |
| `POST` | `/api/apps` | `X-Admin-Key` | Buat toko baru & dapatkan API Key |
| `POST` | `/api/sandbox/orders/:id/pay` | Publik / UI | **Simulasi Bayar 1-Klik Sandbox** |
| `POST` | `/api/sandbox/orders/create-demo` | Publik / UI | Buat tagihan test cepat di Sandbox |
