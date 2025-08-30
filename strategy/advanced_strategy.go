package strategy

import (
	"context"
	"fmt"
	"log"
	"math"
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
}

// AdvancedTradingStrategy implements RSI + VWAP rejection strategy
type AdvancedTradingStrategy struct {
	marketDataService marketdata.MarketDataService
	restClient        *client.OKXRestClient
	config            *config.StrategyConfig

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

	// Event handlers
	onTradeSignal func(*types.TradeSignal) error
}

// NewAdvancedTradingStrategy creates a new advanced trading strategy
func NewAdvancedTradingStrategy(
	marketDataService marketdata.MarketDataService,
	restClient *client.OKXRestClient,
	cfg *config.StrategyConfig,
	bufferSize int,
) *AdvancedTradingStrategy {
	return &AdvancedTradingStrategy{
		marketDataService: marketDataService,
		restClient:        restClient,
		config:            cfg,
		candleBuffer:      NewCircularBuffer[*types.Candle](bufferSize),
		priceBuffer:       NewCircularBuffer[float64](bufferSize),
		activePositions:   make(map[string]*types.Position),
		closedPositions:   make([]*types.Position, 0),

		// Strategy parameters
		rsiLength:           cfg.RSILength,
		atrLength:           cfg.ATRLength,
		minVolume:           500.0,
		riskPercent:         cfg.RiskPercent,
		rewardRatio:         cfg.RewardRatio,
		atrMultiplier:       1.5,
		trailingStopPercent: cfg.TrailingPercent,
		maxPositionSize:     100000.0,
		positionTimeout:     4 * time.Hour,
		rsiOverbought:       72.0,
		rsiOversold:         28.0,
		cooldownBars:        1,
		useTrailing:         cfg.UseTrailingStop,
		trailingPercent:     cfg.TrailingPercent,
	}
}

// SetOnTradeSignal sets the callback for trade signals
func (s *AdvancedTradingStrategy) SetOnTradeSignal(callback func(*types.TradeSignal) error) {
	s.onTradeSignal = callback
}

// Initialize initializes the strategy with historical data
func (s *AdvancedTradingStrategy) Initialize(ctx context.Context) error {
	log.Println("Initializing trading strategy...")

	historicalData, err := s.marketDataService.GetHistoricalData(500)
	if err != nil {
		return fmt.Errorf("failed to get historical data: %w", err)
	}

	for _, candle := range historicalData {
		s.candleBuffer.PushBack(candle)
	}

	log.Printf("📊 Loaded %d historical candles", len(historicalData))
	return nil
}

// Update processes new candle data and generates trading signals
func (s *AdvancedTradingStrategy) Update(candle *types.Candle) {
	s.currentPrice = candle.Close
	s.candleBuffer.PushBack(candle)

	log.Printf("[STRATEGY] %s | O=%.2f H=%.2f L=%.2f C=%.2f V=%.2f",
		candle.Timestamp.Format("15:04:05"),
		candle.Open, candle.High, candle.Low, candle.Close, candle.QuoteVolume)

	// Need at least 2 candles for comparison
	if s.candleBuffer.Size() < 2 {
		return
	}

	prevCandle := s.candleBuffer.Get(s.candleBuffer.Size() - 2)
	if prevCandle == nil {
		return
	}

	// Calculate indicators
	candles := s.candleBuffer.ToSlice()
	vwap := s.calculateDailyVWAP(candles)
	closes := make([]float64, len(candles))
	highs := make([]float64, len(candles))
	lows := make([]float64, len(candles))

	for i, c := range candles {
		closes[i] = c.Close
		highs[i] = c.High
		lows[i] = c.Low
	}

	atr := s.calculateATR(highs, lows, closes, s.atrLength)
	rsi := s.calculateRSI(closes, s.rsiLength)

	if len(rsi) < 2 {
		return
	}

	currentRSI := rsi[len(rsi)-1]
	prevRSI := rsi[len(rsi)-2]

	// Check for RSI crossovers
	rsiCrossover := prevRSI < s.rsiOversold && currentRSI >= s.rsiOversold
	rsiCrossunder := prevRSI > s.rsiOverbought && currentRSI <= s.rsiOverbought

	// Check for VWAP rejection patterns
	bullishRejection := candle.Close > prevCandle.Close && candle.Close < vwap
	bearishRejection := candle.Close < prevCandle.Close && candle.Close > vwap

	// Check if we can trade
	canTrade := s.canTrade(candle)

	// Generate signals
	if canTrade && rsiCrossover && bullishRejection {
		go s.GenerateSignal(types.OrderSideBuy, candle, atr, vwap, "RSI+VWAP Rejection")
	} else if canTrade && rsiCrossunder && bearishRejection {
		go s.GenerateSignal(types.OrderSideSell, candle, atr, vwap, "RSI+VWAP Rejection")
	}

	// Update position management
	s.checkStopConditions(candle.Close)
	s.updateTrailingStops(candle.Close)
	s.checkPositionTimeouts()
}

// canTrade checks if trading conditions are met
func (s *AdvancedTradingStrategy) canTrade(candle *types.Candle) bool {
	// Check cooldown
	if s.lastSignalBarIndex != nil && s.candleBuffer.Size()-*s.lastSignalBarIndex <= s.cooldownBars {
		return false
	}

	// Check volume
	if candle.Volume <= s.minVolume {
		return false
	}

	// Check if we already have an active position
	return !s.hasActivePosition()
}

