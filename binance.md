# MASTER SPECIFICATION: PaymentG — Binance Pay Edition (Go + Gin + SQLite)

> **INSTRUKSI UNTUK AI AGENT CLI:**
> Anda ditugaskan untuk membangun aplikasi **PaymentG (Binance Pay Edition)** secara lengkap dari nol hingga siap produksi (Zero Placeholder / No TODOs). Ikuti seluruh spesifikasi arsitektur, skema database, kontrak JSON, algoritma pencocokan mutasi, manajemen rate-limit, dan antarmuka Frontend (`/dashboard` & `/sandbox`) di dalam dokumen ini secara **eksak**. Jangan menebak atau mengubah nama field maupun struktur endpoint.

---

## 1. Gambaran Umum & Tech Stack Wajib

**PaymentG (Binance Pay Edition)** adalah *self-hosted crypto payment gateway* mandiri berkinerja tinggi yang membaca mutasi pembayaran masuk dari akun personal Binance Pay (`GET /sapi/v1/pay/transactions`), mencocokkannya secara otomatis menggunakan **3-digit Kode Unik Desimal (Milli-Units)**, dan mengirimkan notifikasi Webhook JSON instan ke website toko online (klien).

### Tech Stack & Library (Wajib Pure-Go Tanpa CGO):
- **Bahasa:** Go `1.22+` (`CGO_ENABLED=0`)
- **HTTP Framework:** `github.com/gin-gonic/gin`
- **Database:** `modernc.org/sqlite` (Murni Go tanpa butuh `gcc`, gunakan driver `"sqlite"`)
- **QR Code Generator:** `github.com/skip2/go-qrcode` (Render gambar PNG lokal tanpa layanan eksternal)
- **Env Loader:** `github.com/joho/godotenv`
- **Frontend (`/dashboard` & `/sandbox`):** HTML5 + TailwindCSS (via CDN) + Vanilla JavaScript modern yang di-serve statis langsung oleh Go Gin.

---

## 2. Struktur Direktori Proyek

Buat struktur direktori dan file persis seperti di bawah ini:

```text
paymentg-binance/
├── data/
│   ├── payment.db               # Auto-generated: SQLite Production (Uang/USDT Asli)
│   ├── qr/                      # Auto-generated: Cache gambar QR PNG Live
│   └── sandbox/
│       ├── payment.db           # Auto-generated: SQLite Sandbox (Testing Terisolasi 100%)
│       └── qr/                  # Auto-generated: Cache gambar QR PNG Sandbox
├── dashboard/
│   ├── index.html               # UI Dashboard Live (/dashboard/) dengan Login PIN
│   └── sandbox.html             # UI Simulator Sandbox (/sandbox/) dengan tombol Bayar 1-Klik
├── internal/
│   ├── config/
│   │   └── config.go            # Load .env & runtime config
│   ├── database/
│   │   └── sqlite.go            # Koneksi SQLite (WAL mode) & migrasi skema untuk Live & Sandbox
│   ├── middleware/
│   │   ├── auth.go              # Middleware X-Admin-Key (atau Bearer PIN session) & X-API-Key
│   │   └── cors.go              # Middleware CORS agar frontend toko & dashboard lancar
│   ├── service/
│   │   ├── binance.go           # HTTP Client HMAC-SHA256 ke GET /sapi/v1/pay/transactions
│   │   ├── qr.go                # Generator file PNG QR dari URL uni-qr Binance
│   │   ├── order.go             # Logika pembuatan order, alokasi kode unik milli-unit, & pencocokan
│   │   ├── poller.go            # Smart Hold-Fire Poller & On-Demand Rate-Gate
│   │   └── webhook.go           # Pengirim Webhook HTTP POST + HMAC Signature + Retry Worker
│   └── handler/
│       ├── admin.go             # Handler PIN login, stats, kelola apps (toko), & config Binance
│       ├── order.go             # Handler Live & Sandbox orders, cek status, ambil QR, cancel
│       └── sandbox.go           # Handler khusus simulasi bayar 1-klik (/api/sandbox/orders/:id/pay)
├── .env.example                 # Template konfigurasi environment
├── go.mod
└── main.go                      # Entry point, inisialisasi DB, background workers, & Gin router
```

---

## 3. Spesifikasi File `.env.example`

