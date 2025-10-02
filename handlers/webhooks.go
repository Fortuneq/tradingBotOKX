package handlers

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"okx-trading-bot/bot"
	"okx-trading-bot/types"
)

// WebhookHandlers contains HTTP handlers for webhooks and API endpoints
type WebhookHandlers struct {
	bot *bot.TradingBot
}

// NewWebhookHandlers creates a new webhook handlers instance
func NewWebhookHandlers(tradingBot *bot.TradingBot) *WebhookHandlers {
	return &WebhookHandlers{
		bot: tradingBot,
	}
}

// SetupRoutes configures HTTP routes
func (h *WebhookHandlers) SetupRoutes(router *mux.Router) {
	// Webhook endpoints
	router.HandleFunc("/tv-signal", h.handleTradingViewSignal).Methods("POST")

	// Monitoring endpoints
	router.HandleFunc("/ping", h.handlePing).Methods("GET")
	router.HandleFunc("/status", h.handleStatus).Methods("GET")
	router.HandleFunc("/positions", h.handlePositions).Methods("GET")

	// Manual trading endpoints
	router.HandleFunc("/manual-trade", h.handleManualTrade).Methods("POST")
	router.HandleFunc("/all-in", h.handleAllInTrade).Methods("POST")

	log.Println("🎯 Webhook endpoint: http://localhost:5000/tv-signal")
	log.Println("📊 Status endpoint: http://localhost:5000/status")
	log.Println("📈 Positions endpoint: http://localhost:5000/positions")
	log.Println("🔧 Manual trade endpoint: http://localhost:5000/manual-trade")
	log.Println("💥 All-in trade endpoint: http://localhost:5000/all-in")
}

// handleTradingViewSignal handles incoming TradingView webhook signals
func (h *WebhookHandlers) handleTradingViewSignal(w http.ResponseWriter, r *http.Request) {
	var signal types.TradingViewSignal

	if err := json.NewDecoder(r.Body).Decode(&signal); err != nil {
		log.Printf("❌ Invalid TradingView signal format: %v", err)
		h.sendErrorResponse(w, http.StatusBadRequest, "Invalid signal format")
		return
	}

	log.Printf("📡 Received TradingView signal: %s @ %.2f", signal.Side, signal.Price)

	// Convert side to OrderSide
	var side types.OrderSide
	switch strings.ToLower(signal.Side) {
	case "buy":
		side = types.OrderSideBuy
	case "sell":
		side = types.OrderSideSell
	default:
		log.Printf("❌ Invalid order side: %s", signal.Side)
		h.sendErrorResponse(w, http.StatusBadRequest, "Invalid order side")
		return
	}

	// Calculate position size based on risk (simplified)
	riskUSD := 10.0 // Fixed risk for webhook signals
	size := riskUSD / signal.Price
	size = float64(int(size*1000)) / 1000 // Round to 3 decimal places

	// Place the order
	success, err := h.bot.PlaceManualOrder(side, size)
	if err != nil {
		log.Printf("❌ Failed to process TradingView signal: %v", err)
		h.sendErrorResponse(w, http.StatusInternalServerError, "Failed to execute trade")
		return
	}

	if success {
		h.sendJSONResponse(w, http.StatusOK, map[string]interface{}{
			"status":  "success",
			"message": "Order placed successfully",
			"side":    side,
			"size":    size,
			"price":   signal.Price,
		})
	} else {
		h.sendErrorResponse(w, http.StatusBadRequest, "Order execution failed")
	}
}

// handlePing handles health check requests
func (h *WebhookHandlers) handlePing(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// handleStatus handles status requests
func (h *WebhookHandlers) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := h.bot.GetStatus()
	h.sendJSONResponse(w, http.StatusOK, status)
}

