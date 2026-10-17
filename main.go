package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"paymentg-binance/internal/config"
	"paymentg-binance/internal/database"
	"paymentg-binance/internal/handler"
	"paymentg-binance/internal/middleware"
	"paymentg-binance/internal/service"

	"github.com/gin-gonic/gin"
)

func main() {
	cfg := config.LoadConfig()

	// 1. Inisialisasi Database (Live & Sandbox)
	dbHub, err := database.InitDB()
	if err != nil {
		log.Fatalf("Fatal: Gagal inisialisasi database: %v", err)
	}
	defer dbHub.LiveDB.Close()
	defer dbHub.SandboxDB.Close()

	// 2. Inisialisasi Service & Background Workers
	pollerService := service.NewPollerService(dbHub.LiveDB, cfg)
	pollerService.StartBackgroundWorkers()
	service.StartWebhookRetryWorker(dbHub.LiveDB, dbHub.SandboxDB)

	// 3. Inisialisasi Handler
	liveOrderHandler := handler.NewOrderHandler(dbHub.LiveDB, cfg, pollerService, false)
	sandboxOrderHandler := handler.NewOrderHandler(dbHub.SandboxDB, cfg, nil, true)
	sandboxHandler := handler.NewSandboxHandler(dbHub, cfg)
	adminHandler := handler.NewAdminHandler(dbHub, cfg, pollerService)

	// 4. Setup Gin HTTP Router
	gin.SetMode(cfg.GinMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(gin.Recovery())
	r.Use(middleware.CORS())

	// Static UI Dashboard & Sandbox
	r.GET("/dashboard", func(c *gin.Context) { c.File("dashboard/index.html") })
	r.GET("/dashboard/*filepath", func(c *gin.Context) { c.File("dashboard/index.html") })
	r.GET("/sandbox", func(c *gin.Context) { c.File("dashboard/sandbox.html") })
	r.GET("/sandbox/*filepath", func(c *gin.Context) { c.File("dashboard/sandbox.html") })

	// Redirect root ke dashboard
	r.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/dashboard/")
	})

	// Public Health & PIN Auth
	r.GET("/api/health", adminHandler.HealthCheck)
	r.POST("/api/auth/pin", adminHandler.VerifyPIN)

	// Public QR Code Image (Tanpa auth agar bisa dirender di <img src="">)
	r.GET("/api/orders/:id/qr.png", liveOrderHandler.GetQRCode)
	r.GET("/api/sandbox/orders/:id/qr.png", sandboxOrderHandler.GetQRCode)

	// Client API - LIVE
	liveAPI := r.Group("/api")
	liveAPIRequired := liveAPI.Group("")
	liveAPIRequired.Use(middleware.RequireAPIKey(dbHub.LiveDB, false))
	{
		liveAPIRequired.POST("/orders", liveOrderHandler.CreateOrder)
		liveAPIRequired.POST("/orders/:id/cancel", liveOrderHandler.CancelOrder)
	}
	liveAPIOptional := liveAPI.Group("")
	liveAPIOptional.Use(middleware.OptionalAPIKey(dbHub.LiveDB, false))
	{
		liveAPIOptional.GET("/orders/:id", liveOrderHandler.GetOrder)
		liveAPIOptional.POST("/orders/:id/check", liveOrderHandler.CheckOrder)
	}

	// Client API - SANDBOX
	sandboxAPI := r.Group("/api/sandbox")
	sandboxAPIRequired := sandboxAPI.Group("")
	sandboxAPIRequired.Use(middleware.RequireAPIKey(dbHub.SandboxDB, true))
	{
		sandboxAPIRequired.POST("/orders", sandboxOrderHandler.CreateOrder)
		sandboxAPIRequired.POST("/orders/:id/cancel", sandboxOrderHandler.CancelOrder)
	}
	sandboxAPIOptional := sandboxAPI.Group("")
	sandboxAPIOptional.Use(middleware.OptionalAPIKey(dbHub.SandboxDB, true))
	{
		sandboxAPIOptional.GET("/orders/:id", sandboxOrderHandler.GetOrder)
		sandboxAPIOptional.POST("/orders/:id/check", sandboxOrderHandler.CheckOrder)
	}

	// Sandbox Simulator UI Controls (Terproteksi X-Admin-Key via Login PIN)
	sandboxAdminAPI := r.Group("/api/sandbox")
	sandboxAdminAPI.Use(middleware.RequireAdmin(cfg.AdminKey))
	{
		sandboxAdminAPI.GET("/orders", sandboxHandler.ListSandboxOrders)
		sandboxAdminAPI.POST("/orders/:id/pay", sandboxHandler.SimulatePay)
		sandboxAdminAPI.POST("/orders/create-demo", sandboxHandler.CreateDemoOrder)
		sandboxAdminAPI.GET("/credentials", sandboxHandler.GetSandboxCredentials)
		sandboxAdminAPI.GET("/apps", sandboxHandler.ListSandboxApps)
		sandboxAdminAPI.POST("/apps", sandboxHandler.CreateSandboxApp)
		sandboxAdminAPI.DELETE("/apps/:id", sandboxHandler.DeleteSandboxApp)
		sandboxAdminAPI.GET("/stats", sandboxHandler.GetSandboxStats)
	}

	// Admin API (Wajib X-Admin-Key)
	adminAPI := r.Group("/api")
	adminAPI.Use(middleware.RequireAdmin(cfg.AdminKey))
	{
		adminAPI.GET("/stats", adminHandler.GetStats)
		adminAPI.GET("/orders", adminHandler.GetOrders)
		adminAPI.GET("/unmatched", adminHandler.GetUnmatched)
		adminAPI.GET("/apps", adminHandler.GetApps)
		adminAPI.POST("/apps", adminHandler.CreateApp)
		adminAPI.DELETE("/apps/:id", adminHandler.DeleteApp)
		adminAPI.GET("/config/binance", adminHandler.GetBinanceConfig)
		adminAPI.PUT("/config/binance", adminHandler.UpdateBinanceConfig)
		adminAPI.POST("/config/binance/test", adminHandler.TestBinanceConfig)
	}

	fmt.Println("==================================================================")
	fmt.Printf("⚡ Binance Pay Gateway Engine Aktif di Port :%s\n", cfg.Port)
	fmt.Printf("📊 Live Dashboard  : http://localhost:%s/dashboard/\n", cfg.Port)
	fmt.Printf("🧪 Sandbox Simulator: http://localhost:%s/sandbox/\n", cfg.Port)
	fmt.Printf("🔑 Default PIN     : %s\n", cfg.AdminPIN)
	fmt.Println("==================================================================")

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: r,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server listener error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit
	log.Println("🛑 Menerima sinyal mematikan server... Menutup koneksi dengan aman...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Server shutdown error: %v", err)
	}
	log.Println("✅ Server PaymentG berhasil dimatikan secara bersih (Graceful Shutdown selesai).")
}
