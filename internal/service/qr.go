package service

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/skip2/go-qrcode"
)

// GenerateQRCode membuat gambar PNG QR code lokal dari URL atau teks tujuan
func GenerateQRCode(content string, destPath string) error {
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("gagal membuat direktori qr: %w", err)
	}

	// Buat QR code medium 320x320 px
	err := qrcode.WriteFile(content, qrcode.Medium, 320, destPath)
	if err != nil {
		return fmt.Errorf("gagal render qrcode: %w", err)
	}

	return nil
}
