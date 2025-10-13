package marketdata

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"okx-trading-bot/client"
	"okx-trading-bot/types"
)

const (
	WebSocketURL   = "wss://ws.okx.com:8443/ws/v5/business"
	PingInterval   = 25 * time.Second
	ReconnectDelay = 5 * time.Second
	MaxRetries     = 5
)

// MarketDataService interface defines market data operations
type MarketDataService interface {
	Initialize(ctx context.Context) error
	GetHistoricalData(limit int) ([]*types.Candle, error)
	GetHistoricalDataForTimeframe(limit int, timeframe string) ([]*types.Candle, error)
	SetOnCandleUpdateForTimeframe(func(*types.Candle, string))
	SetOn30mCandle(func(*types.Candle))
	Close() error
}

// OKXMarketDataService handles WebSocket connections to OKX
type OKXMarketDataService struct {
	instrumentID string
	timeframe    string
	apiKey       string
	apiSecret    string
	passphrase   string

	conn       *websocket.Conn
	restClient *client.OKXRestClient

	// Event handlers
	onCandleUpdate func(*types.Candle)
	onCandleUpdateForTimeframe func(*types.Candle, string)
	on30mCandle func(*types.Candle)
	onPriceUpdate  func(float64)

	// Control channels
	ctx        context.Context
	cancel     context.CancelFunc
	pingTicker *time.Ticker

	// State management
	mu              sync.RWMutex
	retryCount      int
	connected       bool
	zeroCandleCount int
}

// NewOKXMarketDataService creates a new market data service
func NewOKXMarketDataService(instrumentID, timeframe, apiKey, apiSecret, passphrase string) *OKXMarketDataService {
	return &OKXMarketDataService{
		instrumentID: instrumentID,
		timeframe:    timeframe,
		apiKey:       apiKey,
		apiSecret:    apiSecret,
		passphrase:   passphrase,
		restClient:   client.NewOKXRestClient(apiKey, apiSecret, passphrase),
	}
}

// SetOnCandleUpdate sets the callback for candle updates
func (s *OKXMarketDataService) SetOnCandleUpdate(callback func(*types.Candle)) {
	s.onCandleUpdate = callback
}

// SetOnPriceUpdate sets the callback for price updates
func (s *OKXMarketDataService) SetOnPriceUpdate(callback func(float64)) {
	s.onPriceUpdate = callback
}

// SetOnCandleUpdateForTimeframe sets the callback for candle updates for specific timeframe
func (s *OKXMarketDataService) SetOnCandleUpdateForTimeframe(callback func(*types.Candle, string)) {
	s.onCandleUpdateForTimeframe = callback
}

// SetOn30mCandle sets the callback for 30-minute candle updates
func (s *OKXMarketDataService) SetOn30mCandle(cb func(*types.Candle)) {
	s.on30mCandle = cb
}

// Initialize starts the WebSocket connection and data streams
func (s *OKXMarketDataService) Initialize(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	if err := s.connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	return nil
}

// GetHistoricalDataForTimeframe retrieves historical candle data for a specific timeframe via REST API
func (s *OKXMarketDataService) GetHistoricalDataForTimeframe(limit int, timeframe string) ([]*types.Candle, error) {
	return s.restClient.GetHistoricalCandles(s.instrumentID, timeframe, limit)
}

// GetHistoricalData retrieves historical candle data via REST API
func (s *OKXMarketDataService) GetHistoricalData(limit int) ([]*types.Candle, error) {
	return s.restClient.GetHistoricalCandles(s.instrumentID, s.timeframe, limit)
}

// connect establishes WebSocket connection
func (s *OKXMarketDataService) connect() error {
	u, err := url.Parse(WebSocketURL)
	if err != nil {
		return fmt.Errorf("invalid WebSocket URL: %w", err)
	}

	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to dial WebSocket: %w", err)
	}

	s.mu.Lock()
	s.conn = conn
	s.connected = true
	s.retryCount = 0
	s.mu.Unlock()

	// Start ping ticker
	s.pingTicker = time.NewTicker(PingInterval)

	// Subscribe to public channels (no authentication needed for public channels)
	if err := s.subscribe(); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}

	// Start message processing
	go s.processMessages()
	go s.handlePing()

	log.Printf("✅ WebSocket connected to OKX (%s, %s)", s.instrumentID, s.timeframe)
	return nil
}

