package strategy

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"strconv"
	"sync"
	"time"

	"okx-trading-bot/client"
	"okx-trading-bot/config"
	"okx-trading-bot/marketdata"
	"okx-trading-bot/types"
)

// TradingStrategy interface defines trading strategy operations
type TradingStrategy interface {
	Initialize(ctx context.Context) error
	Update(candle *types.Candle)
	GetPositions() []*types.Position
	GenerateSignal(side types.OrderSide, candle *types.Candle, atr, vwap float64, strategyName string) error
	// All-In точка входа
	EnterAllIn(side types.OrderSide) error
}

// AdvancedTradingStrategy implements RSI + VWAP rejection strategy
type AdvancedTradingStrategy struct {
	marketDataService marketdata.MarketDataService
	restClient        *client.OKXRestClient
	config            *config.StrategyConfig
	instrumentID      string

	// Circular buffers for historical data
	candleBuffer *CircularBuffer[*types.Candle]
	priceBuffer  *CircularBuffer[float64]

	// Position management
	activePositions map[string]*types.Position
	closedPositions []*types.Position
	positionsMu     sync.RWMutex

	// Strategy parameters
	rsiLength           int
	atrLength           int
	minVolume           float64
	riskPercent         float64
	rewardRatio         float64
	atrMultiplier       float64
	trailingStopPercent float64
	maxPositionSize     float64
	positionTimeout     time.Duration
	rsiOverbought       float64
	rsiOversold         float64
	cooldownBars        int

	// State management
	currentPrice       float64
	lastSignalBarIndex *int
	isProcessingSignal bool
	processingMu       sync.Mutex
	useTrailing        bool
	trailingPercent    float64

	// RSI state tracking
	prevRSI        float64 // значение RSI на предыдущей свече
	prevCrossOver  bool    // уже было пересечение вверх (чтоб не дублировать)
	prevCrossUnder bool    // уже было пересечение вниз (чтоб не дублировать)

	// Event handlers
	onTradeSignal func(*types.TradeSignal) error
}

// NewAdvancedTradingStrategy creates a new advanced trading strategy
func NewAdvancedTradingStrategy(
	marketDataService marketdata.MarketDataService,
	restClient *client.OKXRestClient,
	cfg *config.StrategyConfig,
	instrumentID string,
	bufferSize int,
) *AdvancedTradingStrategy {
	return &AdvancedTradingStrategy{
		marketDataService: marketDataService,
		restClient:        restClient,
		config:            cfg,
		instrumentID:      instrumentID,
		candleBuffer:      NewCircularBuffer[*types.Candle](bufferSize),
		priceBuffer:       NewCircularBuffer[float64](bufferSize),
		activePositions:   make(map[string]*types.Position),
		closedPositions:   make([]*types.Position, 0),

		// Strategy parameters
		rsiLength:           cfg.RSILength,
		atrLength:           cfg.ATRLength,
		minVolume:           0.0, // Убираем фильтр по объему, чтобы поддерживать все инструменты
		riskPercent:         cfg.RiskPercent,
		rewardRatio:         cfg.RewardRatio,
		atrMultiplier:       1.5,
		trailingStopPercent: cfg.TrailingPercent,
		maxPositionSize:     100000.0,
		positionTimeout:     4 * time.Hour,
		rsiOverbought:       getOrDefault(cfg.RsiOverbought, 80.0),
		rsiOversold:         getOrDefault(cfg.RsiOversold, 20.0),
		cooldownBars:        getOrDefaultInt(cfg.CooldownBars, 2), // Используем точное значение из оригинального скрипта
		useTrailing:         cfg.UseTrailingStop,
		trailingPercent:     cfg.TrailingPercent,
	}
}

// Helper functions to provide default values
func getOrDefault(value, defaultValue float64) float64 {
	if value == 0 {
		return defaultValue
	}
	return value
}

func getOrDefaultInt(value, defaultValue int) int {
	if value == 0 {
		return defaultValue
	}
	return value
}

// SetOnTradeSignal sets the callback for trade signals
func (s *AdvancedTradingStrategy) SetOnTradeSignal(callback func(*types.TradeSignal) error) {
	s.onTradeSignal = callback
}

// Initialize initializes the strategy with historical data
func (s *AdvancedTradingStrategy) Initialize(ctx context.Context) error {
	log.Println("Initializing trading strategy...")

	historicalData, err := s.marketDataService.GetHistoricalData(50)
	if err != nil {
		return fmt.Errorf("failed to get historical data: %w", err)
	}

	for i, candle := range historicalData {
		// ⚠️  ПРОПУСКАЕМ последнюю свечу, если она ещё не закрыта
		if i == len(historicalData)-1 && !candle.IsCompleted() {
			log.Printf("⚠️  Skip incomplete warm-up candle: %v", candle.Timestamp)
			continue
		}
		s.candleBuffer.PushBack(candle)
	}

	log.Printf("📊 Loaded %d historical candles", s.candleBuffer.Size())

	// Check for any existing positions on the exchange
	log.Println("🔍 Checking for existing positions on exchange...")
	positions, err := s.restClient.GetPositions("SWAP")
	if err != nil {
		log.Printf("⚠️ Could not fetch positions from exchange: %v", err)
	} else {
		log.Printf("📊 Found %d positions on exchange", len(positions))
		for _, pos := range positions {
			if pos.InstID == s.instrumentID && pos.Size > 0 {
				log.Printf("📋 Exchange position - InstID: %s, Size: %.3f, Side: %s", pos.InstID, pos.Size, pos.Side)
				// If there's an active position on exchange, add it to our internal state
				// This helps maintain consistency if the bot was restarted
				internalPos := &types.Position{
					ID:         fmt.Sprintf("exchange_%d", time.Now().UnixNano()),
					Side:       pos.Side,
					Size:       pos.Size,
					EntryPrice: pos.AvgPrice,
					Status:     types.PositionStatusOpen,
					OpenTime:   time.Now(),
				}

				s.positionsMu.Lock()
				s.activePositions[internalPos.ID] = internalPos
				s.positionsMu.Unlock()

				log.Printf("📌 Added exchange position to internal state")
			}
		}
	}

	return nil
}