```ini
# Server Configuration
PORT=3200
GIN_MODE=release

# Keamanan Admin & Dashboard
ADMIN_PIN=220700
ADMIN_KEY=pg_admin_secret_key_change_me_immediately

# Kredensial Binance Personal API (Cukup Izin: Enable Reading / Read-Only)
BINANCE_API_KEY=your_binance_read_only_api_key
BINANCE_SECRET_KEY=your_binance_read_only_secret_key
BINANCE_BASE_URL=[https://api.binance.com](https://api.binance.com)

# Identitas Penerima Binance Pay Anda (Ditampilkan ke Pembeli)
BINANCE_PAY_ID=81726354
BINANCE_ACCOUNT_NAME=StoreOfficial
BINANCE_UNI_QR_URL=[https://app.binance.com/uni-qr/c2c_example_your_static_qr](https://app.binance.com/uni-qr/c2c_example_your_static_qr)

# Pengaturan Tagihan & Poller
DEFAULT_CURRENCY=USDT
DEFAULT_EXPIRY_MINUTES=15
COOLDOWN_MINUTES=60
MIN_BINANCE_POLL_INTERVAL_SEC=15
BACKGROUND_SWEEP_INTERVAL_SEC=20
```

---

## 4. Skema Database SQLite (Diterapkan Identik di `data/payment.db` & `data/sandbox/payment.db`)

Setiap koneksi database (`LiveDB` dan `SandboxDB`) **WAJIB** menggunakan connection string:
`file:<path>?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)`
dan disetel `db.SetMaxOpenConns(1)` untuk mencegah error `SQLITE_BUSY` saat penulisan konkuren.

Jalankan DDL berikut saat startup pada kedua database:

