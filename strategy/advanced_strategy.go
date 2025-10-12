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

// enterWithSLTP входит на 70 % баланса и сразу вешает SL/TP через attach-algo
func (s *AdvancedTradingStrategy) enterWithSLTP(side types.OrderSide, candle *types.Candle, atr float64) error {
	log.Printf("🔍 enterWithSLTP: started, side=%s, atr=%.4f, candle=(H:%.4f, L:%.4f, C:%.4f)",
		side, atr, candle.High, candle.Low, candle.Close)

	// 1. Текущая цена
	ticker, err := s.restClient.GetTicker(s.instrumentID)
	if err != nil {
		log.Printf("❌ GetTicker error: %v", err)
		return fmt.Errorf("ticker: %w", err)
	}
	price := ticker.Last

	// 2. Вычисление динамического ATR-множителя
	var atrMult float64
	var slDistance, slPrice, tpPrice float64
	var size float64

	if s.config.DynamicATRMult {
		// Динамический ATR-множитель на основе диапазона свечи
		range30m := candle.High - candle.Low
		atrMult = 0.5 + (range30m/price)*100 // 0.5-3.0
		if atrMult < s.config.MinATRMult {
			atrMult = s.config.MinATRMult
		}
		if atrMult > s.config.MaxATRMult {
			atrMult = s.config.MaxATRMult
		}

		// Динамический SL/TP
		slDistance = atr * atrMult // динамический стоп
		if side == types.OrderSideBuy {
			slPrice = price - slDistance
		} else {
			slPrice = price + slDistance // для шорта
		}

		// TP = SL * 1.5
		if side == types.OrderSideBuy {
			tpPrice = price + slDistance*1.5
		} else {
			tpPrice = price - slDistance*1.5 // для шорта
		}

		// Размер позиции на основе риска и расстояния до SL
		bal, err := s.restClient.GetBalance("USDT")
		if err != nil {
			return fmt.Errorf("balance: %w", err)
		}

		// Ограничиваем размер позиции по доступному балансу при 1x плече (кросс-маржа)
		maxContractSize := (bal.Availablebal * 0.9) / price // 90% баланса для надежности

		// Используем фиксированный риск от начального баланса, если он задан, иначе - от текущего баланса
		var riskUSD float64
		if s.initialBalance > 0 {
			// Используем фиксированный риск от начального баланса
			riskUSD = s.initialBalance * (s.config.RiskPercent / 100.0)
		} else {
			// Используем риск от текущего баланса (старое поведение)
			riskUSD = bal.Availablebal * (s.config.RiskPercent / 100.0)
		}
		size = riskUSD / slDistance

		// Ограничиваем по максимальному доступному размеру
		if size > maxContractSize {
			size = maxContractSize
		}

		if size < 0.1 {
			return fmt.Errorf("not enough USDT for 1× cross nominal (need %.2f, have %.2f)",
				size*price, bal.Availablebal)
		}

		// === Округление до шага 0.1 (OKX lot-size) ===
		size = math.Round(size*10) / 10
		if size < 0.1 {
			size = 0.1
		}
		if size > 1000 { // максимум 1000 контрактов
			size = 1000
		}
	} else {
		// Традиционный расчет
		if s.config.PineMode {
			// Проверяем, что ATRMultiplierSL не равен 0, чтобы избежать ошибки "invalid ATR distance: 0.000000"
			atrMultiplier := s.config.ATRMultiplierSL
			if atrMultiplier <= 0 {
				log.Printf("⚠️ ATRMultiplierSL is %.2f, using fallback 0.5", atrMultiplier)
				atrMultiplier = 0.5 // используем разумный fallback
			}

			size, err = PinePositionSize(s.restClient, s.instrumentID,
				s.config.EquityPercent, atr, atrMultiplier)
			if err != nil {
				log.Printf("⚠️ PinePositionSize error: %v, using fallback 0.1 size", err)
				size = 0.1 // минимальный размер контракта

				// Также проверим, что ATR не равен 0, иначе используем разумный fallback
				if atr <= 0 {
					log.Printf("⚠️ ATR is zero, using fallback 0.2 for calculations")
					atr = 0.2
				}
			} else {
				// 2. Ограничиваем размер контракта доступным балансом в cross-режиме 1×
				bal, err := s.restClient.GetBalance("USDT")
				if err != nil {
					return fmt.Errorf("balance: %w", err)
				}

				// Если используется фиксированный начальный баланс, ограничиваем по соотношению к начальному балансу
				var effectiveBalance float64
				if s.initialBalance > 0 {
					effectiveBalance = s.initialBalance
				} else {
					effectiveBalance = bal.Availablebal
				}

				maxContr := math.Floor(effectiveBalance/price/0.1) * 0.1 // 0.1 шаг
				if size > maxContr {
					size = maxContr
				}
				if size < 0.1 {
					return fmt.Errorf("not enough USDT for 1× cross nominal (need %.2f, have %.2f)",
						size*price, bal.Availablebal)
				}
			}

			// === Округление до шага 0.1 (OKX lot-size) ===
			size = math.Round(size*10) / 10
			if size < 0.1 {
				size = 0.1
			}
			if size > 1000 { // максимум 1000 контрактов
				size = 1000
			}
		} else {
			// 2. Доступный USDT (Unified / Cross)
			bal, err := s.restClient.GetBalance("USDT")
			if err != nil {
				return fmt.Errorf("balance: %w", err)
			}
			avail := bal.Availablebal

			// Используем фиксированный риск от начального баланса, если он задан, иначе - от текущего баланса
			var riskAmt float64
			if s.initialBalance > 0 {
				// Используем фиксированный риск от начального баланса
				riskAmt = s.initialBalance * (s.config.RiskPercent / 100.0)
			} else {
				// Используем риск от текущего баланса (старое поведение)
				riskAmt = avail * (s.config.RiskPercent / 100.0)
			}

			if riskAmt <= 1.0 {
				return fmt.Errorf("risk amount too small: %.2f", riskAmt)
			}

			// 3. Размер контракта
			contractVal := 0.1 // минимальный шаг OKX для SWAP
			size = math.Floor((riskAmt/price)/contractVal) * contractVal
			if size < contractVal {
				size = contractVal
			}
		}

		// 4. Цены SL / TP с правильным соотношением риск/вознаграждение
		// slPrice and tpPrice are already declared previously, don't redeclare them

		if s.config.DynamicATRMult {
			// Динамический расчет через ATR
			slDistance := atr * atrMult
			if side == types.OrderSideBuy {
				slPrice = price - slDistance
				// TP = риск * 1.5 (соотношение 1:1.5)
				tpPrice = price + (slDistance * 1.5)
			} else {
				slPrice = price + slDistance
				tpPrice = price - (slDistance * 1.5)
			}
		} else if s.config.PineMode {
			// Pine-режим: используем ATR с множителями из конфигурации
			log.Printf("DEBUG PineMode - ATR: %.5f, price: %.2f, side: %s", atr, price, side)
			log.Printf("DEBUG PineMode - ATRMultiplierSL: %.2f, ATRMultiplierTP: %.2f", s.config.ATRMultiplierSL, s.config.ATRMultiplierTP)

			// Проверяем, что ATRMultiplierSL не равен 0, чтобы избежать ошибки "invalid ATR distance: 0.00000"
			atrMultiplierSL := s.config.ATRMultiplierSL
			atrMultiplierTP := s.config.ATRMultiplierTP
			if atrMultiplierSL <= 0 {
				log.Printf("⚠️ ATRMultiplierSL is %.2f, using fallback 0.5", atrMultiplierSL)
				atrMultiplierSL = 0.5 // используем разумный fallback
			}
			if atrMultiplierTP <= 0 {
				log.Printf("⚠️ ATRMultiplierTP is %.2f, using fallback 1.5", atrMultiplierTP)
				atrMultiplierTP = 1.5 // используем разумный fallback
			}

			slPrice = s.atrStop(price, side, atr, atrMultiplierSL)
			log.Printf("DEBUG PineMode - after atrStop: slPrice = %.5f", slPrice)

			// TP на основе ATR и желаемого соотношения
			tpDistance := atr * atrMultiplierTP
			log.Printf("DEBUG PineMode - tpDistance: %.5f (atr: %.5f * multiplierTP: %.2f)", tpDistance, atr, atrMultiplierTP)

			if side == types.OrderSideBuy {
				tpPrice = price + tpDistance
			} else {
				tpPrice = price - tpDistance
			}
			log.Printf("DEBUG PineMode - after tp calculation: tpPrice = %.5f", tpPrice)

			// Проверяем минимальное соотношение риск/вознаграждение 1:1.2
			risk := math.Abs(price - slPrice)
			reward := math.Abs(tpPrice - price)
			log.Printf("DEBUG PineMode - risk: %.5f, reward: %.5f, ratio: %.2f", risk, reward, reward/risk)

			if reward/risk < 1.2 {
				log.Printf("DEBUG PineMode - ratio < 1.2, adjusting TP")
				// Увеличиваем TP до минимального соотношения
				if side == types.OrderSideBuy {
					tpPrice = price + (risk * 1.2)
				} else {
					tpPrice = price - (risk * 1.2)
				}
				log.Printf("DEBUG PineMode - after ratio adjustment: tpPrice = %.5f", tpPrice)
			}

			// Дополнительная защита: убедимся, что цены SL и TP - корректные (не отрицательные и не нулевые)
			log.Printf("DEBUG PineMode - before zero checks: slPrice=%.5f, tpPrice=%.5f", slPrice, tpPrice)

			if slPrice <= 0 {
				log.Printf("⚠️ PineMode - slPrice is zero, using fallback")
				if side == types.OrderSideBuy {
					slPrice = price * 0.98 // SL не должен быть слишком низким
				} else {
					slPrice = price * 1.02 // SL не должен быть слишком высоким
				}
				slPrice = math.Round(slPrice*100) / 100
			}

			if tpPrice <= 0 {
				log.Printf("⚠️ PineMode - tpPrice is zero, using fallback")
				if side == types.OrderSideBuy {
					tpPrice = price * 1.02 // TP должен быть выше цены для лонга
				} else {
					tpPrice = price * 0.98 // TP должен быть ниже цены для шорта
				}
				tpPrice = math.Round(tpPrice*100) / 100
			}

			log.Printf("DEBUG PineMode - after zero checks: slPrice=%.5f, tpPrice=%.5f", slPrice, tpPrice)
		} else {
			// Классический режим: процент от цены с правильным соотношением
			riskPercent := 0.015   // 1.5% риск
			rewardPercent := 0.023 // 2.3% вознаграждение (соотношение 1:1.5)

			if side == types.OrderSideBuy {
				slPrice = price * (1 - riskPercent)
				tpPrice = price * (1 + rewardPercent)
			} else {
				slPrice = price * (1 + riskPercent)
				tpPrice = price * (1 - rewardPercent)
			}
		}

		// Округляем до шага цены (0.01 для SOL-USDT)
		slPrice = math.Round(slPrice*100) / 100
		tpPrice = math.Round(tpPrice*100) / 100

		// Проверяем минимальное расстояние для SL (не менее 0.3% от цены входа)
		minSLDistance := price * 0.003 // 0.3%
		if math.Abs(price-slPrice) < minSLDistance {
			if side == types.OrderSideBuy {
				slPrice = price - minSLDistance
			} else {
				slPrice = price + minSLDistance
			}
			slPrice = math.Round(slPrice*100) / 100
		}

		// Финальная защита от нулевых и некорректных значений SL/TP
		if slPrice <= 0 {
			if side == types.OrderSideBuy {
				slPrice = price * 0.995 // 0.5% ниже для лонга
			} else {
				slPrice = price * 1.005 // 0.5% выше для шорта
			}
			slPrice = math.Round(slPrice*100) / 100
			log.Printf("⚠️ Final fallback SL price: %.5f (entry: %.5f)", slPrice, price)
		}

		if tpPrice <= 0 {
			if side == types.OrderSideBuy {
				tpPrice = price * 1.008 // 0.8% выше для лонга
			} else {
				tpPrice = price * 0.992 // 0.8% ниже для шорта
			}
			tpPrice = math.Round(tpPrice*100) / 100
			log.Printf("⚠️ Final fallback TP price: %.5f (entry: %.5f)", tpPrice, price)
		}

		// Добавляем лог для отладки - проверяем значения перед вычислением соотношения
		log.Printf("DEBUG Before final logging: slPrice=%.5f, tpPrice=%.5f, price=%.5f", slPrice, tpPrice, price)
	}

	// Выводим информацию о динамическом расчете
	if s.config.DynamicATRMult {
		log.Printf("📊 Dynamic ATR sizing: atr=%.4f, atrMult=%.3f, range30m=%.4f, slDistance=%.4f, size=%.3f",
			atr, atrMult, candle.High-candle.Low, slDistance, size)
	}

	// Добавляем дополнительный отладочный лог перед вычислением risk/reward
	log.Printf("DEBUG Right before risk/reward calculation: slPrice=%.5f, tpPrice=%.5f, price=%.5f", slPrice, tpPrice, price)

	// Добавляем логирование соотношения риск/вознаграждение
	risk := math.Abs(price - slPrice)
	reward := math.Abs(tpPrice - price)
	ratio := reward / risk

	log.Printf("📊 SL/TP calculated: Entry=%.2f, SL=%.2f (%.2f%%), TP=%.2f (%.2f%%), Ratio=1:%.1f",
		price, slPrice, (risk/price)*100, tpPrice, (reward/price)*100, ratio)

	// 5. Сторона закрытия
	var closeSide types.OrderSide
	var closePos string
	if side == types.OrderSideBuy {
		closeSide = types.OrderSideSell // для закрытия лонга продаем
		closePos = "long"
	} else { // types.OrderSideSell
		closeSide = types.OrderSideBuy // для закрытия шорта покупаем
		closePos = "short"
	}

	// 6. Debug logging for SL and TP
	log.Printf("DEBUG attach SL and TP: entry=%.5f  slPrice=%.5f  tpPrice=%.5f  side=%s  closePos=%s",
		price, slPrice, tpPrice, side, closePos)
	log.Printf("DEBUG used block: DynamicATRMult=%v  PineMode=%v  atr=%.5f",
		s.config.DynamicATRMult, s.config.PineMode, atr)

	// Проверяем, что SL и TP цены не равны нулю (иначе OKX отклонит ордер)
	if slPrice <= 0 {
		log.Printf("⚠️ Invalid SL price: %.5f (entry: %.5f, ATR: %.5f) — skipping order", slPrice, price, atr)
		return fmt.Errorf("invalid SL price: %.5f", slPrice)
	}
	if tpPrice <= 0 {
		log.Printf("⚠️ Invalid TP price: %.5f (entry: %.5f, ATR: %.5f) — skipping order", tpPrice, price, atr)
		return fmt.Errorf("invalid TP price: %.5f", tpPrice)
	}

	// 6. Формируем attach-algo (SL и TP)
	attach := []types.AttachAlgoOrd{
		{
			OrdType: "conditional",
			Side:    closeSide,
			// PosSide:     closePos,  // НЕ НУЖЕН в Net Mode!
			SlTriggerPx: slPrice,
			SlOrdPx:     "-1",
		},
		{
			OrdType: "conditional",
			Side:    closeSide,
			// PosSide:     closePos,  // НЕ НУЖЕН в Net Mode!
			TpTriggerPx: tpPrice,
			TpOrdPx:     "-1",
		},
	}

	// 7. Основной ордер
	order := &types.OrderRequest{
		InstID: s.instrumentID,
		TdMode: "cross", // !!! теперь cross
		Side:   side,
		// PosSide:        closePos,     // НЕ НУЖЕН в Net Mode!
		OrderType:      "market",
		Size:           size,
		ClOrdID:        fmt.Sprintf("strat%d%04d", time.Now().Unix(), rand.Intn(10000)),
		AttachAlgoOrds: attach,
	}

	resp, err := s.restClient.PlaceOrder(order)
	if err != nil {
		return fmt.Errorf("place order: %w", err)
	}
	if resp.Code != "0" {
		return fmt.Errorf("order failed: %s", resp.Message)
	}

	// 8. Ждём филла (коротко)
	orderID := resp.Data[0].OrderID
	var oi *types.OrderInfo
	for i := 0; i < 20; i++ {
		time.Sleep(500 * time.Millisecond)
		oi, _ = s.restClient.GetOrderInfo(orderID, s.instrumentID)
		if oi != nil && oi.Status == types.OrderStatusFilled {
			break
		}
	}
	if oi == nil || oi.Status != types.OrderStatusFilled {
		return fmt.Errorf("order not filled")
	}

	// 9. Создаем позицию в памяти
	pos := &types.Position{
		ID:          fmt.Sprintf("%d", time.Now().UnixNano()),
		Side:        side,
		EntryPrice:  oi.GetPrice(),
		Size:        oi.Size,
		StopLoss:    slPrice,
		TakeProfit:  tpPrice,
		Status:      types.PositionStatusOpen,
		OpenTime:    time.Now(),
		OrderID:     orderID,
		PeakPrice:   oi.GetPrice(),               // Инициализируем PeakPrice как EntryPrice
		EntryATR:    atr,                         // ATR на момент входа
		TrailOffset: atr * s.config.TrailATRMult, // смещение для трейлинга
	}

	// Extract algo order IDs from the response
	if len(resp.Data) > 0 && len(resp.Data[0].AttachAlgoOrds) > 0 {
		for _, algoOrd := range resp.Data[0].AttachAlgoOrds {
			// Check if this is a stop loss order (has slTriggerPx)
			if algoOrd.SlTriggerPx != nil {
				pos.StopLossAlgoID = algoOrd.AlgoID
				log.Printf("✅ Captured SL algo order ID: %s", pos.StopLossAlgoID)
				break
			}
		}
	}

	// Use the centralized trailing stop manager instead of creating directly
	if s.config.UseTrailingStop && s.config.TrailingPercent > 0 {
		log.Printf("DEBUG: Trailing stop config - UseTrailingStop=%v, TrailingPercent=%.3f",
			s.config.UseTrailingStop, s.config.TrailingPercent)
		log.Printf("DEBUG: About to initialize trailing stop via manager - size=%.3f entry=%.5f", oi.Size, oi.GetPrice())

		// Initialize trailing stop through the manager
		if s.trailingManager != nil {
			// Using type assertion to call the manager method
			if mgr, ok := s.trailingManager.(*TrailingStopManager); ok {
				if err := mgr.InitializeTrailingForPosition(pos, oi.GetPrice(), slPrice); err != nil {
					log.Printf("⚠️ Failed to initialize trailing stop via manager: %v", err)
					// Fallback: if manager fails, we can still use fixed SL
					pos.StopLoss = slPrice
				} else {
					log.Printf("✅ Trailing stop managed by centralized manager for position %s", pos.ID)
				}
			} else {
				log.Printf("⚠️ Trailing manager type assertion failed, using fallback SL: %.4f", slPrice)
				pos.StopLoss = slPrice
			}
		} else {
			log.Printf("⚠️ Trailing manager not available, using fallback SL: %.4f", slPrice)
			pos.StopLoss = slPrice
		}
	} else {
		log.Printf("ℹ️ Native trailing not set - condition not met: UseTrailingStop=%v, TrailingPercent=%.3f",
			s.config.UseTrailingStop, s.config.TrailingPercent)
	}

	s.positionsMu.Lock()
	s.activePositions[pos.ID] = pos
	s.positionsMu.Unlock()

	// Schedule immediate position sync to ensure consistency with exchange
	go func() {
		time.Sleep(1 * time.Second) // Small delay to allow exchange to process
		s.syncPositionsFromExchange()
	}()

	// Increment daily trade counter
	if s.trailingManager != nil {
		if mgr, ok := s.trailingManager.(*TrailingStopManager); ok {
			_ = mgr // Use the variable to avoid "declared and not used" error
			// Call the increment function through the manager
			// This would require adding an increment function to the manager
		}
	}

	log.Printf("✅ Entry %s %.3f @ %.2f  SL=%.2f  TP=%.2f",
		side, oi.Size, oi.GetPrice(), slPrice, tpPrice)
	return nil
}