// Update processes new candle data and generates trading signals
func (s *AdvancedTradingStrategy) Update(candle *types.Candle) {
	// Skip if the candle has zero values (malformed or temporary data)
	if candle.Close == 0 || candle.Volume == 0 {
		log.Printf("⚠️ Zero candle in strategy, skipping: O:%.2f H:%.2f L:%.2f C:%.2f V:%.1f",
			candle.Open, candle.High, candle.Low, candle.Close, candle.Volume)
		// Update position management even if not processing signals
		s.checkStopConditions(candle.Close)
		s.updateTrailingStops(candle.Close)
		s.checkPositionTimeouts()
		// Update RSI state for next iteration
		s.prevRSI = 0 // Reset RSI state
		return
	}

	s.currentPrice = candle.Close

	// Still add to buffer for indicator calculations
	s.candleBuffer.PushBack(candle)

	// Calculate indicators using all candles (including current)
	candles := s.candleBuffer.ToSlice()
	vwap := s.calculateDailyVWAP(candles) // Calculate VWAP for all candles
	closes := make([]float64, len(candles))
	highs := make([]float64, len(candles))
	lows := make([]float64, len(candles))

	for i, c := range candles {
		closes[i] = c.Close
		highs[i] = c.High
		lows[i] = c.Low
	}

	atr := 0.0
	if len(closes) >= s.atrLength {
		atr = s.calculateATR(highs, lows, closes, s.atrLength)
	}

	rsi := []float64{}
	if len(closes) >= s.rsiLength {
		rsi = s.calculateRSI(closes, s.rsiLength)
	}

	currentRSI := 0.0
	if len(rsi) > 0 {
		currentRSI = rsi[len(rsi)-1]
	}

	log.Printf("[STRATEGY] %s | O=%.2f H:%.2f L:%.2f C=%.2f V=%.2f | RSI=%.2f VWAP=%.2f ATR=%.4f",
		candle.Timestamp.Format("15:04:05"),
		candle.Open, candle.High, candle.Low, candle.Close, candle.QuoteVolume,
		currentRSI, vwap, atr)

	// Need at least 2 candles for comparison and 2 RSI values for crossover detection
	if s.candleBuffer.Size() < 2 || len(rsi) < 2 {
		// Update position management even if not enough data for signals
		s.checkStopConditions(candle.Close)
		s.updateTrailingStops(candle.Close)
		s.checkPositionTimeouts()
		// Update RSI state for next iteration
		s.prevRSI = currentRSI
		return
	}

	// Check for RSI crossovers using previous and current values
	crossOver := false
	crossUnder := false
	if s.prevRSI > 0 { // пропускаем первый бар
		crossOver = s.prevRSI <= s.rsiOversold && currentRSI > s.rsiOversold
		crossUnder = s.prevRSI >= s.rsiOverbought && currentRSI < s.rsiOverbought
	}

	// RSI cooldown filter (to prevent multiple signals)
	if crossOver && s.lastSignalBarIndex != nil && s.candleBuffer.Size()-*s.lastSignalBarIndex <= s.cooldownBars {
		crossOver = false
	}
	if crossUnder && s.lastSignalBarIndex != nil && s.candleBuffer.Size()-*s.lastSignalBarIndex <= s.cooldownBars {
		crossUnder = false
	}

	// Update position management even if we don't generate signals
	s.checkStopConditions(candle.Close)
	s.updateTrailingStops(candle.Close)
	s.checkPositionTimeouts()

	// Check liquidation risk to prevent forced liquidation
	s.checkLiquidationRisk(candle.Close)

	// Only generate signals from completed candles
	// Use the previously completed candle for signal generation
	if s.candleBuffer.Size() >= 2 {
		// Get the previously completed candle (second to last in buffer)
		candleSlice := s.candleBuffer.ToSlice()
		if len(candleSlice) >= 2 {
			prevCandle := candleSlice[len(candleSlice)-2] // Previous completed candle
			bearishRejection := prevCandle.Close > vwap   // цена выше VWAP на предыдущей свече
			bullishRejection := prevCandle.Close < vwap   // цена ниже VWAP на предыдущей свече

			// Check if we can trade
			canTrade := s.canTrade(prevCandle)

			// Add debug logging to show indicator values
			log.Printf("[STRATEGY] RSI=%.2f, VWAP=%.2f, ATR=%.4f | Conditions - RSI_Cross: %t, RSI_Crossunder: %t, Bullish(rej.): %t, Bearish(rej.): %t, CanTrade: %t",
				currentRSI, vwap, atr, crossOver, crossUnder, bullishRejection, bearishRejection, canTrade)

			// Generate signals only based on completed candle
			if canTrade && crossOver && bullishRejection {
				log.Printf("✅ LONG SIGNAL: RSI(%.2f) crossed up(%.2f), Bullish Rejection, Volume: %.2f", currentRSI, s.rsiOversold, prevCandle.Volume)
				go s.GenerateSignal(types.OrderSideBuy, prevCandle, atr, vwap, "RSI+VWAP")
				// Update last signal bar index
				currentBarIndex := s.candleBuffer.Size() - 2 // ← -2  (previous COMPLETED bar)
				s.lastSignalBarIndex = &currentBarIndex
			} else if canTrade && crossUnder && bearishRejection {
				log.Printf("✅ SHORT SIGNAL: RSI(%.2f) crossed down(%.2f), Bearish Rejection, Volume: %.2f", currentRSI, s.rsiOverbought, prevCandle.Volume)
				go s.GenerateSignal(types.OrderSideSell, prevCandle, atr, vwap, "RSI+VWAP")
				// Update last signal bar index
				currentBarIndex := s.candleBuffer.Size() - 2 // ← -2  (previous COMPLETED bar)
				s.lastSignalBarIndex = &currentBarIndex
			}
		}
	}

	// Update RSI state for next iteration
	s.prevRSI = currentRSI
}

// canTrade checks if trading conditions are met
func (s *AdvancedTradingStrategy) canTrade(candle *types.Candle) bool {
	log.Printf("🔍 Checking if can trade...")

	// Check cooldown
	if s.lastSignalBarIndex != nil && s.candleBuffer.Size()-*s.lastSignalBarIndex <= s.cooldownBars {
		log.Println("⏳ Cooldown period - cannot trade")
		return false
	}

	// Check if we already have an active position
	hasPosition := s.hasActivePosition()
	log.Printf("🎯 Can trade: %t (Has internal active position: %t)", !hasPosition, hasPosition)

	return !hasPosition
}

// Check if position already exists for swap