```sql
-- 1. Tabel Konfigurasi Runtime (Dapat di-update dari Dashboard tanpa restart)
CREATE TABLE IF NOT EXISTS system_config (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 2. Tabel Web Toko / Client Apps
CREATE TABLE IF NOT EXISTS apps (
    id TEXT PRIMARY KEY,                  -- Format: "app_" + 12 random alphanumeric
    name TEXT NOT NULL,                   -- Nama toko (misal: "Toko Digital A")
    api_key TEXT NOT NULL UNIQUE,         -- Format: "ak_" + 32 random hex chars
    webhook_url TEXT NOT NULL,            -- URL endpoint penerima webhook di toko
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- 3. Tabel Pesanan / Tagihan (Orders)
CREATE TABLE IF NOT EXISTS orders (
    id TEXT PRIMARY KEY,                  -- Format: "ord_" + 12 random alphanumeric
    app_id TEXT NOT NULL,                 -- FK ke apps.id
    reference_id TEXT NOT NULL,           -- ID Invoice dari toko (misal: "INV-1001")
    currency TEXT NOT NULL DEFAULT 'USDT',-- Mata uang kripto (default: "USDT")
    original_amount REAL NOT NULL,        -- Nominal dasar (misal: 10 atau 10.5)
    original_amount_milli INTEGER NOT NULL, -- Nominal dasar x 1000 (misal: 10000)
    unique_code INTEGER NOT NULL,         -- Kode unik 1 s.d 999 (misal: 237 -> +0.237 USDT)
    total_amount REAL NOT NULL,           -- Nominal akhir float (misal: 10.237)
    total_amount_str TEXT NOT NULL,       -- String eksak 3 desimal: "10.237"
    total_amount_milli INTEGER NOT NULL,  -- Integer eksak untuk matching: 10237
    status TEXT NOT NULL DEFAULT 'PENDING', -- PENDING | PAID | EXPIRED | CANCELLED
    binance_tx_id TEXT UNIQUE,            -- Transaction ID dari Binance (atau Sandbox ID)
    payer_name TEXT DEFAULT '',           -- Nama pengirim dari payerInfo.name Binance
    qr_path TEXT NOT NULL,                -- Path file lokal PNG QR
    created_at INTEGER NOT NULL,          -- Unix timestamp (milliseconds)
    expires_at INTEGER NOT NULL,          -- Unix timestamp (milliseconds)
    cooldown_until INTEGER NOT NULL,      -- Unix timestamp (ms) = expires_at + (COOLDOWN_MINUTES * 60000)
    paid_at INTEGER DEFAULT 0,            -- Unix timestamp (milliseconds) saat lunas
    webhook_delivered INTEGER DEFAULT 0,  -- 0 = belum terkirim, 1 = sukses HTTP 200
    webhook_attempts INTEGER DEFAULT 0,   -- Jumlah percobaan kirim webhook
    next_webhook_retry_at INTEGER DEFAULT 0, -- Unix timestamp (ms) jadwal retry berikutnya
    FOREIGN KEY (app_id) REFERENCES apps(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_orders_matching 
ON orders(currency, total_amount_milli, status, cooldown_until);

CREATE INDEX IF NOT EXISTS idx_orders_status_created 
ON orders(status, created_at, expires_at);

-- 4. Tabel Mutasi Tak Dikenal (Untuk menampung dana masuk yang salah nominal)
CREATE TABLE IF NOT EXISTS unmatched_transactions (
    binance_tx_id TEXT PRIMARY KEY,
    currency TEXT NOT NULL,
    amount_str TEXT NOT NULL,
    amount_milli INTEGER NOT NULL,
    payer_name TEXT DEFAULT '',
    tx_time INTEGER NOT NULL,
    recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

*Catatan Penting Sinkronisasi Toko (`apps`):* Saat admin membuat atau menghapus toko di `POST /api/apps` atau `DELETE /api/apps/:id`, jalankan query `INSERT` / `DELETE` tersebut pada **`LiveDB` DAN `SandboxDB` sekaligus**! Dengan begitu, `X-API-Key` yang sama otomatis langsung berlaku di endpoint Live (`/api/orders`) maupun Sandbox (`/api/sandbox/orders`) (Zero-Code Switching).

---

## 5. Spesifikasi Logika Inti (Core Engine Rules)

### 5.1 Rumus Alokasi Kode Unik Desimal Atomik (`Milli-Units`)
1. Setiap `1.000 USDT` direpresentasikan sebagai `1000` *milli-units* (`INTEGER`).
2. Saat `POST /api/orders` menerima `"amount": 10`, hitung:
   `original_amount_milli = int64(math.Round(amount * 1000))` (contoh: `10000`).
3. Untuk mencari `unique_code` (rentang `1` s.d. `999`):
   - Buat daftar angka `1..999` yang diacak (*shuffled* atau mulai dari acak).
   - Untuk setiap kandidat `code`, hitung `candidate_total_milli = original_amount_milli + code`.
   - Cek ke database apakah `candidate_total_milli` untuk `currency` tersebut sedang terpakai oleh order yang masih aktif atau masih dalam masa karantina:
     ```sql
     SELECT COUNT(1) FROM orders
     WHERE currency = ?
       AND total_amount_milli = ?
       AND (
         status = 'PENDING'
         OR (status IN ('EXPIRED', 'CANCELLED') AND cooldown_until > ?)
       )
     ```
     *(Parameter ke-3 adalah `nowMillis`).*
   - Pilih kandidat pertama yang menghasilkan `COUNT = 0`.
   - Format `total_amount_str = fmt.Sprintf("%.3f", float64(candidate_total_milli)/1000.0)` (contoh: `"10.237"`).
   - Format `total_amount` sebagai float64 `float64(candidate_total_milli) / 1000.0` (contoh: `10.237`).
4. **Mengapa ada `cooldown_until`?** Jika pesanan kedaluwarsa (`EXPIRED`) atau dibatalkan (`CANCELLED`), nominal unik `10.237` dikarantina selama `COOLDOWN_MINUTES` (default 60 menit) agar jika pembeli terlambat menekan tombol *Send* di aplikasi Binance beberapa menit setelah waktu habis, uangnya tetap terdeteksi masuk ke order miliknya (bukan salah melunaskan order pembeli baru).

---

### 5.2 Spesifikasi Client API Binance (`GET /sapi/v1/pay/transactions`)
1. **Endpoint:** `GET {BINANCE_BASE_URL}/sapi/v1/pay/transactions`
2. **Query Parameters:**
   - `startTime`: Unix timestamp dalam milidetik (ambil `MIN(created_at) - 60000` dari order yang sedang dipantau, atau `now - 24 jam`).
   - `limit`: `100`
   - `timestamp`: `time.Now().UnixMilli()`
   - `recvWindow`: `10000`
3. **Signature HMAC-SHA256:**
   - Buat query string: `startTime=...&limit=100&recvWindow=10000&timestamp=...`
   - Hitung HMAC-SHA256 menggunakan `BINANCE_SECRET_KEY` (ambil dari tabel `system_config` jika ada, fallback ke `.env`).
   - Tambahkan `&signature=<hex_signature>` di ujung URL.
   - Kirim header HTTP: `X-MBX-APIKEY: <BINANCE_API_KEY>`.
4. **Parsing JSON Response Binance:**
   ```json
   {
     "code": "000000",
     "message": "success",
     "data": [
       {
         "orderType": "C2C",
         "transactionId": "M_P_71505104267788288",
         "transactionTime": 1760400000000,
         "amount": "10.237",
         "currency": "USDT",
         "payerInfo": {
           "name": "BudiCrypto",
           "type": "USER"
         }
       }
     ],
     "success": true
   }
   ```
5. **Aturan Pencocokan Mutasi Masuk (Matching Logic):**
   - Abaikan transaksi jika `amount` diawali tanda minus `"-"` (itu adalah transaksi keluar).
   - Konversi string `tx.Amount` ke `float64`, lalu ubah ke integer milli: `txMilli := int64(math.Round(parsedFloat * 1000))`.
   - Cek apakah `tx.TransactionID` sudah ada di `orders.binance_tx_id`. Jika sudah ada, lewati (idempoten).
   - Cari order di `LiveDB` yang cocok dengan kueri:
     ```sql
     SELECT id, app_id, reference_id, currency, original_amount, unique_code, total_amount, total_amount_str
     FROM orders
     WHERE currency = ?
       AND total_amount_milli = ?
       AND created_at <= ?
       AND (
         status = 'PENDING'
         OR (status = 'EXPIRED' AND cooldown_until > ?)
       )
     ORDER BY created_at DESC
     LIMIT 1
     ```
     *(Catatan: Beri toleransi `created_at - 10000 <= tx.TransactionTime` untuk mengantisipasi selisih tipis jam server).*
   - **Jika Cocok:**
     - Update baris order tersebut: `status = 'PAID'`, `binance_tx_id = tx.TransactionID`, `payer_name = tx.PayerInfo.Name`, `paid_at = tx.TransactionTime`.
     - Panggil fungsi pengiriman Webhook secara *asynchronous* (`go SendOrderWebhook(LiveDB, orderID)`).
   - **Jika Tidak Cocok dengan Order Mana Pun:**
     - Simpan ke tabel `unmatched_transactions` (`INSERT OR IGNORE`) jika `tx.TransactionTime` berada dalam 24 jam terakhir, agar pemilik server bisa memantau di Dashboard jika ada pembeli yang salah mengetik nominal.

---

### 5.3 Algoritma "Smart Hold-Fire Poller & On-Demand Rate-Gate"
Endpoint `/sapi/v1/pay/transactions` memiliki bobot `3000` (batas kuota Binance adalah `12.000/menit`). Agar pembeli mendapatkan deteksi **instan (0 detik tunggu)** saat mengeklik tombol *"Saya Sudah Bayar"* tanpa membuat VPS terkena blokir rate-limit, implementasikan aturan berikut di `internal/service/poller.go`:

1. **State di Memori (`PollerService`):**
   - `mu sync.Mutex`
   - `lastBinanceCall time.Time`
   - `lastBinanceErr string`
   - `minInterval = 15 * time.Second` (Dari `.env` `MIN_BINANCE_POLL_INTERVAL_SEC`).
2. **Aturan 1 — Hold-Fire Saat Order Baru Dibuat (0–30 Detik Pertama):**
   - Background worker memeriksa database setiap `5 detik` untuk menandai order `PENDING` yang sudah melewati `expires_at` menjadi `EXPIRED`.
   - Namun, background worker **HANYA** memanggil API Binance secara otomatis (setiap `20 detik`) jika:
     a) Ada order `PENDING` yang umurnya sudah **lebih dari 30 detik** (`now - created_at >= 30000`), **ATAU**
     b) Ada order yang baru saja `EXPIRED` dalam **10 menit terakhir** (Tail-Polling mỗi 60 detik untuk menangkap pembayaran terlambat).
   - **Kenapa menunggu 30 detik pertama?** Agar selama 30 detik pertama saat pembeli sedang membuka aplikasi Binance untuk mentransfer, jatah panggilan API Binance **tidak terpakai sia-sia**.
3. **Aturan 2 — Eksekusi Instan Saat Tombol "Saya Sudah Bayar" Diklik (`POST /api/orders/:id/check`):**
   - Pertama, baca status order di SQLite. Jika sudah `PAID`, langsung kembalikan respons `PAID` (0 panggilan ke Binance).
   - Jika masih `PENDING`:
     - Cek `elapsed := time.Since(lastBinanceCall)`.
     - **Jika `elapsed >= 15 detik`:** Langsung perbarui `lastBinanceCall = time.Now()`, panggil API Binance **detik itu juga secara sinkron**, jalankan pencocokan mutasi, lalu kembalikan status terbaru order dari SQLite! (Pembeli langsung mendapat status `PAID` dalam < 1 detik).
     - **Jika `elapsed < 15 detik` dan `remaining := 15s - elapsed <= 4 detik`:** Tahan request dengan `time.Sleep(remaining)`, lalu panggil API Binance dan kembalikan hasilnya.
     - **Jika `remaining > 4 detik`:** Jangan panggil API Binance (kembalikan status SQLite saat ini beserta field `"next_check_in_seconds": int(remaining.Seconds())`).

---

### 5.4 Generator QR Code Lokal (`internal/service/qr.go`)
- Saat order dibuat (baik di Live maupun Sandbox), ambil nilai `BINANCE_UNI_QR_URL` dari `system_config` (atau `.env`). Jika kosong, gunakan fallback string `https://app.binance.com/uni-qr/pay_` + `BINANCE_PAY_ID`.
- Gunakan `qrcode.WriteFile(qrContent, qrcode.Medium, 320, filePath)` untuk menyimpan file PNG ke:
  - Live: `data/qr/<order_id>.png`
  - Sandbox: `data/sandbox/qr/<order_id>.png`
