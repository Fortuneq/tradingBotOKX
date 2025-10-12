package bot

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"okx-trading-bot/client"
	"okx-trading-bot/config"
	"okx-trading-bot/marketdata"
	"okx-trading-bot/strategy"
	"okx-trading-bot/types"
)

// TradingBot orchestrates market data, strategy, and order execution
type TradingBot struct {
	config            *config.Config
	marketDataService marketdata.MarketDataService
	restClient        *client.OKXRestClient
	tradingStrategy   strategy.TradingStrategy

	// State management
	ctx       context.Context
	cancel    context.CancelFunc
	lastPrice float64
	priceMu   sync.RWMutex

	// Timers and tickers
	candlePoller   *time.Ticker
	trailingTimer  *time.Ticker
	lastCandleTime time.Time

	// Control flags
	running bool
	mu      sync.RWMutex
}

// NewTradingBot creates a new trading bot instance
func NewTradingBot(cfg *config.Config) (*TradingBot, error) {
	// Create REST client
	restClient := client.NewOKXRestClient(
		cfg.OKX.APIKey,
		cfg.OKX.APISecret,
		cfg.OKX.Passphrase,
	)

	// Create market data service
	marketDataService := marketdata.NewOKXMarketDataService(
		cfg.OKX.InstrumentID,
		cfg.OKX.Timeframe,
		cfg.OKX.APIKey,
		cfg.OKX.APISecret,
		cfg.OKX.Passphrase,
	)

	// Create trading strategy
	tradingStrategy := strategy.NewAdvancedTradingStrategy(
		marketDataService,
		restClient,
		&cfg.Strategy,
		cfg.OKX.InstrumentID,
		500, // buffer size
	)

	bot := &TradingBot{
		config:            cfg,
		marketDataService: marketDataService,
		restClient:        restClient,
		tradingStrategy:   tradingStrategy,
		lastCandleTime:    time.Time{},
	}

	// Set up event handlers
	marketDataService.SetOnPriceUpdate(bot.onPriceUpdate)
	marketDataService.SetOnCandleUpdate(bot.onCandleUpdate)
	tradingStrategy.SetOnTradeSignal(bot.onTradeSignal)

	return bot, nil
}

// Start initializes and starts the trading bot
func (bot *TradingBot) Start(ctx context.Context) error {
	bot.mu.Lock()
	if bot.running {
		bot.mu.Unlock()
		return fmt.Errorf("bot is already running")
	}
	bot.running = true
	bot.mu.Unlock()

	bot.ctx, bot.cancel = context.WithCancel(ctx)

	log.Println("🚀 Starting OKX Trading Bot...")

	// Initialize market data service
	if err := bot.marketDataService.Initialize(bot.ctx); err != nil {
		return fmt.Errorf("failed to initialize market data service: %w", err)
	}

	// Initialize trading strategy
	if err := bot.tradingStrategy.Initialize(bot.ctx); err != nil {
		return fmt.Errorf("failed to initialize trading strategy: %w", err)
	}

	// Start periodic candle polling (every 5 seconds)
	// Commented out since WebSocket already provides candle updates
	// bot.candlePoller = time.NewTicker(5 * time.Second)
	// go bot.pollCandles()

	// Start trailing stop updates (every 1 second)
	bot.trailingTimer = time.NewTicker(1 * time.Second)
	go bot.updateTrailingStopsOnExchange()

	log.Printf("✅ Trading Bot started for %s (Futures)", bot.config.OKX.InstrumentID)
	log.Printf("📊 Real-time %s candles via WebSocket", bot.config.OKX.Timeframe)
	log.Println("🔄 Trailing stops updated every 1 second")

	return nil
}

// Stop gracefully shuts down the trading bot
func (bot *TradingBot) Stop() error {
	bot.mu.Lock()
	if !bot.running {
		bot.mu.Unlock()
		return nil
	}
	bot.running = false
	bot.mu.Unlock()

	log.Println("🛑 Stopping trading bot...")

	// Cancel context
	if bot.cancel != nil {
		bot.cancel()
	}

	// Stop tickers
	if bot.candlePoller != nil {
		bot.candlePoller.Stop()
	}

	if bot.trailingTimer != nil {
		bot.trailingTimer.Stop()
	}

	// Close market data service
	if err := bot.marketDataService.Close(); err != nil {
		log.Printf("⚠️ Error closing market data service: %v", err)
	}

	log.Println("✅ Trading bot stopped")
	return nil
}

