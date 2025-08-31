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
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"okx-trading-bot/client"
	"okx-trading-bot/types"
)

const (
	WebSocketURL   = "wss://ws.okx.com:8443/ws/v5/public"
	PingInterval   = 25 * time.Second
	ReconnectDelay = 5 * time.Second
	MaxRetries     = 5
)

// MarketDataService interface defines market data operations
type MarketDataService interface {
	Initialize(ctx context.Context) error
	GetHistoricalData(limit int) ([]*types.Candle, error)
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
	onPriceUpdate  func(float64)

	// Control channels
	ctx        context.Context
	cancel     context.CancelFunc
	pingTicker *time.Ticker

	// State management
	mu         sync.RWMutex
	retryCount int
	connected  bool
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

// Initialize starts the WebSocket connection and data streams
func (s *OKXMarketDataService) Initialize(ctx context.Context) error {
	s.ctx, s.cancel = context.WithCancel(ctx)

	if err := s.connect(); err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}

	return nil
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

	// Authenticate and subscribe
	if err := s.authenticate(); err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

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
	subscribeMsg := map[string]interface{}{
		"op": "subscribe",
		"args": []map[string]interface{}{
			{
				"channel": "tickers",
				"instId":  s.instrumentID,
			},
		},
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
			// Handle ticker data
			if channel == "tickers" {
				if data, ok := msg["data"].([]interface{}); ok && len(data) > 0 {
					if tickerData, ok := data[0].(map[string]interface{}); ok {
						if lastStr, ok := tickerData["last"].(string); ok {
							if lastPrice, err := strconv.ParseFloat(lastStr, 64); err == nil && s.onPriceUpdate != nil {
								s.onPriceUpdate(lastPrice)
							}
						}
					}
				}
			}
			// Handle candle data
			if channel == "candle"+s.timeframe {
				if data, ok := msg["data"].([]interface{}); ok && len(data) > 0 {
					if candleArray, ok := data[0].([]interface{}); ok && len(candleArray) >= 6 {
						// Convert to string array for parsing
						candleStrings := make([]string, len(candleArray))
						for i, v := range candleArray {
							candleStrings[i] = fmt.Sprintf("%v", v)
						}

						// Create candle from WebSocket data
						if candle, err := types.NewCandleFromRaw(candleStrings); err == nil && s.onCandleUpdate != nil {
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

// sendPing sends a ping message
func (s *OKXMarketDataService) sendPing() {
	s.mu.RLock()
	if !s.connected {
		s.mu.RUnlock()
		return
	}
	s.mu.RUnlock()

	// Note: OKX WebSocket doesn't require explicit ping, connection is kept alive automatically
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