// GenerateSignal generates a trading signal and executes the trade
func (s *AdvancedTradingStrategy) GenerateSignal(side types.OrderSide, candle *types.Candle, atr, vwap float64, strategyName string) error {
	log.Printf("🔄 Attempting to generate signal: %s", side)

	s.processingMu.Lock()
	if s.isProcessingSignal {
		log.Println("⚠️ Signal processing is already in progress - skipping")
		s.processingMu.Unlock()
		return nil
	}
	s.isProcessingSignal = true
	s.processingMu.Unlock()

	defer func() {
		s.processingMu.Lock()
		s.isProcessingSignal = false
		s.processingMu.Unlock()
	}()

	log.Println("🔍 Checking for existing positions...")

	// Set leverage before placing order to ensure proper margin mode
	if !s.config.AllInMode {
		leverage := 5 // Using 5x leverage which is appropriate for futures trading
		log.Printf("📊 Setting leverage for %s to %d in isolated mode", s.instrumentID, leverage)
		// Set leverage for both long and short positions to ensure it's configured properly
		// In isolated mode, posSide is required to set leverage for each position side
		_ = s.restClient.SetLeverage(s.instrumentID, "isolated", leverage, "long")
		_ = s.restClient.SetLeverage(s.instrumentID, "isolated", leverage, "short")
	} else {
		// For AllInMode, set leverage to 1x in isolated mode (to match tdMode and avoid cross margin issues)
		log.Printf("📊 Setting leverage for %s to 1 in isolated mode (AllInMode)", s.instrumentID)
		_ = s.restClient.SetLeverage(s.instrumentID, "isolated", 1, "")
	}

	// Check if position already exists for swap
	positions, err := s.restClient.GetPositions("SWAP") // Check only swap positions
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	for _, pos := range positions {
		if pos.InstID == s.instrumentID {
			log.Println("⚠️ Position already open - skipping signal")
			return nil
		}
	}

	log.Println("💰 Calculating position size...")

	// Calculate position size - using 90% of balance with 0.5 USDT reserve for fees
	balance, err := s.restClient.GetBalance("USDT")
	if err != nil {
		log.Printf("❌ Failed to get balance: %v", err)
		return fmt.Errorf("failed to get balance: %w", err)
	}

	log.Printf("💵 Balance: Available=%.2f", balance.Availablebal)

	// Use RiskPercent of balance for position, keeping 0.5 USDT for fees
	riskFraction := s.riskPercent / 100.0
	// Add buffer for stop-loss order in isolated margin mode
	const stopLossBuffer = 5.0  // USDT buffer for stop-loss collateral
	maxSpend := balance.Availablebal*riskFraction - 0.5 - stopLossBuffer
	if maxSpend <= 0 {
		log.Printf("⚠️ Insufficient USDT balance for position after fees and stop-loss buffer (%.2f)", balance.Availablebal)
		return nil
	}

	log.Printf("📊 Risk %.1f%% → max spend: %.2f USDT (with %.1f USDT buffer for stop-loss)", s.riskPercent, maxSpend, stopLossBuffer)

	// Place market or limit order based on configuration and liquidity
	var order *types.OrderRequest
	var size float64

	ticker, err := s.restClient.GetTicker(s.instrumentID)
	if err != nil {
		log.Printf("❌ Failed to get ticker: %v", err)
		return nil
	}

	// Check liquidity based on bid/ask spread and last price
	// If liquidity is low (volume = 0), we should consider using market order to avoid "All operations failed" error
	// The volume information is not directly in ticker, but we can use the presence of bid/ask prices as a proxy

	// For both long and short positions in perpetual swap, calculate size based on available balance
	size = maxSpend / ticker.Last
	log.Printf("📊 Pre-futures: maxSpend=%.2f, price=%.2f, size=%.4f", maxSpend, ticker.Last, size)

	// Apply minimum lot size requirements for OKX futures
	// For AllInMode use cross mode which has 0.1 minimum, otherwise 0.01
	var minContract float64
	if s.config.AllInMode {
		minContract = 0.1 // Minimum for futures in cross mode
	} else {
		minContract = 0.01 // Minimum for futures in other modes
	}

	// 1. максимально возможный размер
	maxSize := (balance.Availablebal*(s.riskPercent/100.0) - 0.5) / ticker.Last
	log.Printf("📊 maxSize before rounding: %.4f", maxSize)

	// 2. округляем вниз до 0.1 с запасом 0.1$
	size = math.Floor((maxSize-0.1)*10) / 10 // ← -0.1$ запас
	log.Printf("📊 size after rounding: %.4f", size)

	if size < 0.1 {
		size = 0.1
		log.Printf("📊 size set to minimum 0.1")
	}

	if size < minContract {
		size = minContract
		log.Printf("📊 size set to minContract: %.4f", minContract)
	}

	// 3. финальная стоимость
	realCost := size * ticker.Last
	log.Printf("📊 realCost: %.4f (size: %.4f * price: %.4f)", realCost, size, ticker.Last)

	// Проверяем, хватит ли денег
	needUSDT := size*ticker.Last + 0.5 // 0.5 на комиссию
	if needUSDT > balance.Availablebal {
		log.Printf("❌ Недостаточно USDT: нужно %.2f, есть %.2f – пропускаем сигнал", needUSDT, balance.Availablebal)
		return nil // не входим
	}

	// ОТКЛЮЧАЕМ авто-увеличение размера до минимального номинала OKX
	// minContr := 1.0                       // шаг лота для QTUM
	// minNotional := 20.0                   // порог OKX для QTUM
	// notional := size * ticker.Last

	// if notional < minNotional {
	// 	size = math.Ceil(minNotional/ticker.Last/minContr) * minContr
	// 	log.Printf("⚠️  Увеличили size до %.1f (номинал %.2f USDT) для соблюдения минимума OKX", size, size*ticker.Last)
	//
	// 	// Проверяем снова, хватит ли денег после увеличения размера
	// 	needUSDT = size*ticker.Last + 0.5
	// 	if needUSDT > balance.Availablebal {
	// 		log.Printf("❌ После увеличения размера недостаточно USDT: нужно %.2f, есть %.2f – пропускаем сигнал", needUSDT, balance.Availablebal)
	// 		return nil                       // не входим
	// 	}
	// }

	// Проверим, что размер соответствует минимальным требованиям по номиналу для рыночного ордера
	// (перемещено после определения orderType)

	// Ensure that we have enough USDT in the appropriate account before placing order
	if s.config.AllInMode {
		// For all-in mode using cross, ensure we have funds available
		// Since we're using cross mode now, check the cross margin balance
		bal, err := s.restClient.GetMarginBalance("USDT")
		if err != nil {
			log.Printf("⚠️ Could not fetch cross margin balance: %v, falling back to unified account", err)
			// Fallback to unified account check
			bal, err := s.restClient.GetBalance("USDT")
			if err != nil {
				log.Printf("❌ balance check failed: %v", err)
				return nil
			}
			if bal.Availablebal < realCost+0.5 {
				log.Printf("❌ Insufficient funds in unified account. Available: %.2f, Need: %.2f", bal.Availablebal, realCost+0.5)
				return nil
			}
		} else {
			if bal.Availablebal < realCost+0.5 {
				log.Printf("❌ Insufficient funds in cross margin account. Available: %.2f, Need: %.2f", bal.Availablebal, realCost+0.5)
				return nil
			}
		}
	} else {
		// For non-all-in mode, use ensureCrossUSDT
		if err := s.ensureCrossUSDT(realCost + 0.5); err != nil {
			log.Printf("❌ funding transfer failed: %v", err)
			return nil
		}
	}

	log.Printf("📊 Post-futures: size=%.4f", size)

	// Choose the appropriate trading mode based on AllInMode
	// For AllInMode, use isolated to avoid cross-margin complications with stop-loss orders
	tdMode := "cross" // default
	if s.config.AllInMode {
		tdMode = "isolated" // isolated mode for all-in to avoid cross margin issues with stop-loss
	}

	// Determine order type based on configuration and liquidity
	orderType := s.config.OrderType
	if orderType == "" {
		orderType = "market" // default to market if not specified
	}

	// Check for low liquidity by attempting to get order book
	orderBook, err := s.restClient.GetOrderBook(s.instrumentID, 1) // Get top of book
	lowLiquidity := err != nil || len(orderBook.Asks) == 0 || len(orderBook.Bids) == 0

	// If order book is empty or error occurred, it's low liquidity situation
	if lowLiquidity {
		log.Printf("⚠️ Low liquidity detected - using market order to avoid 'All operations failed' error")
		orderType = "market" // Force market order when liquidity is low
	}

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
		minNotional := 20.0       // OKX лимит для фьючерсных инструментов
		notional := size * *price // номинал = размер * цена

		if notional < minNotional {
			log.Printf("⚠️  Лимитный номинал %.2f < %.2f USDT – переключаемся на рыночный", notional, minNotional)
			orderType = "market" // переключаемся на рыночный ордер
			price = nil          // у рыночного ордера цена не указывается
		} else {
			log.Printf("📊 Limit price calculated: %.4f for %s order (nominal: %.2f USDT)", *price, side, notional)

			// Дополнительная проверка: если номинал < 25 USD – force market, чтобы избежать "All operations failed"
			// OKX может отклонить лимитные ордера даже с номиналом > 1 USD из-за широкого спреда или малого объёма
			if notional < 25.0 {
				log.Printf("⚠️  Nominal %.2f < 25 USD – force market order to avoid 'All operations failed'", notional)
				orderType = "market"
				price = nil
			}
		}
	}

	// Проверим, что размер соответствует минимальным требованиям по номиналу для рыночного ордера
	minMarketNotional := 1.0 // минимальный номинал для рыночных ордеров OKX (обычно 1 USDT)
	if orderType == "market" && realCost < minMarketNotional {
		log.Printf("⚠️  Рыночный ордер номиналом %.2f < %.2f USDT – пропускаем", realCost, minMarketNotional)
		return nil
	}

	// Если номинал < 25 USD – увеличиваем размер до 25 USD для рыночных ордеров (понижен с 55 для поддержки низких балансов)
	// (проверка после всех возможных переключений на market-ордер)
	if orderType == "market" {
		nominal := size * ticker.Last
		if nominal < 25.0 {
			minContract := 0.1 // используем минимальный размер контракта для фьючерсов
			size = math.Ceil(25.0/ticker.Last/minContract) * minContract
			realCost = size * ticker.Last
			log.Printf("⚠  Nominal < 25 USD – increased size to %.4f (nominal: %.2f USDT)", size, realCost)
		}
	}

	// Determine posSide based on order side
	posSide := "long"
	if side == types.OrderSideSell {
		posSide = "short"
	}

	order = &types.OrderRequest{
		InstID:    s.instrumentID, // futures contract
		TdMode:    tdMode,         // Use the selected trading mode (cross for all-in mode)
		Side:      side,
		PosSide:   posSide, // Position side: long/short
		OrderType: orderType,
		Size:      size,
		Price:     price,
		ClOrdID:   fmt.Sprintf("strat%d%04d", time.Now().Unix(), rand.Intn(10000)),
	}

	log.Printf("📊 Preparing to place order - Size: %.4f, Price: %v, Type: %s, Nominal: %.4f",
		size, price, orderType, size*ticker.Last)

	orderResp, err := s.restClient.PlaceOrder(order)
	if err != nil {
		log.Printf("❌ Failed to place order: %v", err)
		return fmt.Errorf("failed to place order: %w", err)
	}

	if orderResp.Code != "0" {
		log.Printf("❌ Order failed with code %s: %s", orderResp.Code, orderResp.Message)
		// Попробуем получить больше информации о причине ошибки
		log.Printf("📊 Order details - InstID: %s, Side: %s, Size: %.4f, Price: %v, Type: %s",
			order.InstID, order.Side, order.Size, order.Price, order.OrderType)
		return fmt.Errorf("order failed: %s", orderResp.Message)
	}

	if len(orderResp.Data) == 0 {
		log.Println("❌ No order ID returned")
		return fmt.Errorf("no order ID returned")
	}

	orderID := orderResp.Data[0].OrderID
	log.Printf("✅ Order placed successfully. Order ID: %s", orderID)

	// Wait for order to be filled
	var orderInfo *types.OrderInfo
	for i := 0; i < 20; i++ { // Increased timeout
		time.Sleep(500 * time.Millisecond)
		orderInfo, err = s.restClient.GetOrderInfo(orderID, s.instrumentID)
		if err != nil {
			log.Printf("⚠️ Error getting order info: %v", err)
			continue
		}
		if orderInfo.Status == types.OrderStatusFilled {
			log.Printf("✅ Order filled. Status: %s, Price: %.2f, Size: %.3f", orderInfo.Status, orderInfo.GetPrice(), orderInfo.Size)
			break
		}
		log.Printf("⏳ Waiting for order fill... Current status: %s", orderInfo.Status)
	}

	if orderInfo == nil {
		log.Printf("❌ Order info still nil after timeout – aborting")
		return nil
	}

	if orderInfo.Status != types.OrderStatusFilled {
		log.Printf("⚠️ Order not filled within timeout. Last status: %v", orderInfo.Status)
		return nil
	}

	filledPrice := orderInfo.GetPrice()  // Use the helper method to safely get the price
	filledSize := orderInfo.Size

	log.Printf("✅ Entry %s %.3f @ %.2f", side, filledSize, filledPrice)

	// Set up exit strategy - using OCO if configured, otherwise separate SL/TP
	if s.config.UseOCO {
		log.Println("🎯 Setting up OCO exit strategy...")
		s.setupOCOExit(side, filledPrice, filledSize)
	} else {
		log.Println("🎯 Setting up separate SL/TP strategy...")
		s.setupGuaranteedExit(side, filledPrice, filledSize)
		s.setupTakeProfit(side, filledPrice, filledSize)
	}

	// Store position
	position := &types.Position{
		ID:             fmt.Sprintf("%d", time.Now().UnixNano()),
		Side:           side,
		EntryPrice:     filledPrice,
		Size:           filledSize,
		StopLoss:       0, // Will be set by exit strategy
		TakeProfit:     0, // Will be set by exit strategy
		Status:         types.PositionStatusOpen,
		OpenTime:       time.Now(),
		OrderID:        orderID,
		IsTrailingStop: s.useTrailing,
	}

	s.positionsMu.Lock()
	s.activePositions[position.ID] = position
	s.positionsMu.Unlock()
	log.Printf("📌 Position stored. Active positions now: %d", len(s.activePositions))

	// Update last signal bar index
	currentBarIndex := s.candleBuffer.Size() - 1 // ← -1  (last COMPLETED bar)
	s.lastSignalBarIndex = &currentBarIndex

	return nil
}