// PlaceManualOrder places a manual order via webhook
func (bot *TradingBot) PlaceManualOrder(side types.OrderSide, size float64) (bool, error) {
	ticker, err := bot.restClient.GetTicker(bot.config.OKX.InstrumentID)
	if err != nil {
		return false, fmt.Errorf("failed to get ticker: %w", err)
	}

	if ticker.Last <= 0 {
		return false, fmt.Errorf("invalid price: %f", ticker.Last)
	}

	// Determine order type based on configuration
	orderType := bot.config.Strategy.OrderType
	if orderType == "" {
		orderType = "market" // default to market if not specified
	}

	// Check for low liquidity by attempting to get order book
	orderBook, err := bot.restClient.GetOrderBook(bot.config.OKX.InstrumentID, 1) // Get top of book
	lowLiquidity := err != nil || len(orderBook.Asks) == 0 || len(orderBook.Bids) == 0

	// If order book is empty or error occurred, it's low liquidity situation
	if lowLiquidity && orderType == "limit" {
		log.Printf("⚠️ Low liquidity detected - switching to market order for %s", side)
		orderType = "market" // Force market order when liquidity is low
	}

	// Calculate limit price if order type is limit
	var price *float64 = nil
	if orderType == "limit" {
		// Get order book to determine best available prices
		if err != nil || len(orderBook.Asks) == 0 || len(orderBook.Bids) == 0 {
			log.Printf("⚠️ Could not get order book, using calculated price: %v", err)
			// Fallback: calculate limit price depending on order side for immediate fill:
			// - For buy orders: place slightly above market to get filled immediately
			// - For sell orders: place slightly below market to get filled immediately
			limitPrice := ticker.Last
			if side == types.OrderSideBuy {
				// For buy orders, place slightly above current market to be filled immediately
				limitPrice = ticker.Last * 1.001 // 0.1% above market
			} else {
				// For sell orders, place slightly below current market to be filled immediately
				limitPrice = ticker.Last * 0.999 // 0.1% below market
			}
			price = &limitPrice
		} else {
			// Use prices from the order book
			if side == types.OrderSideBuy {
				// For buy orders, use the best ask price (lowest sell price)
				askPriceStr := orderBook.Asks[0][0] // First element is price
				askPrice, err := strconv.ParseFloat(askPriceStr, 64)
				if err != nil {
					log.Printf("⚠️ Could not parse ask price, using calculated price: %v", err)
					limitPrice := ticker.Last * 1.001
					price = &limitPrice
				} else {
					// Place slightly above the best ask to ensure execution
					limitPrice := askPrice * 1.0005 // 0.05% above best ask
					price = &limitPrice
				}
			} else {
				// For sell orders, use the best bid price (highest buy price)
				bidPriceStr := orderBook.Bids[0][0] // First element is price
				bidPrice, err := strconv.ParseFloat(bidPriceStr, 64)
				if err != nil {
					log.Printf("⚠️ Could not parse bid price, using calculated price: %v", err)
					limitPrice := ticker.Last * 0.999
					price = &limitPrice
				} else {
					// Place slightly below the best bid to ensure execution
					limitPrice := bidPrice * 0.9995 // 0.05% below best bid
					price = &limitPrice
				}
			}
		}

		// Проверяем минимальный номинал для лимитных ордеров
		// OKX требует минимальный номинал ~20 USDT для лимитных ордеров на фьючерсах
		minNotional := 20.0       // OKX лимит для QTUM-USDT-SWAP
		notional := size * *price // номинал = размер * цена

		if notional < minNotional {
			log.Printf("⚠️  Лимитный номинал %.2f < %.2f USDT – переключаемся на рыночный", notional, minNotional)
			orderType = "market" // переключаемся на рыночный ордер
			price = nil          // у рыночного ордера цена не указывается
		} else {
			log.Printf("📊 Manual limit price calculated: %.4f for %s order (nominal: %.2f USDT)", *price, side, notional)
		}
	}

	// Determine posSide based on order side
	posSide := "long"
	if side == types.OrderSideSell {
		posSide = "short"
	}

	order := &types.OrderRequest{
		InstID:    bot.config.OKX.InstrumentID,
		TdMode:    "cross", // Cross margin mode for futures trading
		Side:      side,
		PosSide:   posSide, // Position side: long/short
		OrderType: orderType,
		Size:      size,
		Price:     price,
		ClOrdID:   fmt.Sprintf("bot%d%04d", time.Now().Unix(), rand.Intn(10000)),
	}

	response, err := bot.restClient.PlaceOrder(order)
	if err != nil {
		return false, fmt.Errorf("failed to place order: %w", err)
	}

	if response.Code == "0" {
		log.Printf("✅ Manual order: %s %.3f %s", side, size, bot.config.OKX.InstrumentID)
		return true, nil
	}

	log.Printf("❌ Manual order error: %s", response.Message)
	return false, fmt.Errorf("order failed: %s", response.Message)
}

