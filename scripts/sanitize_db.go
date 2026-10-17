package main

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	fmt.Println("🧹 Memulai Sanitasi Database: Pemisahan Total Data Live vs Sandbox...")

	// 1. Sanitasi Live Database (data/payment.db)
	sanitizeLiveDB("data/payment.db")

	// 2. Sanitasi Sandbox Database (data/sandbox/payment.db)
	sanitizeSandboxDB("data/sandbox/payment.db")

	fmt.Println("✅ Sanitasi Database Selesai! Data Live dan Sandbox kini 100% terisolasi.")
}

func sanitizeLiveDB(path string) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		log.Printf("[LiveDB] Gagal membuka %s: %v", path, err)
		return
	}
	defer db.Close()

	// Hapus toko demo sandbox yang sebelumnya sempat tersinkronisasi ke LiveDB
	res, err := db.Exec("DELETE FROM apps WHERE id = 'app_demo_sandbox' OR LOWER(name) LIKE '%sandbox%'")
	if err == nil {
		affected, _ := res.RowsAffected()
		if affected > 0 {
			fmt.Printf("  [LiveDB] Berhasil menghapus %d toko demo sandbox dari database riil.\n", affected)
		}
	}

	// Update key toko live yang tersisa agar ber-prefix ak_live_
	rows, err := db.Query("SELECT id, api_key FROM apps")
	if err == nil {
		defer rows.Close()
		type item struct{ id, key string }
		var toUpdate []item
		for rows.Next() {
			var id, key string
			if err := rows.Scan(&id, &key); err == nil {
				if !strings.HasPrefix(key, "ak_live_") {
					newKey := "ak_live_" + strings.TrimPrefix(key, "ak_")
					toUpdate = append(toUpdate, item{id, newKey})
				}
			}
		}
		rows.Close()

		for _, u := range toUpdate {
			_, _ = db.Exec("UPDATE apps SET api_key = ? WHERE id = ?", u.key, u.id)
			fmt.Printf("  [LiveDB] Memperbarui API Key toko '%s' ke format baru: %s\n", u.id, u.key)
		}
	}
}

func sanitizeSandboxDB(path string) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		log.Printf("[SandboxDB] Gagal membuka %s: %v", path, err)
		return
	}
	defer db.Close()

	// Pastikan semua key toko di Sandbox ber-prefix ak_test_
	rows, err := db.Query("SELECT id, api_key FROM apps")
	if err == nil {
		defer rows.Close()
		type item struct{ id, key string }
		var toUpdate []item
		for rows.Next() {
			var id, key string
			if err := rows.Scan(&id, &key); err == nil {
				if !strings.HasPrefix(key, "ak_test_") {
					newKey := "ak_test_" + strings.TrimPrefix(key, "ak_")
					toUpdate = append(toUpdate, item{id, newKey})
				}
			}
		}
		rows.Close()

		for _, u := range toUpdate {
			_, _ = db.Exec("UPDATE apps SET api_key = ? WHERE id = ?", u.key, u.id)
			fmt.Printf("  [SandboxDB] Memperbarui API Key toko '%s' ke format sandbox: %s\n", u.id, u.key)
		}
	}

	// Jika apps di sandbox kosong, buat 1 default app
	var count int
	_ = db.QueryRow("SELECT COUNT(1) FROM apps").Scan(&count)
	if count == 0 {
		_, _ = db.Exec(`
			INSERT INTO apps (id, name, api_key, webhook_url)
			VALUES ('app_demo_sandbox', 'Toko Testing Sandbox', 'ak_test_defaultdemo000000000000000000', 'https://webhook.site/test-sandbox-binance')
		`)
		fmt.Println("  [SandboxDB] Membuat toko testing default sandbox dengan key 'ak_test_...'")
	}
}