// TradingStrategy interface defines trading strategy operations

// atrStop возвращает цену стоп-лосса/тейк-профита на основе ATR
func (s *AdvancedTradingStrategy) atrStop(entryPx float64, side types.OrderSide, atr float64, multiplier float64) float64 {
	// Защита от нулевых или отрицательных значений
	log.Printf("DEBUG atrStop - entryPx: %.5f, atr: %.5f, multiplier: %.2f", entryPx, atr, multiplier)

	if atr <= 0 || multiplier <= 0 {
		log.Printf("⚠️ ATR(%.5f) or multiplier(%.2f) is zero, using fallback", atr, multiplier)
		// Используем разумные fallback-значения
		if atr <= 0 {
			atr = entryPx * 0.005 // 0.5% от цены как fallback для ATR
		}
		if multiplier <= 0 {
			multiplier = 0.5 // 0.5 как fallback для множителя
		}
	}

	distance := atr * multiplier
	log.Printf("DEBUG atrStop - distance: %.5f (atr: %.5f * multiplier: %.2f)", distance, atr, multiplier)

	var stopPx float64
	switch side {
	case types.OrderSideBuy:
		stopPx = entryPx - distance
	case types.OrderSideSell:
		stopPx = entryPx + distance
	}

	log.Printf("DEBUG atrStop - before rounding: stopPx = %.5f (entryPx: %.5f, distance: %.5f)", stopPx, entryPx, distance)

	// округляем до шага тика 0.01 (для большинства USDT-пар)
	stopPx = math.Round(stopPx*100) / 100

	log.Printf("DEBUG atrStop - after rounding: stopPx = %.5f", stopPx)

	// Проверяем, чтобы цена не была отрицательной или нулевой
	if stopPx <= 0 {
		log.Printf("⚠️ atrStop result is zero or negative: %.5f, using fallback", stopPx)
		if side == types.OrderSideBuy {
			// Для лонга SL не должен быть слишком низким
			stopPx = entryPx * 0.98 // 2% от цены входа как минимальный SL
		} else {
			// Для шорта SL не должен быть слишком высоким
			stopPx = entryPx * 1.02 // 2% от цены входа как минимальный SL
		}
		stopPx = math.Round(stopPx*100) / 100
		log.Printf("DEBUG atrStop - after fallback: stopPx = %.5f", stopPx)
	}

	return stopPx
}