// authenticate sends authentication message
func (s *OKXMarketDataService) authenticate() error {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := s.generateSignature(timestamp, "GET", "/users/self/verify", "")

	authMsg := map[string]interface{}{
		"op": "login",
		"args": []map[string]interface{}{
			{
				"apiKey":     s.apiKey,
				"passphrase": s.passphrase,
				"timestamp":  timestamp,
				"sign":       signature,
			},
		},
	}

	return s.sendMessage(authMsg)
}

// subscribe sends subscription message
func (s *OKXMarketDataService) subscribe() error {
	// Subscribe to candle channel only (tickers channel is not supported for futures)
	channel := "candle" + strings.ToLower(strings.TrimSpace(s.timeframe))
	instId := strings.ToUpper(strings.TrimSpace(s.instrumentID))

	subscribeArgs := []map[string]interface{}{
		{
			"channel": channel,
			"instId":  instId,
		},
	}

	// Subscribe to 30-minute channel if current timeframe is not 30m
	if s.timeframe != "30m" {
		subscribeArgs = append(subscribeArgs, map[string]interface{}{
			"channel": "candle30m",
			"instId":  instId,
		})
	}

	subscribeMsg := map[string]interface{}{
		"op":   "subscribe",
		"args": subscribeArgs,
	}

	log.Printf("WebSocket subscribing: %+v", subscribeMsg)
	return s.sendMessage(subscribeMsg)
}

// processMessages handles incoming WebSocket messages
func (s *OKXMarketDataService) processMessages() {
	defer s.handleReconnection()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			var msg map[string]interface{}
			if err := s.conn.ReadJSON(&msg); err != nil {
				log.Printf("❌ Error reading message: %v", err)
				return
			}

			s.processMessage(msg)
		}
	}
}

// processMessage processes individual WebSocket messages
func (s *OKXMarketDataService) processMessage(msg map[string]interface{}) {
	// Handle errors
	if event, ok := msg["event"].(string); ok && event == "error" {
		if msgText, ok := msg["msg"].(string); ok {
			log.Printf("⚠️ WebSocket error: %s", msgText)
		}
		return
	}

	// Handle data messages
	if arg, ok := msg["arg"].(map[string]interface{}); ok {
		if channel, ok := arg["channel"].(string); ok {
			// Handle 30-minute candle data
			if channel == "candle30m" {
				if data, ok := msg["data"].([]interface{}); ok && len(data) > 0 {
					if candleArray, ok := data[0].([]interface{}); ok && len(candleArray) >= 6 {
						// Convert to string array for parsing
						candleStrings := make([]string, len(candleArray))
						for i, v := range candleArray {
							candleStrings[i] = fmt.Sprintf("%v", v)
						}

						// Create 30-minute candle from WebSocket data
						if candle, err := types.NewCandleFromRaw(candleStrings); err == nil {
							// Проверяем, является ли свеча нулевой
							if candle.Close == 0 || candle.Volume == 0 {
								s.mu.Lock()
								s.zeroCandleCount++
								log.Printf("⚠️ Zero 30m candle #%d подряд", s.zeroCandleCount)
								if s.zeroCandleCount >= 5 {
									log.Printf("🔄 5 zero-30m-candle подряд – перезапускаем WebSocket")
									s.zeroCandleCount = 0
									s.mu.Unlock()
									go s.handleReconnection() // не блокируем обработку
									return
								}
								s.mu.Unlock()
								return
							}
							// Сброс счётчика при появлении нормальной свечи
							s.mu.Lock()
							s.zeroCandleCount = 0
							s.mu.Unlock()

							// не прокидываем «живые» тики - только завершенные свечи
							if !candle.IsCompleted() {
								return
							}

							// Call specific 30-minute candle handler if available
							if s.onCandleUpdateForTimeframe != nil {
								s.onCandleUpdateForTimeframe(candle, "30m")
							}

							// Call dedicated 30m candle handler if available
							if s.on30mCandle != nil {
								s.on30mCandle(candle)
							}

							// Also trigger price update with the close price from the candle
							if s.onPriceUpdate != nil {
								s.onPriceUpdate(candle.Close)
							}
						}
					}
				}
				return
			}

			// Handle candle data for the main timeframe
			if channel == "candle"+strings.ToLower(s.timeframe) {
				if data, ok := msg["data"].([]interface{}); ok && len(data) > 0 {
					if candleArray, ok := data[0].([]interface{}); ok && len(candleArray) >= 6 {
						// Convert to string array for parsing
						candleStrings := make([]string, len(candleArray))
						for i, v := range candleArray {
							candleStrings[i] = fmt.Sprintf("%v", v)
						}

						// Create candle from WebSocket data
						if candle, err := types.NewCandleFromRaw(candleStrings); err == nil && s.onCandleUpdate != nil {
							// Проверяем, является ли свеча нулевой
							if candle.Close == 0 || candle.Volume == 0 {
								s.mu.Lock()
								s.zeroCandleCount++
								log.Printf("⚠️ Zero candle #%d подряд", s.zeroCandleCount)
								if s.zeroCandleCount >= 5 {
									log.Printf("🔄 5 zero-candle подряд – перезапускаем WebSocket")
									s.zeroCandleCount = 0
									s.mu.Unlock()
									go s.handleReconnection() // не блокируем обработку
									return
								}
								s.mu.Unlock()
								return
							}
							// Сброс счётчика при появлении нормальной свечи
							s.mu.Lock()
							s.zeroCandleCount = 0
							s.mu.Unlock()

							// Also trigger price update with the close price from the candle
							if s.onPriceUpdate != nil {
								s.onPriceUpdate(candle.Close)
							}

							s.onCandleUpdate(candle)
						}
					}
				}
			}
		}
	}
}