- Sajikan file PNG ini melalui endpoint publik `GET /api/orders/:id/qr.png` dan `GET /api/sandbox/orders/:id/qr.png` dengan header `Content-Type: image/png` dan `Cache-Control: public, max-age=86400`.

---

### 5.5 Mesin Webhook & Auto-Retry (`internal/service/webhook.go`)
Saat status order berubah menjadi `PAID` (baik karena mutasi asli di `LiveDB` maupun klik tombol simulasi bayar di `SandboxDB`):
1. Ambil `webhook_url` dan `api_key` dari tabel `apps` berdasarkan `order.app_id`.
2. Susun JSON Payload berikut:
   ```json
   {
     "event": "payment.success",
     "is_sandbox": false,
     "order_id": "ord_aBcDeFg123",
     "reference_id": "INV-1001",
     "currency": "USDT",
     "original_amount": 10,
     "unique_code": 237,
     "total_amount": 10.237,
     "total_amount_str": "10.237",
     "paid_at": "2026-10-14T16:18:24Z",
     "binance_tx_id": "M_P_71505104267788288"
   }
   ```
   *(Catatan: Untuk Sandbox, `"is_sandbox": true` dan `"binance_tx_id": "SBX_71505104267"`).*
3. Hitung signature HMAC-SHA256 dari body JSON di atas menggunakan `app.api_key` sebagai secret, lalu kirim `HTTP POST` (timeout 10 detik) ke `app.webhook_url` dengan headers:
   ```http
   Content-Type: application/json
   X-Webhook-Event: payment.success
   X-Webhook-Signature: <hex_hmac_sha256>
   ```