// setupGuaranteedExit sets up guaranteed stop-loss and take-profit using conditional orders
func (s *AdvancedTradingStrategy) setupGuaranteedExit(side types.OrderSide, filledPrice, filledSize float64) {
	// Check if filledPrice is valid
	if filledPrice <= 0 {
		log.Printf("⚠️ Invalid filled price for exit setup: %.4f – skipping algo order", filledPrice)
		return
	}

	// Calculate initial SL based on entry price (for trailing stop)
	var slPrice float64
	var closeSide types.OrderSide

	if side == types.OrderSideBuy {
		// For long: stop-loss 1.5% below entry price (for initial setup)
		slPrice = math.Round((filledPrice * 0.985) * 100) / 100 // 1.5% below, rounded to 0.01
		closeSide = types.OrderSideSell
	} else {
		// For short: stop-loss 1.5% above entry price (for initial setup)
		slPrice = math.Round((filledPrice * 1.015) * 100) / 100 // 1.5% above, rounded to 0.01
		closeSide = types.OrderSideBuy
	}

	// Check if calculated SL price is valid
	if slPrice <= 0 {
		log.Printf("⚠️ Invalid SL price calculated: %.4f – skipping algo order", slPrice)
		return
	}

	var posSide string
	if closeSide == types.OrderSideBuy {
		posSide = "long" // When closing a short, we're buying (long) to close
	} else {
		posSide = "short" // When closing a long, we're selling (short) to close
	}
	
	// For trailing stop: Check if isolated balance is sufficient for stop-loss order
	if s.config.AllInMode { // Only check for AllInMode which uses isolated
		isoBal, err := s.restClient.GetIsolatedBalance(s.instrumentID)
		if err != nil {
			log.Printf("⚠️ Could not get isolated balance, proceeding anyway: %v", err)
		} else {
			// Calculate required margin for stop-loss (approximately 1% of position nominal)
			requiredMargin := filledSize * slPrice * 0.01
			if isoBal < requiredMargin {
				log.Printf("⚠️ Insufficient USDT in isolated account for stop-loss: need %.2f, available %.2f", 
					requiredMargin, isoBal)
				return // Skip creating stop-loss if not enough margin
			}
		}
	}

	// For trailing stop: create only SL initially, TP will be managed separately if required
	algoOrder := &types.AlgoOrderRequest{
		InstID:      s.instrumentID,
		TdMode:      "cross", // Use isolated mode to avoid cross margin issues
		Side:        closeSide,
		PosSide:     posSide, // Position side for the order
		Size:        filledSize,
		SlTriggerPx: slPrice, // Use the correct field
		SlOrdPx:     "-1",    // Market order for stop loss execution
		OrderType:   "conditional",
		ClOrdID:     fmt.Sprintf("sl%d%04d", time.Now().Unix(), rand.Intn(10000)),
	}

	response, err := s.restClient.PlaceAlgoOrder(algoOrder)
	if err != nil || response.Code != "0" {
		log.Printf("⚠️ Initial SL order not set: %v", err)
		log.Printf("❌ Algo order response: %+v", response)
	} else {
		log.Printf("✅ Initial SL set at %.4f", slPrice)
	}
}