// handlePositions handles position information requests
func (h *WebhookHandlers) handlePositions(w http.ResponseWriter, r *http.Request) {
	positions := h.bot.GetPositions()

	// Transform positions for JSON response
	responsePositions := make([]map[string]interface{}, len(positions))
	for i, pos := range positions {
		positionData := map[string]interface{}{
			"id":         pos.ID,
			"side":       pos.Side,
			"entryPrice": pos.EntryPrice,
			"size":       pos.Size,
			"stopLoss":   pos.StopLoss,
			"takeProfit": pos.TakeProfit,
			"status":     pos.Status,
			"openTime":   pos.OpenTime.Format("2006-01-02T15:04:05Z"),
			"isTrailing": pos.IsTrailingStop,
		}

		if pos.CloseTime != nil {
			positionData["closeTime"] = pos.CloseTime.Format("2006-01-02T15:04:05Z")
		}

		if pos.ClosePrice != nil {
			positionData["closePrice"] = *pos.ClosePrice
		}

		if pos.PnL != nil {
			positionData["pnl"] = *pos.PnL
		}

		responsePositions[i] = positionData
	}

	h.sendJSONResponse(w, http.StatusOK, map[string]interface{}{
		"positions": responsePositions,
		"count":     len(positions),
	})
}

// handleManualTrade handles manual trade requests
func (h *WebhookHandlers) handleManualTrade(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Side string  `json:"side"`
		Size float64 `json:"size"`
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		log.Printf("❌ Invalid manual trade request: %v", err)
		h.sendErrorResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Validate request
	if request.Size <= 0 {
		h.sendErrorResponse(w, http.StatusBadRequest, "Size must be positive")
		return
	}

	// Convert side to OrderSide
	var side types.OrderSide
	switch strings.ToLower(request.Side) {
	case "buy":
		side = types.OrderSideBuy
	case "sell":
		side = types.OrderSideSell
	default:
		h.sendErrorResponse(w, http.StatusBadRequest, "Invalid order side")
		return
	}

	// Place the order
	var success bool
	var err error
	if h.bot.IsAllInMode() {
		// Игнорируем присланный размер, если включен AllInMode
		err = h.bot.EnterAllIn(side)
		if err != nil {
			success = false
		} else {
			success = true
		}
	} else {
		success, err = h.bot.PlaceManualOrder(side, request.Size)
	}
	if err != nil {
		log.Printf("❌ Manual trade failed: %v", err)
		h.sendErrorResponse(w, http.StatusInternalServerError, "Trade execution failed")
		return
	}

	if success {
		h.sendJSONResponse(w, http.StatusOK, map[string]interface{}{
			"status":  "success",
			"message": "Manual trade executed successfully",
			"side":    side,
			"size":    request.Size,
		})
	} else {
		h.sendErrorResponse(w, http.StatusBadRequest, "Trade execution failed")
	}
}

// handleAllInTrade handles all-in trade requests
func (h *WebhookHandlers) handleAllInTrade(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Side string  `json:"side"`
		Size float64 `json:"size"`  // включаем на всякий случай, но не используем
	}

	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		log.Printf("❌ Invalid all-in trade request: %v", err)
		h.sendErrorResponse(w, http.StatusBadRequest, "Invalid request format")
		return
	}

	// Convert side to OrderSide
	var side types.OrderSide
	switch strings.ToLower(request.Side) {
	case "buy":
		side = types.OrderSideBuy
	case "sell":
		side = types.OrderSideSell
	default:
		h.sendErrorResponse(w, http.StatusBadRequest, "Invalid order side")
		return
	}

	// Execute all-in trade
	err := h.bot.EnterAllIn(side)
	if err != nil {
		log.Printf("❌ All-in trade failed: %v", err)
		h.sendErrorResponse(w, http.StatusInternalServerError, "All-in trade execution failed")
		return
	}

	h.sendJSONResponse(w, http.StatusOK, map[string]interface{}{
		"status":  "success",
		"message": "All-in trade executed successfully",
		"side":    side,
	})
}

// sendJSONResponse sends a JSON response
func (h *WebhookHandlers) sendJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("❌ Failed to encode JSON response: %v", err)
	}
}

// sendErrorResponse sends an error response
func (h *WebhookHandlers) sendErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	h.sendJSONResponse(w, statusCode, map[string]interface{}{
		"status": "error",
		"error":  message,
	})
}

// Middleware for logging requests
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response writer that captures the status code
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(lrw, r)

		duration := time.Since(start)
		log.Printf("%s %s %d %v", r.Method, r.URL.Path, lrw.statusCode, duration)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

// Middleware for CORS headers
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// Middleware for recovering from panics
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				log.Printf("❌ Panic recovered: %v", err)
				http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			}
		}()

		next.ServeHTTP(w, r)
	})
}