4. Jika response HTTP status code berada di rentang `200–299`, update order: `webhook_delivered = 1, webhook_attempts = webhook_attempts + 1`.
5. Jika gagal / timeout, update `webhook_attempts = webhook_attempts + 1` dan jadwalkan `next_webhook_retry_at` dengan jeda bertahap (15 detik, 60 detik, 300 detik, maksimal 5 kali percobaan). Buat background ticker setiap 15 detik yang memeriksa dan mengirim ulang webhook yang belum terkirim (`status = 'PAID' AND webhook_delivered = 0 AND webhook_attempts < 5 AND next_webhook_retry_at <= now`).

---

## 6. Spesifikasi Lengkap Endpoint REST API

### A. Endpoint Toko Online / Client API (Wajib Header `X-API-Key`)

| Method | Endpoint Live | Endpoint Sandbox | Fungsi |
| :--- | :--- | :--- | :--- |
| `POST` | `/api/orders` | `/api/sandbox/orders` | Membuat tagihan Binance Pay baru |
| `GET` | `/api/orders/:id` | `/api/sandbox/orders/:id` | Cek status tagihan (hanya baca SQLite VPS) |
| `GET` | `/api/orders/:id/qr.png` | `/api/sandbox/orders/:id/qr.png` | Ambil gambar QR PNG (**Publik tanpa header auth** agar bisa dipasang di `<img src="">`) |
| `POST` | `/api/orders/:id/check` | `/api/sandbox/orders/:id/check` | Tombol "Saya Sudah Bayar" (Memicu Smart Rate-Gate ke Binance jika Live) |
| `POST` | `/api/orders/:id/cancel` | `/api/sandbox/orders/:id/cancel` | Batalkan pesanan & masukkan kode unik ke masa karantina |