// setupTakeProfit sets up guaranteed take-profit using conditional order
func (s *AdvancedTradingStrategy) setupTakeProfit(side types.OrderSide, filledPrice, filledSize float64) {
	// Check if filledPrice is valid
	if filledPrice <= 0 {
		log.Printf("⚠️ Invalid filled price for TP setup: %.4f – skipping algo order", filledPrice)
		return
	}

	// Calculate TP price based on entry price
	var tpPrice float64
	var closeSide types.OrderSide

	if side == types.OrderSideBuy {
		// For long: take-profit 2% above entry price
		tpPrice = math.Round((filledPrice * 1.02) * 100) / 100 // 2% above, rounded to 0.01
		closeSide = types.OrderSideSell
	} else {
		// For short: take-profit 2% below entry price
		tpPrice = math.Round((filledPrice * 0.98) * 100) / 100 // 2% below, rounded to 0.01
		closeSide = types.OrderSideBuy
	}

	// Check if calculated TP price is valid
	if tpPrice <= 0 {
		log.Printf("⚠️ Invalid TP price calculated: %.4f – skipping algo order", tpPrice)
		return
	}

	var posSide string
	if closeSide == types.OrderSideBuy {
		posSide = "long"
	} else {
		posSide = "short"
	}

	// Check isolated balance (same as for SL)
	if s.config.AllInMode {
		isoBal, err := s.restClient.GetIsolatedBalance(s.instrumentID)
		if err != nil {
			log.Printf("⚠️ Could not get isolated balance for TP: %v", err)
		} else {
			requiredMargin := filledSize * tpPrice * 0.01 // 1% от номинала
			if isoBal < requiredMargin {
				log.Printf("⚠️ Insufficient USDT in isolated for TP: need %.2f, available %.2f – skipping", requiredMargin, isoBal)
				return
			}
		}
	}

	// Create conditional TP order
	algoOrder := &types.AlgoOrderRequest{
		InstID:      s.instrumentID,
		TdMode:      "cross",
		Side:        closeSide,
		PosSide:     posSide,
		Size:        filledSize,
		TpTriggerPx: tpPrice,
		TpOrdPx:     "-1", // Market execution
		OrderType:   "conditional",
		ClOrdID:     fmt.Sprintf("tp%d%04d", time.Now().Unix(), rand.Intn(10000)),
	}

	response, err := s.restClient.PlaceAlgoOrder(algoOrder)
	if err != nil || response.Code != "0" {
		log.Printf("⚠️ TP order not set: %v", err)
		log.Printf("❌ TP algo order response: %+v", response)
	} else {
		log.Printf("✅ TP set at %.4f", tpPrice)
	}
}