// handlePing sends periodic ping messages
func (s *OKXMarketDataService) handlePing() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.pingTicker.C:
			s.sendPing()
		}
	}
}

// sendPing sends a keep-alive subscription message
func (s *OKXMarketDataService) sendPing() {
	s.mu.RLock()
	if !s.connected || s.conn == nil {
		s.mu.RUnlock()
		return
	}
	instId := strings.ToUpper(s.instrumentID)
	s.mu.RUnlock()

	// Переподписываемся на ту же свечу – OKX считает это активностью
	ping := map[string]interface{}{
		"op": "subscribe",
		"args": []map[string]interface{}{
			{"channel": "candle" + strings.ToLower(s.timeframe), "instId": instId},
		},
	}
	if err := s.conn.WriteJSON(ping); err != nil {
		log.Printf("❌ keep-alive send error: %v", err)
	}
}

// handleReconnection handles reconnection logic
func (s *OKXMarketDataService) handleReconnection() {
	s.mu.Lock()
	s.connected = false
	s.mu.Unlock()

	if s.retryCount >= MaxRetries {
		log.Printf("❌ Max retry attempts reached")
		return
	}

	s.retryCount++
	log.Printf("⏳ Reconnection attempt %d/%d...", s.retryCount, MaxRetries)

	time.Sleep(ReconnectDelay * time.Duration(s.retryCount))

	if err := s.connect(); err != nil {
		log.Printf("❌ Reconnection failed: %v", err)
		s.handleReconnection()
	}
}

// sendMessage sends a message to the WebSocket
func (s *OKXMarketDataService) sendMessage(msg interface{}) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if !s.connected || s.conn == nil {
		return fmt.Errorf("WebSocket not connected")
	}

	return s.conn.WriteJSON(msg)
}

// generateSignature generates HMAC-SHA256 signature for authentication
func (s *OKXMarketDataService) generateSignature(timestamp, method, endpoint, body string) string {
	prehash := timestamp + method + endpoint + body
	h := hmac.New(sha256.New, []byte(s.apiSecret))
	h.Write([]byte(prehash))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// Close closes the WebSocket connection and cleans up resources
func (s *OKXMarketDataService) Close() error {
	if s.cancel != nil {
		s.cancel()
	}

	if s.pingTicker != nil {
		s.pingTicker.Stop()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.conn != nil {
		err := s.conn.Close()
		s.conn = nil
		s.connected = false
		return err
	}

	return nil
}