// GetStatus returns the current status of the trading bot
func (bot *TradingBot) GetStatus() map[string]interface{} {
	positions := bot.tradingStrategy.GetPositions()
	activeCount := 0

	for _, pos := range positions {
		if pos.Status == types.PositionStatusOpen {
			activeCount++
		}
	}

	bot.priceMu.RLock()
	currentPrice := bot.lastPrice
	bot.priceMu.RUnlock()

	return map[string]interface{}{
		"status":          "running",
		"instrument":      bot.config.OKX.InstrumentID,
		"currentPrice":    currentPrice,
		"activePositions": activeCount,
		"totalPositions":  len(positions),
		"lastUpdate":      time.Now().Format(time.RFC3339),
	}
}

// GetPositions returns all trading positions
func (bot *TradingBot) GetPositions() []*types.Position {
	return bot.tradingStrategy.GetPositions()
}

// onPriceUpdate handles real-time price updates
func (bot *TradingBot) onPriceUpdate(price float64) {
	bot.priceMu.Lock()
	bot.lastPrice = price
	bot.priceMu.Unlock()
}

// onCandleUpdate handles real-time candle updates from WebSocket
func (bot *TradingBot) onCandleUpdate(candle *types.Candle) {
	// Skip if the candle has zero values (malformed or temporary data)
	if candle.Close == 0 || candle.Volume == 0 {
		log.Printf("⚠️ Received zero candle, skipping: O:%.2f H:%.2f L:%.2f C:%.2f V:%.1f",
			candle.Open, candle.High, candle.Low, candle.Close, candle.Volume)
		return
	}

	// Update price from candle close
	bot.priceMu.Lock()
	bot.lastPrice = candle.Close
	bot.priceMu.Unlock()

	// Skip if this is the same candle as before
	if candle.Timestamp.Equal(bot.lastCandleTime) {
		return
	}

	bot.lastCandleTime = candle.Timestamp
	log.Printf("📊 New %s candle: O:%.2f H:%.2f L:%.2f C:%.2f V:%.1f",
		bot.config.OKX.Timeframe, candle.Open, candle.High, candle.Low, candle.Close, candle.Volume)

	// Process the new candle through the strategy
	bot.tradingStrategy.Update(candle)
}

// onTradeSignal handles trading signals from the strategy
func (bot *TradingBot) onTradeSignal(signal *types.TradeSignal) error {
	if signal.Size <= 0 {
		return nil
	}

	log.Printf("📈 Trade Signal: %s %.3f @ %.2f (Strategy: %s)",
		signal.Side, signal.Size, signal.EntryPrice, signal.Strategy)

	// The signal execution is handled within the strategy itself
	// This is just for logging and monitoring
	return nil
}

// WebSocketSilentTimeout определяет максимальное время ожидания новой свечи через WebSocket
const WebSocketSilentTimeout = 30 * time.Second

// pollCandles periodically fetches and processes new candle data
func (bot *TradingBot) pollCandles() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("❌ Panic in candle poller: %v", r)
		}
	}()

	for {
		select {
		case <-bot.ctx.Done():
			return
		case <-bot.candlePoller.C:
			// если последняя свеча старше 6 мин – берём из REST (это означает, что WebSocket действительно сломался)
			bot.priceMu.RLock()
			last := bot.lastCandleTime
			bot.priceMu.RUnlock()

			if time.Since(last) > WebSocketSilentTimeout {
				log.Printf("⏳ WebSocket тихо > %v – запрашиваем свечу по REST", WebSocketSilentTimeout)
				bot.fetchAndProcessCandles()
			}
		}
	}
}

// fetchAndProcessCandles fetches the latest candle and processes it
func (bot *TradingBot) fetchAndProcessCandles() {
	candles, err := bot.marketDataService.GetHistoricalData(1)
	if err != nil {
		log.Printf("❌ Failed to fetch candles: %v", err)
		return
	}

	if len(candles) == 0 {
		return
	}

	latestCandle := candles[len(candles)-1]

	// Skip if this is the same candle as before
	if latestCandle.Timestamp.Equal(bot.lastCandleTime) {
		return
	}

	bot.lastCandleTime = latestCandle.Timestamp

	// Process the new candle through the strategy
	bot.tradingStrategy.Update(latestCandle)
}