// setupOCOExit sets up guaranteed stop-loss and take-profit using OCO order
func (s *AdvancedTradingStrategy) setupOCOExit(side types.OrderSide, filledPrice, filledSize float64) {
	// Check if filledPrice is valid
	if filledPrice <= 0 {
		log.Printf("⚠️ Invalid filled price for OCO exit: %.4f – skipping", filledPrice)
		return
	}

	slPercent := getOrDefault(s.config.StopLossPercent, 1.5)
	tpPercent := getOrDefault(s.config.TakeProfitPercent, 1.5)

	var slPrice, tpPrice float64
	var closeSide types.OrderSide
	var posSide string

	if side == types.OrderSideBuy {
		slPrice = math.Round((filledPrice * (1 - slPercent/100)) * 100) / 100 // 1.5% ниже
		tpPrice = math.Round((filledPrice * (1 + tpPercent/100)) * 100) / 100  // 1.5% выше
		closeSide = types.OrderSideSell
		posSide = "short"
	} else {
		slPrice = math.Round((filledPrice * (1 + slPercent/100)) * 100) / 100 // 1.5% выше
		tpPrice = math.Round((filledPrice * (1 - tpPercent/100)) * 100) / 100  // 1.5% ниже
		closeSide = types.OrderSideBuy
		posSide = "long"
	}

	// Ensure there is sufficient USDT in cross account for OCO order
	err := s.restClient.TransferToCross("USDT", 20.0) // Direct transfer to cross for OCO (20 USDT)
	if err != nil {
		log.Printf("⚠️ Could not transfer USDT to cross: %v", err)
		// Continue anyway, might still work if balance is sufficient
	} else {
		log.Printf("✅ Transferred USDT to cross for OCO order")
	}

	// Add a small delay to allow balance update after transfer
	time.Sleep(500 * time.Millisecond)

	// Double-check cross balance before placing OCO order
	crossBal, err := s.restClient.GetMarginBalance("USDT")
	if err == nil && crossBal.Availablebal < 5.0 {
		log.Printf("⚠️ Insufficient cross USDT balance after transfer: %.2f, need at least 5.0 USDT", crossBal.Availablebal)
	} else if err == nil {
		log.Printf("💰 Confirmed cross USDT balance: %.2f USDT available", crossBal.Availablebal)
	}

	ocoOrder := &types.AlgoOrderRequest{
		InstID:      s.instrumentID,
		TdMode:      "cross",
		Side:        closeSide,
		PosSide:     posSide,
		Size:        filledSize,
		OrderType:   "oco",
		SlTriggerPx: slPrice,
		SlOrdPx:     "-1",
		TpTriggerPx: tpPrice,
		TpOrdPx:     "-1",
		ClOrdID:     fmt.Sprintf("oco%d%04d", time.Now().Unix(), rand.Intn(10000)),
	}

	resp, err := s.restClient.PlaceAlgoOrder(ocoOrder)
	if err != nil || resp.Code != "0" {
		log.Printf("⚠️ OCO exit not set: %v", err)
		log.Printf("❌ OCO response: %+v", resp)
	} else {
		log.Printf("✅ OCO exit set: SL %.4f / TP %.4f", slPrice, tpPrice)
	}
}

// ensureCrossUSDT makes sure we have USDT in cross wallet
func (s *AdvancedTradingStrategy) ensureCrossUSDT(need float64) error {
	// We need to check the cross margin balance
	// Using the margin balance endpoint with instType=MARGIN
	bal, err := s.restClient.GetMarginBalance("USDT")
	if err != nil {
		// Fallback to funding balance
		fundingBalance, err := s.restClient.GetFundingBalance()
		if err != nil {
			log.Printf("⚠️ Could not get funding balance: %v", err)
			fundingBalance = 0
		}

		transferAmt := need + 0.5 // Adding buffer
		if transferAmt > fundingBalance {
			transferAmt = fundingBalance // don't transfer more than available
		}
		if transferAmt < 1.0 {
			transferAmt = 1.0 // minimum transfer amount
		}
		log.Printf("💸 Transfer %.4f USDT funding→cross (fallback)", transferAmt)
		return s.restClient.TransferToMargin("USDT", transferAmt)
	}
	avail := bal.Availablebal
	log.Printf("💰 cross-USDT %.4f , need %.4f", avail, need)

	if avail >= need { // enough in cross account
		return nil
	}

	// transfer the shortfall (+ small buffer)
	transferAmt := math.Ceil(need - avail + 0.5)
	if transferAmt < 1.0 {
		transferAmt = 1.0
	}
	log.Printf("💸 Transfer %.4f USDT funding→cross", transferAmt)
	return s.restClient.TransferToMargin("USDT", transferAmt)
}

// GetPositions returns all positions (active and closed)
func (s *AdvancedTradingStrategy) GetPositions() []*types.Position {
	s.positionsMu.RLock()
	defer s.positionsMu.RUnlock()

	positions := make([]*types.Position, 0, len(s.activePositions)+len(s.closedPositions))
	for _, pos := range s.activePositions {
		positions = append(positions, pos)
	}
	positions = append(positions, s.closedPositions...)
	return positions
}

// EnterAllIn implements the all-in entry method
func (s *AdvancedTradingStrategy) EnterAllIn(side types.OrderSide) error {
	// используем тот же инструмент, что и в остальной части системы
	exec := &AllInExecutor{rest: s.restClient, instID: s.instrumentID}
	orderType := s.config.OrderType
	if orderType == "" {
		orderType = "market" // default to market if not specified
	}
	return exec.EnterLongShort(context.Background(), side, orderType)
}

// AllInExecutor входит на 100 % доступного USDT без плеча (1×)
type AllInExecutor struct {
	rest   *client.OKXRestClient
	instID string
}