type TradingStrategy interface {
	Initialize(ctx context.Context) error
	Update(candle *types.Candle)
	GetPositions() []*types.Position
	GenerateSignal(side types.OrderSide, candle *types.Candle, atr, vwap float64, strategyName string) error
	// All-In точка входа
	EnterAllIn(side types.OrderSide) error
	// Set trailing manager for the strategy
	SetTrailingManager(interface{})
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
	lastSyncTime       time.Time

	// RSI state tracking
	prevRSI        float64 // значение RSI на предыдущей свече
	prevCrossOver  bool    // уже было пересечение вверх (чтоб не дублировать)
	prevCrossUnder bool    // уже было пересечение вниз (чтоб не дублировать)

	// Event handlers
	onTradeSignal func(*types.TradeSignal) error

	// Reference to the trailing manager
	trailingManager interface{} // TrailingStopManager interface

	// Fixed risk management
	initialBalance float64 // начальный баланс для фиксированного риска
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
	// Используем defaultValue только если value < 0 (как индикатор "не задано")
	if value < 0 {
		return defaultValue
	}
	return value
}

// syncPositionsFromExchange synchronizes internal positions with exchange positions
func (s *AdvancedTradingStrategy) syncPositionsFromExchange() error {
	positions, err := s.restClient.GetPositions("SWAP")
	if err != nil {
		log.Printf("⚠️ Failed to fetch positions from exchange: %v", err)
		return err
	}

	s.positionsMu.Lock()
	defer s.positionsMu.Unlock()

	// Build map of exchange positions for our instrument
	exchangePositions := make(map[string]bool)
	for _, pos := range positions {
		if pos.InstID == s.instrumentID && pos.Size > 0 {
			// Use position side + size as key for cross-margin mode
			key := fmt.Sprintf("%s_%.3f", pos.Side, pos.Size)
			exchangePositions[key] = true
		}
	}

	// Check for positions that exist internally but not on exchange
	var positionsToRemove []string
	for posID, pos := range s.activePositions {
		if pos.Status != types.PositionStatusOpen {
			continue
		}

		// Check if this internal position still exists on exchange
		key := fmt.Sprintf("%s_%.3f", pos.Side, pos.Size)
		if !exchangePositions[key] {
			log.Printf("🔍 Position %s (size: %.3f, side: %s) not found on exchange - marking for cleanup", posID, pos.Size, pos.Side)
			positionsToRemove = append(positionsToRemove, posID)
		}
	}

	// Clean up positions that no longer exist on exchange
	for _, posID := range positionsToRemove {
		pos := s.activePositions[posID]
		if pos.StopLossAlgoID != "" {
			log.Printf("🚫 Cancelling trailing stop %s for closed position %s", pos.StopLossAlgoID, posID)
			if mgr, ok := s.trailingManager.(*TrailingStopManager); ok {
				if err := mgr.CancelExecutedTrailing(pos); err != nil {
					log.Printf("⚠️ Failed to cancel trailing stop %s: %v", pos.StopLossAlgoID, err)
				} else {
					log.Printf("✅ Successfully cancelled trailing stop %s", pos.StopLossAlgoID)
				}
			}
		}

		// Mark position as closed
		now := time.Now()
		pos.Status = types.PositionStatusClosed
		pos.CloseTime = &now
		delete(s.activePositions, posID)
		log.Printf("🔄 Removed closed position %s from internal tracking", posID)
	}

	// Legacy: Also clean all if no positions at all on exchange
	if len(exchangePositions) == 0 && len(s.activePositions) > 0 {
		log.Printf("🔍 Exchange sync: Found %d internal positions but no active positions on exchange", len(s.activePositions))
		for posID, pos := range s.activePositions {
			if pos.StopLossAlgoID != "" {
				log.Printf("🚫 Cancelling trailing stop %s for closed position %s", pos.StopLossAlgoID, posID)
				if mgr, ok := s.trailingManager.(*TrailingStopManager); ok {
					if err := mgr.CancelExecutedTrailing(pos); err != nil {
						log.Printf("⚠️ Failed to cancel trailing stop %s: %v", pos.StopLossAlgoID, err)
					} else {
						log.Printf("✅ Successfully cancelled trailing stop %s", pos.StopLossAlgoID)
					}
				}
			}
		}
		posCount := len(s.activePositions)
		s.activePositions = make(map[string]*types.Position)
		log.Printf("🔄 All %d internal positions cleared - exchange shows no active positions", posCount)
	}

	return nil
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

	// Periodically synchronize positions with exchange to ensure consistency
	// Also sync immediately if we have positions but infrequent updates
	shouldSync := time.Since(s.lastSyncTime) > 5*time.Second
	hasActivePositions := len(s.activePositions) > 0
	if shouldSync || (hasActivePositions && time.Since(s.lastSyncTime) > 2*time.Second) {
		s.syncPositionsFromExchange()
		s.lastSyncTime = time.Now()
	}

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

	// Check cooldown with debug info
	if s.lastSignalBarIndex != nil && s.candleBuffer.Size()-*s.lastSignalBarIndex <= s.cooldownBars {
		log.Printf("DEBUG cooldown: lastSignalBarIndex=%v, currentBarIndex=%d, diff=%d, cooldown=%d",
			s.lastSignalBarIndex, s.candleBuffer.Size()-1,
			s.candleBuffer.Size()-1-*s.lastSignalBarIndex, s.cooldownBars)
		log.Println("⏳ Cooldown period - cannot trade")
		return false
	}

	if s.lastSignalBarIndex != nil {
		log.Printf("DEBUG cooldown: lastSignalBarIndex=%v, currentBarIndex=%d, diff=%d, cooldown=%d",
			s.lastSignalBarIndex, s.candleBuffer.Size()-1,
			s.candleBuffer.Size()-1-*s.lastSignalBarIndex, s.cooldownBars)
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

	// Point of control - check conditions
	balance, err := s.restClient.GetBalance("USDT")
	balanceCheck := err == nil && balance.Availablebal > 0

	log.Printf("🐞 GenerateSignal: side=%s, canTrade=%t, hasPos=%t, balanceCheck=%t, balance=%.2f",
		side, s.canTrade(candle), s.hasActivePosition(), balanceCheck, balance.Availablebal)

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

	// Добавляем лог перед вызовом enterWithSLTP
	log.Printf("🔍 About to call enterWithSLTP: side=%s, atr=%.4f", side, atr)
	err = s.enterWithSLTP(side, candle, atr)
	if err != nil {
		log.Printf("❌ enterWithSLTP failed: %v", err)
		return err
	}
	log.Printf("✨ enterWithSLTP completed successfully")
	return nil
}

// setupGuaranteedExit sets up guaranteed stop-loss and take-profit using conditional orders
func (s *AdvancedTradingStrategy) setupGuaranteedExit(side types.OrderSide, filledPrice, filledSize float64) {
	// Check if filledPrice is valid
	if filledPrice <= 0 {
		log.Printf("⚠️ Invalid filled price for exit setup: %.4f – skipping algo order", filledPrice)
		return
	}

	// If ATR trailing stop is enabled, we'll handle SL differently
	if s.config.UseATRTrailingStop {
		// For ATR trailing stop: we'll set up initial SL and then update it via updateTrailingStops
		// Calculate initial SL based on ATR settings
		var slPrice float64
		var closeSide types.OrderSide

		if side == types.OrderSideBuy {
			// For long: stop-loss at entry price minus ATR-based distance
			initialATR := s.calculateATR([]float64{filledPrice * 1.02, filledPrice * 0.98, filledPrice * 0.99},
				[]float64{filledPrice * 0.98, filledPrice * 0.96, filledPrice * 0.97},
				[]float64{filledPrice, filledPrice * 0.97, filledPrice * 0.98}, 2) // dummy values just for initial calculation
			initialTrailDist := initialATR * s.config.TrailATRMult
			slPrice = filledPrice - initialTrailDist
			closeSide = types.OrderSideSell
		} else {
			// For short: stop-loss at entry price plus ATR-based distance
			initialATR := s.calculateATR([]float64{filledPrice * 1.02, filledPrice * 0.98, filledPrice * 0.99},
				[]float64{filledPrice * 0.98, filledPrice * 0.96, filledPrice * 0.97},
				[]float64{filledPrice, filledPrice * 0.97, filledPrice * 0.98}, 2) // dummy values just for initial calculation
			initialTrailDist := initialATR * s.config.TrailATRMult
			slPrice = filledPrice + initialTrailDist
			closeSide = types.OrderSideBuy
		}

		// Ensure SL is valid
		if slPrice <= 0 {
			log.Printf("⚠️ Invalid initial SL price calculated: %.4f – skipping algo order", slPrice)
			return
		}

		var posSide string
		if closeSide == types.OrderSideBuy {
			posSide = "long" // When closing a short, we're buying (long) to close
		} else {
			posSide = "short" // When closing a long, we're selling (short) to close
		}

		// For ATR trailing: Check if isolated balance is sufficient for stop-loss order
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

		// Create conditional SL order
		algoOrder := &types.AlgoOrderRequest{
			InstID:      s.instrumentID,
			TdMode:      "isolated", // Use isolated mode to avoid cross margin issues
			Side:        closeSide,
			PosSide:     posSide, // Position side for the order
			Size:        filledSize,
			SlTriggerPx: slPrice, // Use the correct field
			SlOrdPx:     "-1",    // Market order for stop loss execution
			OrderType:   "conditional",
			ClOrdID:     fmt.Sprintf("atrsl%d%04d", time.Now().Unix(), rand.Intn(10000)), // Special prefix for ATR SL
		}

		response, err := s.restClient.PlaceAlgoOrder(algoOrder)
		if err != nil || response.Code != "0" {
			log.Printf("⚠️ Initial ATR SL order not set: %v", err)
			log.Printf("❌ Algo order response: %+v", response)
		} else {
			log.Printf("✅ Initial ATR SL set at %.4f (ID: %s)", slPrice, response.Data[0].OrderID)
		}
	} else {
		// Original logic for non-ATR trailing
		// Calculate initial SL based on entry price (for trailing stop)
		var slPrice float64
		var closeSide types.OrderSide

		if side == types.OrderSideBuy {
			// For long: stop-loss 1.5% below entry price (for initial setup)
			slPrice = math.Round((filledPrice*0.985)*100) / 100 // 1.5% below, rounded to 0.01
			closeSide = types.OrderSideSell
		} else {
			// For short: stop-loss 1.5% above entry price (for initial setup)
			slPrice = math.Round((filledPrice*1.015)*100) / 100 // 1.5% above, rounded to 0.01
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
			TdMode:      "isolated", // Use isolated mode to avoid cross margin issues
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

	// Calculate minimum profit in USD to cover commission
	minProfitUSD := s.config.MinTakeProfitUSD
	if minProfitUSD <= 0 {
		minProfitUSD = 0.10 // default minimum 0.10 USDT
	}

	// Calculate minimum distance based on minimum profit
	minDistance := minProfitUSD / filledSize
	if minDistance <= 0 {
		minDistance = 0.02 * filledPrice // fallback to 2% if calculation is invalid
	}

	if side == types.OrderSideBuy {
		// For long: take-profit with minimum distance above entry price
		tpPrice = filledPrice + minDistance
		closeSide = types.OrderSideSell
	} else {
		// For short: take-profit with minimum distance below entry price
		tpPrice = filledPrice - minDistance
		closeSide = types.OrderSideBuy
	}

	// Round to tick size (0.01 for most USDT pairs)
	tpPrice = math.Round(tpPrice*100) / 100

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

	// Determine order execution price based on exit type
	var tpOrdPx string
	if s.config.UseLimitOrdersForExit {
		// For limit order execution
		orderBook, err := s.restClient.GetOrderBook(s.instrumentID, 1)
		if err == nil && len(orderBook.Asks) > 0 && len(orderBook.Bids) > 0 {
			if side == types.OrderSideBuy {
				// For long position, we sell - use best ask price if better than our TP
				bestAsk, _ := strconv.ParseFloat(orderBook.Asks[0][0], 64)
				if tpPrice > bestAsk {
					tpPrice = bestAsk - 0.01 // slightly better than market for faster fill
				}
			} else {
				// For short position, we buy - use best bid price if better than our TP
				bestBid, _ := strconv.ParseFloat(orderBook.Bids[0][0], 64)
				if tpPrice < bestBid {
					tpPrice = bestBid + 0.01 // slightly better than market for faster fill
				}
			}
		}
		tpOrdPx = fmt.Sprintf("%.2f", tpPrice) // limit execution at specific price
	} else {
		tpOrdPx = "-1" // market execution
	}

	// Create conditional TP order
	algoOrder := &types.AlgoOrderRequest{
		InstID:      s.instrumentID,
		TdMode:      "isolated",
		Side:        closeSide,
		PosSide:     posSide,
		Size:        filledSize,
		TpTriggerPx: tpPrice,
		TpOrdPx:     tpOrdPx,
		OrderType:   "conditional",
		ClOrdID:     fmt.Sprintf("tp%d%04d", time.Now().Unix(), rand.Intn(10000)),
	}

	response, err := s.restClient.PlaceAlgoOrder(algoOrder)
	if err != nil || response.Code != "0" {
		log.Printf("⚠️ TP order not set: %v", err)
		log.Printf("❌ TP algo order response: %+v", response)
	} else {
		log.Printf("✅ TP set at %.4f (size: %.3f, exit type: %s)", tpPrice, filledSize,
			map[bool]string{true: "limit", false: "market"}[s.config.UseLimitOrdersForExit])
	}
}

// setupTrailingStop sets up a native trailing stop order using move_order_stop
func (s *AdvancedTradingStrategy) setupTrailingStop(side types.OrderSide, filledPrice, filledSize, slDistance float64) {
	var activationPx float64
	var closeSide types.OrderSide

	// For long positions, activationPx is the highest price (peak)
	// For short positions, activationPx is the lowest price (peak)
	if side == types.OrderSideBuy {
		activationPx = filledPrice // For long, we start tracking from entry price as initial peak
		closeSide = types.OrderSideSell
	} else {
		activationPx = filledPrice // For short, we start tracking from entry price as initial peak
		closeSide = types.OrderSideBuy
	}

	callbackRatio := s.config.TrailingPercent / 100.0 // Convert percentage to ratio

	tdMode := s.config.TdMode // Use the strategy's margin mode ("cross" or "isolated")
	trailResp, err := s.restClient.PlaceTrailingStop(s.instrumentID, tdMode, closeSide, filledSize, activationPx, callbackRatio)
	if err != nil || trailResp.Code != "0" {
		log.Printf("⚠️ Native trailing stop not set: %v", err)
	} else {
		log.Printf("✅ Native trailing stop set - activation: %.4f callback: %.2f%%", activationPx, s.config.TrailingPercent)
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

// SetTrailingManager sets the trailing manager for the strategy
func (s *AdvancedTradingStrategy) SetTrailingManager(manager interface{}) {
	s.trailingManager = manager
}

// SetInitialBalance sets the initial balance for fixed risk management
func (s *AdvancedTradingStrategy) SetInitialBalance(balance float64) {
	s.initialBalance = balance
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
	const stopLossBuffer = 5.0                                  // USDT buffer for stop-loss collateral in isolated mode
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

	// Don't process trailing stops if no active positions exist
	if len(s.activePositions) == 0 {
		return // nothing to trail
	}

	for id, pos := range s.activePositions {
		// Handle ATR trailing stops with amend API - классический трейлинг стоп
		if s.config.UseATRTrailingStop {
			// TRUE ATR TRAILING: Calculate dynamic ATR trailing stop based on most recent candles
			candles := s.candleBuffer.ToSlice()
			if len(candles) >= s.atrLength { // Use the configured ATR length instead of fixed ATR(2)
				// Extract the most recent candles for ATR calculation
				startIdx := len(candles) - s.atrLength
				highs := make([]float64, s.atrLength)
				lows := make([]float64, s.atrLength)
				closes := make([]float64, s.atrLength)

				for i := 0; i < s.atrLength; i++ {
					candle := candles[startIdx+i]
					highs[i] = candle.High
					lows[i] = candle.Low
					closes[i] = candle.Close
				}

				// Calculate current ATR using the full ATR length
				currentATR := s.calculateATR(highs, lows, closes, s.atrLength)
				trailDistance := currentATR * s.config.TrailATRMult

				// Update the peak price for trailing
				if (pos.Side == types.OrderSideBuy && currentPrice > pos.PeakPrice) ||
					(pos.Side == types.OrderSideSell && currentPrice < pos.PeakPrice) {
					pos.PeakPrice = currentPrice
				}

				// Calculate new stop based on peak and current ATR distance
				var newStop float64
				if pos.Side == types.OrderSideBuy {
					newStop = pos.PeakPrice - trailDistance
				} else {
					newStop = pos.PeakPrice + trailDistance // for short
				}

				// Update stop only if it improves the position (tighter stop)
				if (pos.Side == types.OrderSideBuy && newStop > pos.StopLoss) ||
					(pos.Side == types.OrderSideSell && newStop < pos.StopLoss) {

					// Only update if the price change is significant enough to avoid excessive API calls
					priceDiff := math.Abs(newStop - pos.StopLoss)
					if priceDiff >= 0.01 { // Minimum 0.01 USDT change threshold
						// Update stop on the exchange level if we have a conditional order ID
						if pos.StopLossAlgoID != "" {
							// Use amend API to update the existing SL order instead of canceling and replacing
							triggerPx := newStop
							// Pass nil for orderPx to indicate market execution (-1)
							response, err := s.restClient.AmendAlgoOrder(
								s.instrumentID,
								pos.StopLossAlgoID,
								"conditional",
								&triggerPx,
								nil, // Market execution
							)

							if err != nil {
								log.Printf("⚠️ Failed to amend SL order: %v", err)
								// Fallback to cancel and replace approach
								_, cancelErr := s.restClient.CancelAlgoOrder(pos.StopLossAlgoID, s.instrumentID)
								if cancelErr != nil {
									log.Printf("⚠️ Could not cancel old SL algo order: %v", cancelErr)
								} else {
									log.Printf("🔄 Cancelled old SL algo order: %s", pos.StopLossAlgoID)

									// Place new SL order with updated price
									var closeSide types.OrderSide
									var posSide string
									if pos.Side == types.OrderSideBuy {
										closeSide = types.OrderSideSell
										posSide = "short"
									} else {
										closeSide = types.OrderSideBuy
										posSide = "long"
									}

									algoOrder := &types.AlgoOrderRequest{
										InstID:      s.instrumentID,
										TdMode:      "isolated",
										Side:        closeSide,
										PosSide:     posSide,
										Size:        pos.Size,
										SlTriggerPx: newStop,
										SlOrdPx:     "-1", // Market order execution
										OrderType:   "conditional",
										ClOrdID:     fmt.Sprintf("atrsl%d%04d", time.Now().Unix(), rand.Intn(10000)),
									}

									response, err := s.restClient.PlaceAlgoOrder(algoOrder)
									if err != nil || response.Code != "0" {
										log.Printf("⚠️ Failed to place updated SL order: %v", err)
									} else {
										// Store the new order ID
										pos.StopLossAlgoID = response.Data[0].OrderID
										pos.StopLoss = newStop
										s.activePositions[id] = pos
										log.Printf("✅ Updated ATR trailing SL order placed at %.4f (ID: %s) - ATR: %.4f, Distance: %.4f",
											newStop, pos.StopLossAlgoID, currentATR, trailDistance)
									}
								}
							} else if response.Code != "0" {
								log.Printf("⚠️ Amend SL order failed with code %s: %s", response.Code, response.Message)
								// Fallback to cancel and replace approach
								_, cancelErr := s.restClient.CancelAlgoOrder(pos.StopLossAlgoID, s.instrumentID)
								if cancelErr != nil {
									log.Printf("⚠️ Could not cancel old SL algo order: %v", cancelErr)
								} else {
									log.Printf("🔄 Cancelled old SL algo order: %s", pos.StopLossAlgoID)

									// Place new SL order with updated price
									var closeSide types.OrderSide
									var posSide string
									if pos.Side == types.OrderSideBuy {
										closeSide = types.OrderSideSell
										posSide = "short"
									} else {
										closeSide = types.OrderSideBuy
										posSide = "long"
									}

									algoOrder := &types.AlgoOrderRequest{
										InstID:      s.instrumentID,
										TdMode:      "isolated",
										Side:        closeSide,
										PosSide:     posSide,
										Size:        pos.Size,
										SlTriggerPx: newStop,
										SlOrdPx:     "-1", // Market order execution
										OrderType:   "conditional",
										ClOrdID:     fmt.Sprintf("atrsl%d%04d", time.Now().Unix(), rand.Intn(10000)),
									}

									response, err := s.restClient.PlaceAlgoOrder(algoOrder)
									if err != nil || response.Code != "0" {
										log.Printf("⚠️ Failed to place updated SL order: %v", err)
									} else {
										// Store the new order ID
										pos.StopLossAlgoID = response.Data[0].OrderID
										pos.StopLoss = newStop
										s.activePositions[id] = pos
										log.Printf("✅ Updated ATR trailing SL order placed at %.4f (ID: %s) - ATR: %.4f, Distance: %.4f",
											newStop, pos.StopLossAlgoID, currentATR, trailDistance)
									}
								}
							} else {
								// Successfully amended the order
								pos.StopLoss = newStop
								s.activePositions[id] = pos
								log.Printf("✅ Amended ATR trailing SL order to %.4f (ID: %s) - ATR: %.4f, Distance: %.4f",
									newStop, pos.StopLossAlgoID, currentATR, trailDistance)
							}
						} else {
							// If no conditional order ID exists, update locally only
							pos.StopLoss = newStop
							s.activePositions[id] = pos
							log.Printf("📊 Updated ATR trailing SL locally at %.4f - ATR: %.4f, Distance: %.4f",
								newStop, currentATR, trailDistance)
						}
					} else {
						// Log when the trailing stop doesn't improve or change is too small (for debugging)
						log.Printf("⏳ ATR trailing: No improvement or change too small (%.4f < 0.01) - current: %.4f, proposed: %.4f (ATR: %.4f)",
							priceDiff, pos.StopLoss, newStop, currentATR)
					}
				} else {
					// Log when the trailing stop doesn't improve (for debugging)
					log.Printf("⏳ ATR trailing: No improvement - current: %.4f, proposed: %.4f (ATR: %.4f)",
						pos.StopLoss, newStop, currentATR)
				}
			} else {
				// Not enough candles for ATR calculation, log a message
				log.Printf("⚠️ Not enough candles for ATR calculation (%d/%d)", len(candles), s.atrLength)
			}
		} else if s.config.PineMode && s.config.TrailPercent > 0 {
			// трейлим только в профите
			if (pos.Side == types.OrderSideBuy && currentPrice > pos.EntryPrice) ||
				(pos.Side == types.OrderSideSell && currentPrice < pos.EntryPrice) {

				peak := pos.PeakPrice
				if (pos.Side == types.OrderSideBuy && currentPrice > peak) ||
					(pos.Side == types.OrderSideSell && currentPrice < peak) {
					pos.PeakPrice = currentPrice
				}

				trailDist := pos.PeakPrice * s.config.TrailPercent / 100.0
				newStop := 0.0
				if pos.Side == types.OrderSideBuy {
					newStop = pos.PeakPrice - trailDist
				} else {
					newStop = pos.PeakPrice + trailDist
				}
				if (pos.Side == types.OrderSideBuy && newStop > pos.StopLoss) ||
					(pos.Side == types.OrderSideSell && newStop < pos.StopLoss) {
					// Only update if the price change is significant enough to avoid excessive API calls
					priceDiff := math.Abs(newStop - pos.StopLoss)
					if priceDiff >= 0.01 { // Minimum 0.01 USDT change threshold
						pos.StopLoss = math.Round(newStop*100) / 100 // 0.01
					} else {
						// Log when the change is too small (for debugging)
						log.Printf("⏳ Pine trailing: Change too small (%.4f < 0.01) - current: %.4f, proposed: %.4f",
							priceDiff, pos.StopLoss, newStop)
					}
				}
			}
		} else if pos.IsTrailingStop {
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
				// Only update if the price change is significant enough to avoid excessive API calls
				priceDiff := math.Abs(newStop - pos.StopLoss)
				if priceDiff >= 0.01 { // Minimum 0.01 USDT change threshold
					pos.StopLoss = newStop
					s.activePositions[id] = pos
				} else {
					// Log when the change is too small (for debugging)
					log.Printf("⏳ Basic trailing: Change too small (%.4f < 0.01) - current: %.4f, proposed: %.4f",
						priceDiff, pos.StopLoss, newStop)
				}
			}
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
	// First, let's find the internal position to cancel any associated trailing stop
	s.positionsMu.Lock()
	for id, pos := range s.activePositions {
		if s.instrumentID == instID { // Using the strategy's instrument ID
			// Cancel trailing stop via manager if available
			if s.trailingManager != nil {
				if mgr, ok := s.trailingManager.(*TrailingStopManager); ok {
					if err := mgr.CancelTrailingStop(pos); err != nil {
						log.Printf("⚠️ Failed to cancel trailing stop via manager: %v", err)
						// Fallback to direct cancellation
						if pos.StopLossAlgoID != "" {
							if ok, err := s.restClient.CancelAlgoOrder(pos.StopLossAlgoID, s.instrumentID); err != nil {
								log.Printf("⚠️ Failed to cancel trailing algo %s: %v", pos.StopLossAlgoID, err)
							} else if ok {
								log.Printf("✅ Trailing algo %s cancelled", pos.StopLossAlgoID)
							}
						}
					}
				} else {
					// Fallback to direct cancellation if manager is not available
					if pos.StopLossAlgoID != "" {
						if ok, err := s.restClient.CancelAlgoOrder(pos.StopLossAlgoID, s.instrumentID); err != nil {
							log.Printf("⚠️ Failed to cancel trailing algo %s: %v", pos.StopLossAlgoID, err)
						} else if ok {
							log.Printf("✅ Trailing algo %s cancelled", pos.StopLossAlgoID)
						}
					}
				}
			} else {
				// Fallback to direct cancellation if manager is not available
				if pos.StopLossAlgoID != "" {
					if ok, err := s.restClient.CancelAlgoOrder(pos.StopLossAlgoID, s.instrumentID); err != nil {
						log.Printf("⚠️ Failed to cancel trailing algo %s: %v", pos.StopLossAlgoID, err)
					} else if ok {
						log.Printf("✅ Trailing algo %s cancelled", pos.StopLossAlgoID)
					}
				}
			}
			delete(s.activePositions, id)
			s.closedPositions = append(s.closedPositions, pos)
			break
		}
	}
	s.positionsMu.Unlock()

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
	// Отменяем трейлинг при закрытии
	if position.StopLossAlgoID != "" {
		log.Printf("🔄 Cancelling trailing stop ID: %s for position %s", position.StopLossAlgoID, positionID)
		if mgr, ok := s.trailingManager.(*TrailingStopManager); ok {
			if err := mgr.CancelExecutedTrailing(position); err != nil {
				log.Printf("⚠️ Failed to cancel trailing stop: %v", err)
			}
		}
	} else {
		log.Printf("📋 No trailing stop to cancel for position %s", positionID)
	}

	// Быстрый расчет P&L
	pnl := (closePrice - position.EntryPrice) * position.Size
	if position.Side == types.OrderSideSell {
		pnl = -pnl
	}

	now := time.Now()
	position.Status = types.PositionStatusClosed
	position.CloseTime = &now
	position.ClosePrice = &closePrice
	position.PnL = &pnl

	delete(s.activePositions, positionID)
	s.closedPositions = append(s.closedPositions, position)

	// Trigger immediate position sync to ensure cleanup on exchange side
	go func() {
		time.Sleep(500 * time.Millisecond) // Small delay to allow exchange to process
		s.syncPositionsFromExchange()
	}()

	log.Printf("🚪 EXIT %s | Price=%.2f | Reason=%s | PnL=%.2f USDT",
		positionID, closePrice, reason, pnl)
}