#### Detail Request & Response `POST /api/orders` (dan `/api/sandbox/orders`):
- **Request Body:**
  ```json
  {
    "reference_id": "INV-1001",
    "amount": 10,
    "currency": "USDT",
    "expiry_minutes": 15
  }
  ```
  *(Jika `currency` kosong, gunakan `"USDT"`. Jika `expiry_minutes` kosong atau <= 0, gunakan `15`).*
- **Response (`HTTP 201 Created`):**
  ```json
  {
    "success": true,
    "data": {
      "order_id": "ord_aBcDeFg123",
      "reference_id": "INV-1001",
      "currency": "USDT",
      "original_amount": 10,
      "unique_code": 237,
      "total_amount": 10.237,
      "total_amount_str": "10.237",
      "binance_pay_id": "81726354",
      "binance_account_name": "StoreOfficial",
      "binance_uni_qr_url": "[https://app.binance.com/uni-qr/c2c_example](https://app.binance.com/uni-qr/c2c_example)",
      "status": "PENDING",
      "qr_url": "/api/orders/ord_aBcDeFg123/qr.png",
      "expires_at": "2026-10-14T16:30:00Z",
      "expires_in_seconds": 900
    }
  }
  ```
  *(Untuk Sandbox, `qr_url` bernilai `"/api/sandbox/orders/ord_aBcDeFg123/qr.png"`).*

#### Detail Response `GET /api/orders/:id` & `POST /api/orders/:id/check`:
```json
{
  "success": true,
  "data": {
    "order_id": "ord_aBcDeFg123",
    "reference_id": "INV-1001",
    "currency": "USDT",
    "original_amount": 10,
    "unique_code": 237,
    "total_amount": 10.237,
    "total_amount_str": "10.237",
    "binance_pay_id": "81726354",
    "binance_account_name": "StoreOfficial",
    "binance_uni_qr_url": "[https://app.binance.com/uni-qr/c2c_example](https://app.binance.com/uni-qr/c2c_example)",
    "status": "PAID",
    "binance_tx_id": "M_P_71505104267788288",
    "paid_at": "2026-10-14T16:18:24Z",
    "qr_url": "/api/orders/ord_aBcDeFg123/qr.png",
    "expires_at": "2026-10-14T16:30:00Z",
    "expires_in_seconds": 420
  }
}
```

---

### B. Endpoint Admin & Simulator API

