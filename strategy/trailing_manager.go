package strategy

import (
	"log"
	"strings"

	"okx-trading-bot/client"
	"okx-trading-bot/types"
)

// TrailingConfig holds configuration for trailing stop management
type TrailingConfig struct {
	Enabled       bool
	CallbackRatio float64
}

// TrailingStopManager manages all trailing stop operations
type TrailingStopManager struct {
	restClient   *client.OKXRestClient
	instrumentID string
	config       *TrailingConfig
}

// NewTrailingStopManager creates a new trailing stop manager
func NewTrailingStopManager(
	restClient *client.OKXRestClient,
	instrumentID string,
	config *TrailingConfig,
) *TrailingStopManager {
	manager := &TrailingStopManager{
		restClient:   restClient,
		instrumentID: instrumentID,
		config:       config,
	}

	return manager
}

// InitializeTrailingForPosition creates a trailing stop for a given position
func (tm *TrailingStopManager) InitializeTrailingForPosition(pos *types.Position, entryPrice, stopLossPrice float64) error {
	if !tm.config.Enabled || tm.config.CallbackRatio <= 0 {
		return nil
	}

	// Determine the activation price based on position side
	var activationPx float64
	var closeSide types.OrderSide

	if pos.Side == types.OrderSideBuy {
		// For long positions, start trailing from stop loss price (below entry)
		activationPx = stopLossPrice
		closeSide = types.OrderSideSell
	} else {
		// For short positions, start trailing from stop loss price (above entry)
		activationPx = stopLossPrice
		closeSide = types.OrderSideBuy
	}

	callbackRatio := tm.config.CallbackRatio
	tdMode := "cross" // Default to cross mode

	log.Printf("🎯 Trailing Stop Setup: Entry=%.2f, SL=%.2f, Activation=%.2f, Callback=%.3f%%",
		entryPrice, stopLossPrice, activationPx, callbackRatio*100)

	// Create the trailing stop order on exchange
	resp, err := tm.restClient.PlaceTrailingStop(
		tm.instrumentID,
		tdMode,
		closeSide,
		pos.Size,
		activationPx,
		callbackRatio,
	)
	if err != nil || resp.Code != "0" {
		return err
	}

	if len(resp.Data) > 0 {
		pos.StopLossAlgoID = resp.Data[0].AlgoID
		// Store initial peak price
		pos.PeakPrice = entryPrice
		// Reset fixed SL since we're using trailing now
		pos.StopLoss = 0
	}

	log.Printf("✅ Native trailing SL set: activation=%.5f callback=%.3f%% algoID=%s",
		activationPx, tm.config.CallbackRatio*100, pos.StopLossAlgoID)

	return nil
}

// CancelExecutedTrailing - Упрощенная проверка - только отмена исполненных
func (tm *TrailingStopManager) CancelExecutedTrailing(pos *types.Position) error {
	if pos.StopLossAlgoID == "" || pos.Status != types.PositionStatusClosed {
		return nil
	}

	// Пробуем отменить - если не получается, значит уже исполнен
	_, err := tm.restClient.CancelAlgoOrder(pos.StopLossAlgoID, tm.instrumentID)
	if err != nil && strings.Contains(err.Error(), "not found") {
		log.Printf("✅ Trailing stop already executed: %s", pos.StopLossAlgoID)
		pos.StopLossAlgoID = "" // Очищаем ID
		return nil
	}

	if err == nil {
		log.Printf("✅ Cancelled trailing stop: %s", pos.StopLossAlgoID)
		pos.StopLossAlgoID = ""
	}

	return err
}

// UpdateTrailingStops updates all active trailing stops based on current price
func (tm *TrailingStopManager) UpdateTrailingStops(positions []*types.Position, currentPrice float64) error {
	if !tm.config.Enabled {
		return nil
	}

	for _, pos := range positions {
		if pos.Status != types.PositionStatusOpen {
			continue
		}

		// Update peak price based on current price
		needsUpdate := false
		newPeakPrice := pos.PeakPrice

		if pos.Side == types.OrderSideBuy {
			// For long positions, update peak if current price is higher
			if currentPrice > pos.PeakPrice {
				newPeakPrice = currentPrice
				needsUpdate = true
			}
		} else {
			// For short positions, update peak if current price is lower
			if currentPrice < pos.PeakPrice {
				newPeakPrice = currentPrice
				needsUpdate = true
			}
		}

		if needsUpdate {
			pos.PeakPrice = newPeakPrice
		}
	}

	return nil
}

// CancelTrailingStop cancels the trailing stop for a specific position
func (tm *TrailingStopManager) CancelTrailingStop(pos *types.Position) error {
	if pos.StopLossAlgoID == "" {
		return nil
	}

	// Cancel the algo order on exchange
	if ok, err := tm.restClient.CancelAlgoOrder(pos.StopLossAlgoID, tm.instrumentID); err != nil {
		log.Printf("⚠️ Failed to cancel trailing algo %s: %v", pos.StopLossAlgoID, err)
		return err
	} else if ok {
		log.Printf("✅ Trailing algo %s cancelled", pos.StopLossAlgoID)
	}

	pos.StopLossAlgoID = ""

	return nil
}

// Stop stops the trailing stop manager
func (tm *TrailingStopManager) Stop() {
	// Empty implementation - no need to stop anything in ultra-light version
}