// updateTrailingStopsOnExchange updates trailing stops directly on the exchange
func (bot *TradingBot) updateTrailingStopsOnExchange() {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("❌ Panic in trailing stop updater: %v", r)
		}
	}()

	for {
		select {
		case <-bot.ctx.Done():
			return
		case <-bot.trailingTimer.C:
			bot.processTrailingStops()
		}
	}
}

// processTrailingStops processes trailing stop updates
func (bot *TradingBot) processTrailingStops() {
	bot.priceMu.RLock()
	currentPrice := bot.lastPrice
	bot.priceMu.RUnlock()

	if currentPrice <= 0 {
		return // No price data yet
	}

	positions := bot.tradingStrategy.GetPositions()
	for _, pos := range positions {
		if pos.Status != types.PositionStatusOpen || !pos.IsTrailingStop {
			continue
		}

		// Calculate new stop level
		var newStop float64
		if pos.Side == types.OrderSideBuy {
			newStop = currentPrice * (1 - bot.config.Strategy.TrailingPercent/100.0)
		} else {
			newStop = currentPrice * (1 + bot.config.Strategy.TrailingPercent/100.0)
		}

		// Only update if the new stop is better
		shouldUpdate := false
		if pos.Side == types.OrderSideBuy && newStop > pos.StopLoss {
			shouldUpdate = true
		} else if pos.Side == types.OrderSideSell && newStop < pos.StopLoss {
			shouldUpdate = true
		}

		if !shouldUpdate || newStop == pos.StopLoss {
			continue
		}

		// Cancel existing algo order and place new one
		if pos.StopLossAlgoID != "" {
			if success, err := bot.restClient.CancelAlgoOrder(pos.StopLossAlgoID, bot.config.OKX.InstrumentID); err != nil || !success {
				log.Printf("⚠️ Failed to cancel trailing stop: %v", err)
				continue
			}
		}

		// Place new trailing stop
		var closeSide types.OrderSide
		if pos.Side == types.OrderSideBuy {
			closeSide = types.OrderSideSell
		} else {
			closeSide = types.OrderSideBuy
		}

		var posSide string
		if closeSide == types.OrderSideBuy {
			posSide = "long"   // When closing short position, we buy (long)
		} else {
			posSide = "short"  // When closing long position, we sell (short)
		}
		
		algoOrder := &types.AlgoOrderRequest{
			InstID:      bot.config.OKX.InstrumentID,
			TdMode:      "isolated", // Use isolated mode to avoid cross margin issues
			Side:        closeSide,
			PosSide:     posSide, // Use correct position side
			Size:        pos.Size,
			SlTriggerPx: newStop,  // Use the correct field
			SlOrdPx:     "-1",     // Market order for stop loss execution
			OrderType:   "conditional",
			ClOrdID:     fmt.Sprintf("trail%d%04d", time.Now().Unix(), rand.Intn(10000)),
		}

		response, err := bot.restClient.PlaceAlgoOrder(algoOrder)
		if err != nil || response.Code != "0" {
			log.Printf("⚠️ Failed to place new trailing stop: %v", err)
			continue
		}

		// Update position with new algo ID (this would need to be implemented in strategy)
		log.Printf("✅ Updated trailing stop for %s: %.2f -> %.2f", pos.ID, pos.StopLoss, newStop)
	}
}

// IsRunning returns whether the bot is currently running
func (bot *TradingBot) IsRunning() bool {
	bot.mu.RLock()
	defer bot.mu.RUnlock()
	return bot.running
}

// GetLastPrice returns the last received price
func (bot *TradingBot) GetLastPrice() float64 {
	bot.priceMu.RLock()
	defer bot.priceMu.RUnlock()
	return bot.lastPrice
}

// IsAllInMode returns whether AllInMode is enabled
func (bot *TradingBot) IsAllInMode() bool {
	return bot.config.Strategy.AllInMode
}

// EnterAllIn executes an all-in order based on configuration
func (bot *TradingBot) EnterAllIn(side types.OrderSide) error {
	if !bot.config.Strategy.AllInMode {
		return fmt.Errorf("AllInMode disabled")
	}
	return bot.tradingStrategy.EnterAllIn(side)
}