| Method | Endpoint | Auth | Fungsi |
| :--- | :--- | :--- | :--- |
| `GET` | `/api/health` | Publik | Status server, status koneksi API Binance, waktu sync terakhir |
| `POST` | `/api/auth/pin` | Publik | Verifikasi `{ "pin": "220700" }`, jika benar kembalikan `{ "success": true, "admin_key": "<ADMIN_KEY>" }` |
| `GET` | `/api/config/binance` | `X-Admin-Key` | Mengambil konfigurasi `binance_pay_id`, `binance_account_name`, `binance_uni_qr_url`, dan status API key (masked) |
| `PUT` | `/api/config/binance` | `X-Admin-Key` | Mengubah `binance_api_key`, `binance_secret_key`, `binance_pay_id`, `binance_account_name`, `binance_uni_qr_url` di tabel `system_config` |
| `POST` | `/api/config/binance/test` | `X-Admin-Key` | Mengetes koneksi `GET /sapi/v1/pay/transactions` ke Binance dan mengembalikan jumlah mutasi yang terbaca |
| `GET` | `/api/stats` | `X-Admin-Key` | Statistik Live: total omset USDT (`PAID`), jumlah order `PAID`, `PENDING`, `EXPIRED`, dan `unmatched_transactions` |
| `GET` | `/api/orders` | `X-Admin-Key` | Daftar 100 riwayat transaksi terakhir di LiveDB beserta filter status |
| `GET` | `/api/unmatched` | `X-Admin-Key` | Daftar mutasi masuk di Binance yang nominalnya tidak cocok dengan order mana pun |
| `POST` | `/api/apps` | `X-Admin-Key` | Mendaftarkan web toko baru `{ "name": "...", "webhook_url": "..." }` ke LiveDB & SandboxDB, mengembalikan `api_key` |
| `GET` | `/api/apps` | `X-Admin-Key` | Daftar seluruh web toko terdaftar |
| `DELETE` | `/api/apps/:id` | `X-Admin-Key` | Menghapus web toko dari LiveDB & SandboxDB |
| `GET` | `/api/sandbox/orders` | `X-Admin-Key` | Daftar seluruh transaksi di SandboxDB |
| `POST` | `/api/sandbox/orders/:id/pay` | `X-Admin-Key` / Publik di UI Sandbox | **Simulasi Bayar 1-Klik Sandbox:** Mengubah status order Sandbox menjadi `PAID`, membuat `binance_tx_id` palsu (`SBX_<timestamp>`), dan langsung menembakkan Webhook asli ke `webhook_url` toko! |
| `POST` | `/api/sandbox/orders/create-demo` | `X-Admin-Key` | Membuat order dummy di Sandbox dari UI `/sandbox/` untuk menguji alur tanpa harus dari web toko |

---

## 7. Spesifikasi Antarmuka Web (`/dashboard` & `/sandbox`)

Sajikan file statis di route Gin:
- `GET /dashboard` dan `GET /dashboard/*` $\rightarrow$ merender `dashboard/index.html`
- `GET /sandbox` dan `GET /sandbox/*` $\rightarrow$ merender `dashboard/sandbox.html`

### 7.1 Desain `dashboard/index.html` (Mode Live):
- Gunakan tema gelap modern (*Dark Mode* bergaya Binance dengan aksen kuning emas `#F3BA2F` dan latar `#0B0E11` / `#181A20`).
- **Layar Kunci PIN:** Saat pertama dibuka, jika `sessionStorage.getItem('pg_admin_key')` belum ada, tampilkan modal input PIN (`POST /api/auth/pin`). Setelah PIN benar, simpan `admin_key` ke `sessionStorage` dan muat data dashboard.
- **4 Kartu Statistik Utama:** Total Omset (`USDT`), Transaksi Sukses (`PAID`), Tagihan Aktif (`PENDING`), dan Mutasi Tak Dikenal (`Unmatched`).
- **Tab 1 — Riwayat Transaksi Live:** Tabel real-time (auto-refresh tiap 5 detik) menampilkan `Order ID`, `Ref ID Toko`, `Total Tagihan (USDT)`, `Status` (badge warna), `TxID Binance`, `Status Webhook`, dan waktu.
- **Tab 2 — Kelola Toko (`X-API-Key`):** Form tambah toko baru (`Nama Toko` & `Webhook URL`), tabel daftar toko beserta tombol **Copy `X-API-Key`** dan tombol Hapus.
- **Tab 3 — Pengaturan Binance Pay:** Form untuk memperbarui `Binance Pay ID`, `Nama Akun Binance`, `Link Statis uni-qr`, `API Key`, dan `Secret Key` secara langsung dari browser, dilengkapi tombol **"Tes Koneksi API Binance"** (`POST /api/config/binance/test`).
- **Tab 4 — Mutasi Tak Dikenal (`Unmatched`):** Tabel untuk melihat dana masuk di Binance yang nominalnya salah diketik oleh pembeli.