// GenerateSignal generates a trading signal and executes the trade
func (s *AdvancedTradingStrategy) GenerateSignal(side types.OrderSide, candle *types.Candle, atr, vwap float64, strategyName string) error {
	s.processingMu.Lock()
	if s.isProcessingSignal {
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

	// Check if position already exists
	positions, err := s.restClient.GetPositions("SWAP")
	if err != nil {
		return fmt.Errorf("failed to get positions: %w", err)
	}

	for _, pos := range positions {
		if pos.InstID == "SOL-USDT" {
			log.Println("⚠️ Position already open - skipping signal")
			return nil
		}
	}

	// Calculate position size
	balance, err := s.restClient.GetBalance("USDT")
	if err != nil {
		return fmt.Errorf("failed to get balance: %w", err)
	}

	if balance.Availablebal < 10.0 {
		log.Printf("⚠️ Insufficient USDT balance (%.2f)", balance.Availablebal)
		return nil
	}

	slDistance := atr * s.atrMultiplier
	riskUSD := balance.Availablebal * (s.riskPercent / 100.0)
	size := math.Round(riskUSD/slDistance*1000) / 1000 // Round to 3 decimal places

	if size <= 0 {
		return nil
	}

	// Place market order
	order := &types.OrderRequest{
		InstID:    "SOL-USDT",
		Side:      side,
		OrderType: "market",
		Size:      size,
	}

	orderResp, err := s.restClient.PlaceOrder(order)
	if err != nil {
		return fmt.Errorf("failed to place order: %w", err)
	}

	if orderResp.Code != "0" {
		return fmt.Errorf("order failed: %s", orderResp.Message)
	}

	if len(orderResp.Data) == 0 {
		return fmt.Errorf("no order ID returned")
	}

	orderID := orderResp.Data[0].OrderID

	// Wait for order to be filled
	var orderInfo *types.OrderInfo
	for i := 0; i < 10; i++ {
		time.Sleep(500 * time.Millisecond)
		orderInfo, err = s.restClient.GetOrderInfo(orderID, "SOL-USDT")
		if err != nil {
			continue
		}
		if orderInfo.Status == types.OrderStatusFilled {
			break
		}
	}

	if orderInfo == nil || orderInfo.Status != types.OrderStatusFilled {
		log.Printf("⚠️ Order not filled within timeout")
		return nil
	}

	filledPrice := orderInfo.Price
	filledSize := orderInfo.Size

	log.Printf("✅ Entry %s %.3f @ %.2f", side, filledSize, filledPrice)

	// Set up exit strategy
	if s.useTrailing {
		s.setupTrailingStop(side, filledPrice, filledSize, slDistance)
	} else {
		s.setupStopLossAndTakeProfit(side, filledPrice, filledSize, slDistance)
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

	// Update last signal bar index
	currentBarIndex := s.candleBuffer.Size()
	s.lastSignalBarIndex = &currentBarIndex

	return nil
}

// setupTrailingStop sets up a trailing stop order
func (s *AdvancedTradingStrategy) setupTrailingStop(side types.OrderSide, filledPrice, filledSize, slDistance float64) {
	var triggerPrice float64
	var closeSide types.OrderSide

	if side == types.OrderSideBuy {
		triggerPrice = filledPrice - slDistance
		closeSide = types.OrderSideSell
	} else {
		triggerPrice = filledPrice + slDistance
		closeSide = types.OrderSideBuy
	}

	trailResp, err := s.restClient.PlaceTrailingStop("SOL-USDT", closeSide, filledSize, triggerPrice)
	if err != nil || trailResp.Code != "0" {
		log.Printf("⚠️ Trailing stop not set: %v", err)
	} else {
		log.Printf("✅ Trailing stop set at %.2f", triggerPrice)
	}
}

// setupStopLossAndTakeProfit sets up traditional SL/TP orders
func (s *AdvancedTradingStrategy) setupStopLossAndTakeProfit(side types.OrderSide, filledPrice, filledSize, slDistance float64) {
	var slPrice, tpPrice float64
	var closeSide types.OrderSide

	if side == types.OrderSideBuy {
		slPrice = filledPrice - slDistance
		tpPrice = filledPrice + slDistance*s.rewardRatio
		closeSide = types.OrderSideSell
	} else {
		slPrice = filledPrice + slDistance
		tpPrice = filledPrice - slDistance*s.rewardRatio
		closeSide = types.OrderSideBuy
	}

	algoOrder := &types.AlgoOrderRequest{
		InstID:     "SOL-USDT",
		Side:       closeSide,
		Size:       filledSize,
		StopLoss:   slPrice,
		TakeProfit: tpPrice,
		OrderType:  "oco",
	}

	bracketResp, err := s.restClient.PlaceAlgoOrder(algoOrder)
	if err != nil || bracketResp.Code != "0" {
		log.Printf("⚠️ SL/TP not set: %v", err)
	} else {
		log.Printf("✅ SL=%.2f TP=%.2f", slPrice, tpPrice)
	}
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

// hasActivePosition checks if there are any active positions
func (s *AdvancedTradingStrategy) hasActivePosition() bool {
	s.positionsMu.RLock()
	defer s.positionsMu.RUnlock()
	return len(s.activePositions) > 0
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