// EnterLongShort вычисляет максимальный объём и ставит рыночный или лимитный ордер
func (e *AllInExecutor) EnterLongShort(ctx context.Context, side types.OrderSide, orderType string) error {
	// 1. Установим плечо 1× в isolated режиме (posSide не нужен в isolated режиме) - для избежания cross margin проблем
	_ = e.rest.SetLeverage(e.instID, "isolated", 1, "")

	// 2. Баланс USDT (в Unified/Portfolio режиме)
	bal, err := e.rest.GetBalance("USDT")
	if err != nil {
		return fmt.Errorf("balance: %w", err)
	}
	avail := bal.Availablebal
	if avail <= 0.5 { // увеличенный минимальный остаток как рекомендовано
		return fmt.Errorf("no free USDT")
	}

	// 3. Текущая цена
	tick, err := e.rest.GetTicker(e.instID)
	if err != nil {
		return fmt.Errorf("ticker: %w", err)
	}
	price := tick.Last

	// 4. Размер контракта - используем все доступные средства с запасом
	const stopLossBuffer = 5.0                                   // USDT buffer for stop-loss collateral in isolated mode
	contractVal := 0.1                                          // минимальный размер для фьючерсов
	maxSize := (avail - 0.6 - stopLossBuffer) / price           // вычитаем 0.6 на комиссии и stopLossBuffer на залог
	size := math.Floor((maxSize-0.1)/contractVal) * contractVal // округляем вниз до 0.1 с запасом 0.1$
	if size < contractVal {
		size = contractVal
	}

	// Check for low liquidity by attempting to get order book
	orderBook, err := e.rest.GetOrderBook(e.instID, 1) // Get top of book
	lowLiquidity := err != nil || len(orderBook.Asks) == 0 || len(orderBook.Bids) == 0

	// If order book is empty or error occurred, it's low liquidity situation
	if lowLiquidity && orderType == "limit" {
		log.Printf("⚠️ Low liquidity detected - switching to market order for %s", side)
		orderType = "market" // Force market order when liquidity is low
	}

	// 5. Вычисляем цену для лимитного ордера, если тип ордера - limit
	var pricePtr *float64 = nil
	if orderType == "limit" {
		// Get order book to determine best available prices
		if err != nil || len(orderBook.Asks) == 0 || len(orderBook.Bids) == 0 {
			log.Printf("⚠️ Could not get order book, using calculated price: %v", err)
			// Fallback: calculate limit price depending on order side for immediate fill:
			// - For buy orders: place slightly above market to get filled immediately
			// - For sell orders: place slightly below market to get filled immediately
			limitPrice := price
			if side == types.OrderSideBuy {
				// For buy orders, place slightly above current market to be filled immediately
				limitPrice = price * 1.001 // 0.1% above market
			} else {
				// For sell orders, place slightly below current market to be filled immediately
				limitPrice = price * 0.999 // 0.1% below market
			}
			pricePtr = &limitPrice
		} else {
			// Use prices from the order book
			if side == types.OrderSideBuy {
				// For buy orders, use the best ask price (lowest sell price)
				askPriceStr := orderBook.Asks[0][0] // First element is price
				askPrice, err := strconv.ParseFloat(askPriceStr, 64)
				if err != nil {
					log.Printf("⚠️ Could not parse ask price, using calculated price: %v", err)
					limitPrice := price * 1.001
					pricePtr = &limitPrice
				} else {
					// Place slightly above the best ask to ensure execution
					limitPrice := askPrice * 1.0005 // 0.05% above best ask
					pricePtr = &limitPrice
				}
			} else {
				// For sell orders, use the best bid price (highest buy price)
				bidPriceStr := orderBook.Bids[0][0] // First element is price
				bidPrice, err := strconv.ParseFloat(bidPriceStr, 64)
				if err != nil {
					log.Printf("⚠️ Could not parse bid price, using calculated price: %v", err)
					limitPrice := price * 0.999
					pricePtr = &limitPrice
				} else {
					// Place slightly below the best bid to ensure execution
					limitPrice := bidPrice * 0.9995 // 0.05% below best bid
					pricePtr = &limitPrice
				}
			}
		}

		// Проверяем минимальный номинал для лимитных ордеров
		// OKX требует минимальный номинал ~20 USDT для лимитных ордеров на фьючерсах
		minNotional := 20.0          // OKX лимит для фьючерсных инструментов
		notional := size * *pricePtr // номинал = размер * цена

		if notional < minNotional {
			log.Printf("⚠️  Лимитный номинал %.2f < %.2f USDT – переключаемся на рыночный", notional, minNotional)
			orderType = "market" // переключаемся на рыночный ордер
			pricePtr = nil       // у рыночного ордера цена не указывается
		} else {
			log.Printf("📊 Limit price calculated: %.4f for %s order (nominal: %.2f USDT)", *pricePtr, side, notional)

			// Дополнительная проверка: если номинал < 25 USD – force market, чтобы избежать "All operations failed"
			// OKX может отклонить лимитные ордера даже с номиналом > 1 USD из-за широкого спреда или малого объёма
			if notional < 25.0 {
				log.Printf("⚠️  Nominal %.2f < 25 USD – force market order to avoid 'All operations failed'", notional)
				orderType = "market"
				pricePtr = nil
			}
		}
	}

	// Если это рыночный ордер и номинал < 25 USD – увеличиваем размер до 25 USD (понижен с 55 для поддержки низких балансов)
	if orderType == "market" {
		nominal := size * price
		if nominal < 25.0 {
			contractVal := 0.1 // минимальный размер контракта для фьючерсов
			size = math.Ceil(25.0/price/contractVal) * contractVal
			log.Printf("⚠  Nominal < 25 USD – increased size to %.4f (nominal: %.2f USDT)", size, size*price)
		}
	}

	// Ордер в режиме «isolated» с 1× плечом
	// Determine posSide based on order side
	posSide := "long"
	if side == types.OrderSideSell {
		posSide = "short"
	}

	order := &types.OrderRequest{
		InstID:    e.instID,
		TdMode:    "cross", // cross mode for all-in mode to use entire balance
		Side:      side,
		PosSide:   posSide, // Position side: long/short
		OrderType: orderType,
		Size:      size,
		Price:     pricePtr,
		ClOrdID:   fmt.Sprintf("allin%d%04d", time.Now().Unix(), rand.Intn(10000)),
	}
	resp, err := e.rest.PlaceOrder(order)
	if err != nil {
		return err
	}
	if resp.Code != "0" {
		return fmt.Errorf("order failed: %s", resp.Message)
	}
	log.Printf("✅ ALL-IN %s %.3f контрактов по %.2f", side, size, price)
	return nil
}

// hasActivePosition checks if there are any active positions
func (s *AdvancedTradingStrategy) hasActivePosition() bool {
	s.positionsMu.RLock()
	defer s.positionsMu.RUnlock()
	activeCount := len(s.activePositions)
	log.Printf("🔍 Internal active positions count: %d", activeCount)
	for id, pos := range s.activePositions {
		log.Printf("📋 Internal position - ID: %s, Side: %s, Size: %.3f, Status: %s", id, pos.Side, pos.Size, pos.Status)
	}
	return activeCount > 0
}

