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

	"github.com/gorilla/mux"

	"okx-trading-bot/bot"
	"okx-trading-bot/config"
	"okx-trading-bot/handlers"
)

func main() {
	log.Println("🚀 Starting OKX Trading Bot")

	// Load configuration
	cfg, err := config.LoadConfig("appsettings.json")
	if err != nil {
		log.Fatalf("❌ Failed to load configuration: %v", err)
	}

	log.Printf("📊 Configuration loaded - Instrument: %s, Timeframe: %s",
		cfg.OKX.InstrumentID, cfg.OKX.Timeframe)
	log.Printf("⚙️ Strategy - Risk: %.1f%%, RSI Length: %d, ATR Length: %d",
		cfg.Strategy.RiskPercent, cfg.Strategy.RSILength, cfg.Strategy.ATRLength)

	// Create trading bot
	tradingBot, err := bot.NewTradingBot(cfg)
	if err != nil {
		log.Fatalf("❌ Failed to create trading bot: %v", err)
	}

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the trading bot
	if err := tradingBot.Start(ctx); err != nil {
		log.Fatalf("❌ Failed to start trading bot: %v", err)
	}

	// Set up HTTP server
	router := mux.NewRouter()

	// Add middleware
	router.Use(handlers.LoggingMiddleware)
	router.Use(handlers.CORSMiddleware)
	router.Use(handlers.RecoveryMiddleware)

	// Set up webhook handlers
	webhookHandlers := handlers.NewWebhookHandlers(tradingBot)
	webhookHandlers.SetupRoutes(router)

	// Create HTTP server
	server := &http.Server{
		Addr:         ":5500",
		Handler:      router,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// Start HTTP server in a goroutine
	go func() {
		log.Println("🌐 Starting HTTP server on :5500")
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("❌ HTTP server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("✅ OKX Trading Bot is running...")
	log.Println("📝 Available endpoints:")
	log.Println("   • POST /tv-signal     - TradingView webhook")
	log.Println("   • GET  /status        - Bot status")
	log.Println("   • GET  /positions     - Trading positions")
	log.Println("   • GET  /ping          - Health check")
	log.Println("   • POST /manual-trade  - Manual trade execution")
	log.Println()
	log.Println("🛑 Press Ctrl+C to stop the bot")

	// Wait for signal
	<-sigChan
	log.Println("🛑 Shutdown signal received, stopping bot...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop HTTP server
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("⚠️ HTTP server shutdown error: %v", err)
	}

	// Stop trading bot
	if err := tradingBot.Stop(); err != nil {
		log.Printf("⚠️ Trading bot stop error: %v", err)
	}

	log.Println("✅ OKX Trading Bot stopped gracefully")
}

// printBanner prints the application banner
func printBanner() {
	banner := `
╔═══════════════════════════════════════════════════════════════╗
║                      OKX Trading Bot                          ║
║                   RSI + VWAP Strategy                         ║
║                                                               ║
║  🎯 Automated cryptocurrency trading on OKX exchange         ║
║  📊 Technical analysis with RSI and VWAP indicators          ║
║  ⚡ Real-time WebSocket data processing                       ║
║  🛡️ Advanced risk management and position sizing             ║
║                                                               ║
╚═══════════════════════════════════════════════════════════════╝
`
	fmt.Print(banner)
}

// validateConfiguration validates the loaded configuration
func validateConfiguration(cfg *config.Config) error {
	if cfg.OKX.APIKey == "" {
		return fmt.Errorf("OKX API key is required")
	}

	if cfg.OKX.APISecret == "" {
		return fmt.Errorf("OKX API secret is required")
	}

	if cfg.OKX.Passphrase == "" {
		return fmt.Errorf("OKX passphrase is required")
	}

	if cfg.OKX.InstrumentID == "" {
		return fmt.Errorf("OKX instrument ID is required")
	}

	if cfg.Strategy.RiskPercent <= 0 || cfg.Strategy.RiskPercent > 100 {
		return fmt.Errorf("risk percent must be between 0 and 100")
	}

	if cfg.Strategy.RSILength <= 0 {
		return fmt.Errorf("RSI length must be positive")
	}

	if cfg.Strategy.ATRLength <= 0 {
		return fmt.Errorf("ATR length must be positive")
	}

	return nil
}