### 7.2 Desain `dashboard/sandbox.html` (Mode Sandbox Simulator):
- Beri banner atas berwarna mencolok (**"🧪 SANDBOX SIMULATOR MODE — TIDAK MENGGUNAKAN UANG ASLI"**) dan tombol navigasi kembali ke `/dashboard`.
- Menampilkan daftar seluruh tagihan yang masuk ke `/api/sandbox/orders` (auto-refresh tiap 3 detik).
- Untuk setiap tagihan berstatus `PENDING`, tampilkan:
  - Gambar QR Code (`/api/sandbox/orders/:id/qr.png`), Nominal Unik (`10.237 USDT`), dan Waktu Tersisa.
  - Tombol Hijau Besar: **[ ⚡ Simulasikan Bayar Lunas (1-Klik) ]** yang memanggil `POST /api/sandbox/orders/:id/pay`. Saat diklik, order otomatis berubah menjadi `PAID` dan langsung menembakkan Webhook ke web toko.
- Sediakan juga tombol **[ + Buat Tagihan Test Cepat ]** di halaman Sandbox agar pemilik server bisa mengetes pembuatan order dan tembakan webhook langsung dari layar `/sandbox`.

---

## 8. Blueprint Integrasi di Halaman Web Toko (Inline Render + Guest Checkout)

Sertakan file dokumentasi `INTEGRATION.md` di dalam repositori proyek yang menjelaskan cara web toko online (tanpa login / *Guest Checkout*) mengintegrasikan `PaymentG`:

1. **Zero-Code Switching:**
   Di `.env` web toko, cukup atur:
   - Saat Testing: `PAYMENTG_URL=http://localhost:3200/api/sandbox`
   - Saat Live: `PAYMENTG_URL=https://pay.domainmu.com/api`
   Dengan `PAYMENTG_API_KEY=ak_xxxx` yang sama!
2. **Alur Halaman Toko (Tab Sama, Render di Bawah):**
   - Pembeli mengisi email dan mengeklik tombol **"Bayar dengan Binance Pay"**.
   - Backend toko memanggil `POST {PAYMENTG_URL}/orders` dan menyimpan `order_id` beserta kode klaim produk (status `PENDING`) di database toko.
   - Frontend toko **langsung menyimpan `order_id` ke `localStorage`** dan mengubah URL browser menjadi `?order_id=ord_xxx` (`window.history.replaceState`) sebagai pengaman jika browser HP tertutup atau ter-refresh.
   - Halaman toko membuka kotak pembayaran tepat di bagian bawah halaman (`scrollIntoView({ behavior: 'smooth' })`) yang menampilkan:
     1. Nominal Unik yang wajib ditransfer persis: **`10.237 USDT`** + Tombol **[Salin Nominal]**.
     2. Gambar QR: `<img src="https://pay.domainmu.com/api/orders/ord_xxx/qr.png">` + Tombol **[Buka Aplikasi Binance]** (`binance_uni_qr_url`).
     3. Nomor **Binance Pay ID** (`81726354`) + Tombol **[Salin Pay ID]**.
     4. Tombol Utama: **[ 🔄 Saya Sudah Bayar / Cek Status ]** yang memanggil `POST {PAYMENTG_URL}/orders/ord_xxx/check`.
   - Begitu respons mengembalikan `"status": "PAID"` (atau saat Webhook `payment.success` diterima oleh backend toko), halaman yang sama langsung membuka produk/kode klaim kepada pembeli dan mengirimkan email cadangan berisi *Magic Link* klaim produk.

---

## 9. Urutan Langkah Eksekusi untuk AI Agent CLI

1. Inisialisasi `go.mod` (`module paymentg-binance`) dan instal seluruh dependensi (`gin`, `modernc.org/sqlite`, `go-qrcode`, `godotenv`).
2. Buat seluruh kode di `internal/config`, `internal/database`, `internal/middleware`, `internal/service`, dan `internal/handler` tanpa ada fungsi kosong atau placeholder.
3. Buat antarmuka lengkap `dashboard/index.html` dan `dashboard/sandbox.html`.
4. Buat `main.go`, `.env.example`, `README.md`, dan `INTEGRATION.md`.
5. Jalankan `go mod tidy` dan lakukan kompilasi `CGO_ENABLED=0 go build -o paymentg-binance main.go` hingga berhasil menghasilkan *single binary* tanpa satu pun error atau warning.