// checkStopConditions checks stop loss and take profit conditions
func (s *AdvancedTradingStrategy) checkStopConditions(currentPrice float64) {
	s.positionsMu.Lock()
	defer s.positionsMu.Unlock()

	for id, pos := range s.activePositions {
		shouldClose := false
		reason := ""

		// Check stop loss
		if (pos.Side == types.OrderSideBuy && currentPrice <= pos.StopLoss) ||
			(pos.Side == types.OrderSideSell && currentPrice >= pos.StopLoss) {
			shouldClose = true
			reason = "Stop Loss"
		}

		// Check take profit
		if (pos.Side == types.OrderSideBuy && currentPrice >= pos.TakeProfit) ||
			(pos.Side == types.OrderSideSell && currentPrice <= pos.TakeProfit) {
			shouldClose = true
			reason = "Take Profit"
		}

		if shouldClose {
			s.closePosition(id, pos, currentPrice, reason)
		}
	}
}

// updateTrailingStops updates trailing stop levels
func (s *AdvancedTradingStrategy) updateTrailingStops(currentPrice float64) {
	s.positionsMu.Lock()
	defer s.positionsMu.Unlock()

	for id, pos := range s.activePositions {
		if !pos.IsTrailingStop {
			continue
		}

		var newStop float64
		if pos.Side == types.OrderSideBuy {
			newStop = currentPrice * (1 - s.trailingStopPercent/100.0)
		} else {
			newStop = currentPrice * (1 + s.trailingStopPercent/100.0)
		}

		// Check if newStop is valid before using it
		if newStop <= 0 {
			continue // Skip this position update if newStop is invalid
		}

		// Only update if the new stop is better
		if (pos.Side == types.OrderSideBuy && newStop > pos.StopLoss) ||
			(pos.Side == types.OrderSideSell && newStop < pos.StopLoss) {
			pos.StopLoss = newStop
			s.activePositions[id] = pos
		}
	}
}

// checkPositionTimeouts checks for position timeouts
func (s *AdvancedTradingStrategy) checkPositionTimeouts() {
	s.positionsMu.Lock()
	defer s.positionsMu.Unlock()

	now := time.Now()
	for id, pos := range s.activePositions {
		if now.Sub(pos.OpenTime) > s.positionTimeout {
			s.closePosition(id, pos, s.currentPrice, "Timeout")
		}
	}
}

// checkLiquidationRisk проверяет уровень ликвидации и закрывает позицию при 80 % залога
func (s *AdvancedTradingStrategy) checkLiquidationRisk(currentPrice float64) {
	positions, err := s.restClient.GetPositions("SWAP")
	if err != nil || len(positions) == 0 {
		return
	}

	var pos *types.PositionInfo
	for _, p := range positions {
		if p.InstID == s.instrumentID && p.Size > 0 {
			pos = &p
			break
		}
	}

	if pos == nil {
		return // Нет позиции для проверки
	}

	// Рассчитываем процент изменения цены в зависимости от типа позиции
	// Для лонга: если цена падает ниже входа, риск увеличивается
	// Для шорта: если цена растет выше входа, риск увеличивается
	priceChangePercent := (currentPrice - pos.AvgPrice) / pos.AvgPrice * 100

	var lossPercent float64
	if pos.Side == "long" {
		if currentPrice < pos.AvgPrice {
			// Для лонга: потеря при падении цены
			lossPercent = -priceChangePercent
		}
	} else if pos.Side == "short" {
		if currentPrice > pos.AvgPrice {
			// Для шорта: потеря при росте цены
			lossPercent = priceChangePercent
		}
	}

	// Если потеря > 80 % от залога — закрываем принудительно
	if lossPercent >= 80.0 {
		log.Printf("🚨 Ликвидация близко (потеря %.1f%%) — закрываем позицию", lossPercent)
		s.closePositionImmediately(pos.InstID, currentPrice, "Liquidation protection")
	}
}

// closePositionImmediately закрывает позицию принудительно рыночным ордером
func (s *AdvancedTradingStrategy) closePositionImmediately(instID string, currentPrice float64, reason string) {
	// Получаем текущую позицию чтобы определить сторону
	positions, err := s.restClient.GetPositions("SWAP")
	if err != nil || len(positions) == 0 {
		log.Printf("❌ Не удалось получить позиции для закрытия: %v", err)
		return
	}

	var position *types.PositionInfo
	for _, pos := range positions {
		if pos.InstID == instID {
			position = &pos
			break
		}
	}

	if position == nil {
		log.Printf("❌ Позиция %s не найдена для закрытия", instID)
		return
	}

	// Закрываем позицию с типом ордера из конфигурации (по умолчанию market для быстрого закрытия)
	var side types.OrderSide
	if position.Side == "long" {
		side = types.OrderSideSell // Для закрытия лонга продаем
	} else {
		side = types.OrderSideBuy // Для закрытия шорта покупаем
	}

	// Для закрытия при ликвидации всегда используем рыночный ордер для гарантированного исполнения
	orderType := "market"
	var pricePtr *float64 = nil // Не требуется для рыночного ордера

	// Determine posSide based on order side for closing position
	posSide := "long"
	if side == types.OrderSideSell {
		posSide = "short"
	}

	closeOrder := &types.OrderRequest{
		InstID:    instID,
		TdMode:    "cross", // Cross margin mode to match the position
		Side:      side,
		PosSide:   posSide, // Position side: long/short
		OrderType: orderType,
		Size:      position.Size, // Закрываем всю позицию
		Price:     pricePtr,
		ClOrdID:   fmt.Sprintf("close%d%04d", time.Now().Unix(), rand.Intn(10000)),
	}

	resp, err := s.restClient.PlaceOrder(closeOrder)
	if err != nil || resp.Code != "0" {
		log.Printf("❌ Не удалось закрыть позицию: %v", err)
		if resp != nil {
			log.Printf("❌ Ответ от API: %s", resp.Message)
		}
	} else {
		log.Printf("✅ Позиция закрыта по рынку: %s", reason)
	}
}

// closePosition closes a position and calculates P&L
func (s *AdvancedTradingStrategy) closePosition(positionID string, position *types.Position, closePrice float64, reason string) {
	// Calculate P&L
	var pnl float64
	if position.Side == types.OrderSideBuy {
		pnl = (closePrice - position.EntryPrice) * position.Size
	} else {
		pnl = (position.EntryPrice - closePrice) * position.Size
	}

	log.Printf("🚪 EXIT %s | Price=%.2f | Reason=%s | PnL=%.2f USDT",
		positionID, closePrice, reason, pnl)

	// Update position
	now := time.Now()
	position.Status = types.PositionStatusClosed
	position.CloseTime = &now
	position.ClosePrice = &closePrice
	position.PnL = &pnl

	// Move to closed positions
	delete(s.activePositions, positionID)
	s.closedPositions = append(s.closedPositions, position)
}
