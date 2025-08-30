// Быстрый тест торговой логики
// Запуск: go run quick_test.go
package main

import (
	"fmt"
	"math"
)

func main() {
	fmt.Println("⚡ Быстрый тест торговой логики (аналог Pine Script)")
	fmt.Println("==================================================")

	// Тестируем LONG сценарий: RSI crossover + bullish rejection
	prices := []float64{
		190.0, 189.5, 189.0, 188.5, 188.0, // Падение
		187.5, 187.0, 186.5, 186.0, 185.5, // Продолжение падения
		185.0, 184.5, 184.2, 184.8, 185.1, // Небольшой отскок ниже VWAP
	}
	volumes := []float64{
		1100, 1250, 1180, 1320, 1400, // Начальные объемы
		1200, 1350, 980, 1450, 1680, // Средние объемы
		1290, 1150, 1520, 1780, 1630, // Последние объемы
	}

	fmt.Printf("📊 Анализируем %d свечей, текущая цена: %.2f\n", len(prices), prices[len(prices)-1])

	// Параметры (как в Pine Script)
	rsiLength := 2
	rsiOverbought := 72.0
	rsiOversold := 28.0
	minVol := 50.0
	atrMultiplier := 1.5

	fmt.Printf("⚙️  Параметры: RSI=%d, Oversold=%.0f, Overbought=%.0f\n\n",
		rsiLength, rsiOversold, rsiOverbought)

	// Для тестирования используем фиксированные RSI значения
	// чтобы продемонстрировать crossover логику
	rsi := make([]float64, len(prices))
	for i := range rsi {
		if i < len(prices)-3 {
			rsi[i] = 50.0 // Нейтральные значения
		} else if i == len(prices)-3 {
			rsi[i] = 35.0 // Выше 28
		} else if i == len(prices)-2 {
			rsi[i] = 25.0 // Ниже 28 (для crossover)
		} else {
			rsi[i] = 32.0 // Выше 28 (crossover!)
		}
	}

	// Альтернативно - используем реальный расчет (закомментировано)
	// rsi := calculateQuickRSI(prices, rsiLength)

	// Рассчитываем VWAP
	vwap := calculateQuickVWAP(prices, volumes)

	// Рассчитываем ATR (упрощенно)
	atr := calculateQuickATR(prices, 14)

	// Текущие и предыдущие значения
	currentPrice := prices[len(prices)-1]
	prevPrice := prices[len(prices)-2]
	currentRSI := rsi[len(rsi)-1]
	prevRSI := rsi[len(rsi)-2]
	currentVolume := volumes[len(volumes)-1]

	// Выводим индикаторы
	fmt.Printf("📊 Текущие индикаторы:\n")
	fmt.Printf("   Price:  %.2f (prev: %.2f)\n", currentPrice, prevPrice)
	fmt.Printf("   RSI:    %.1f (prev: %.1f)\n", currentRSI, prevRSI)
	fmt.Printf("   VWAP:   %.2f\n", vwap)
	fmt.Printf("   ATR:    %.3f\n", atr)
	fmt.Printf("   Volume: %.0f\n", currentVolume)

	// Отладка RSI расчета
	fmt.Printf("\n🔍 RSI отладка (2-период):\n")
	for i := len(rsi) - 3; i < len(rsi); i++ {
		if i >= 0 {
			fmt.Printf("   [%d] Price: %.2f, RSI: %.1f\n", i, prices[i], rsi[i])
		}
	}
	fmt.Println()

	// Проверяем условия (точно как в Pine Script)

	// RSI кроссоверы (точная логика Pine Script ta.crossover/ta.crossunder)
	rsiCrossover := prevRSI <= rsiOversold && currentRSI > rsiOversold
	rsiCrossunder := prevRSI >= rsiOverbought && currentRSI < rsiOverbought

	// Rejection patterns (как в Pine Script)
	bearishRejection := currentPrice < prevPrice && currentPrice > vwap
	bullishRejection := currentPrice > prevPrice && currentPrice < vwap

	// Volume filter
	volumeOK := currentVolume > minVol

	fmt.Printf("🔍 Анализ условий:\n")
	fmt.Printf("   Volume > 50:          %s\n", statusEmoji(volumeOK))
	fmt.Println()

	// Показываем только релевантные условия в зависимости от потенциального сигнала
	fmt.Printf("🎯 LONG условия:\n")
	fmt.Printf("   RSI Crossover (>28):  %s (prev: %.1f, curr: %.1f)\n", statusEmoji(rsiCrossover), prevRSI, currentRSI)
	fmt.Printf("   Bullish Rejection:    %s (price > prev && price < vwap)\n", statusEmoji(bullishRejection))
	fmt.Printf("   → LONG возможен:      %s\n", statusEmoji(rsiCrossover && bullishRejection && volumeOK))
	fmt.Println()

	fmt.Printf("🎯 SHORT условия:\n")
	fmt.Printf("   RSI Crossunder (<72): %s (prev: %.1f, curr: %.1f)\n", statusEmoji(rsiCrossunder), prevRSI, currentRSI)
	fmt.Printf("   Bearish Rejection:    %s (price < prev && price > vwap)\n", statusEmoji(bearishRejection))
	fmt.Printf("   → SHORT возможен:     %s\n", statusEmoji(rsiCrossunder && bearishRejection && volumeOK))
	fmt.Println()

	// Финальные торговые условия (как в Pine Script)
	longCondition := rsiCrossover && bullishRejection && volumeOK
	shortCondition := rsiCrossunder && bearishRejection && volumeOK

	fmt.Printf("🎯 ФИНАЛЬНЫЕ СИГНАЛЫ:\n")
	if longCondition {
		fmt.Printf("   ✅ LONG СИГНАЛ АКТИВЕН!\n")
	} else {
		fmt.Printf("   ❌ LONG сигнал не активен\n")
	}

	if shortCondition {
		fmt.Printf("   ✅ SHORT СИГНАЛ АКТИВЕН!\n")
	} else {
		fmt.Printf("   ❌ SHORT сигнал не активен\n")
	}
	fmt.Println()

	// Выводим результат
	if longCondition {
		stopLoss := currentPrice - atr*atrMultiplier
		takeProfit := currentPrice + atr*atrMultiplier

		fmt.Printf("🚀 СИГНАЛ: ОТКРЫВАЕМ LONG!\n")
		fmt.Printf("   Entry Price:  %.2f\n", currentPrice)
		fmt.Printf("   Stop Loss:    %.2f (риск: %.2f)\n", stopLoss, currentPrice-stopLoss)
		fmt.Printf("   Take Profit:  %.2f (цель: %.2f)\n", takeProfit, takeProfit-currentPrice)
		fmt.Printf("   R/R Ratio:    %.2f\n", (takeProfit-currentPrice)/(currentPrice-stopLoss))

	} else if shortCondition {
		stopLoss := currentPrice + atr*atrMultiplier
		takeProfit := currentPrice - atr*atrMultiplier

		fmt.Printf("🔻 СИГНАЛ: ОТКРЫВАЕМ SHORT!\n")
		fmt.Printf("   Entry Price:  %.2f\n", currentPrice)
		fmt.Printf("   Stop Loss:    %.2f (риск: %.2f)\n", stopLoss, stopLoss-currentPrice)
		fmt.Printf("   Take Profit:  %.2f (цель: %.2f)\n", takeProfit, currentPrice-takeProfit)
		fmt.Printf("   R/R Ratio:    %.2f\n", (currentPrice-takeProfit)/(stopLoss-currentPrice))

	} else {
		fmt.Printf("⏳ НЕТ СИГНАЛОВ\n")
		fmt.Printf("   Причины:\n")
		if !rsiCrossover && !rsiCrossunder {
			fmt.Printf("   • RSI не пересекает уровни (текущий: %.1f)\n", currentRSI)
		}
		if !bullishRejection && !bearishRejection {
			fmt.Printf("   • Нет rejection от VWAP\n")
		}
		if !volumeOK {
			fmt.Printf("   • Малый объем (%.0f < %.0f)\n", currentVolume, minVol)
		}
	}

	// Показываем как это выглядело бы в Pine Script
	fmt.Printf("\n📝 Эквивалент в Pine Script:\n")
	fmt.Printf("   rsi = ta.rsi(close, %d)  // %.1f\n", rsiLength, currentRSI)
	fmt.Printf("   atr = ta.atr(14)         // %.3f\n", atr)
	fmt.Printf("   vwapDaily = vwap         // %.2f\n", vwap)
	fmt.Printf("   \n")
	fmt.Printf("   bearishRejection = close < close[1] and close > vwapDaily\n")
	fmt.Printf("   bullishRejection = close > close[1] and close < vwapDaily\n")
	fmt.Printf("   \n")
	fmt.Printf("   longCond = ta.crossover(rsi, 28) and volume > 50 and bullishRejection\n")
	fmt.Printf("   shortCond = ta.crossunder(rsi, 72) and volume > 50 and bearishRejection\n")
}

func statusEmoji(condition bool) string {
	if condition {
		return "✅ ДА"
	}
	return "❌ НЕТ"
}

// Правильный расчет RSI (как в Pine Script)
func calculateQuickRSI(prices []float64, period int) []float64 {
	if len(prices) <= period {
		return []float64{50, 50}
	}

	rsi := make([]float64, len(prices))

	// Инициализируем начальные значения
	for i := 0; i < period && i < len(prices); i++ {
		rsi[i] = 50.0 // Нейтральные значения для первых элементов
	}

	// Рассчитываем RSI для каждой позиции начиная с period
	for i := period; i < len(prices); i++ {
		var gains, losses float64

		// Считаем изменения за последний period
		for j := i - period + 1; j <= i; j++ {
			change := prices[j] - prices[j-1]
			if change > 0 {
				gains += change
			} else {
				losses -= change // losses уже положительные
			}
		}

		avgGain := gains / float64(period)
		avgLoss := losses / float64(period)

		if avgLoss == 0 {
			rsi[i] = 100.0
		} else if avgGain == 0 {
			rsi[i] = 0.0
		} else {
			rs := avgGain / avgLoss
			rsi[i] = 100.0 - (100.0 / (1.0 + rs))
		}
	}

	return rsi
}

// Daily VWAP (как в Pine Script)
func calculateQuickVWAP(prices, volumes []float64) float64 {
	// Для упрощения берем только данные текущего дня (последние 8 свечей)
	// В реальном Pine Script это сбрасывается каждый день
	dayStart := len(prices) - 8
	if dayStart < 0 {
		dayStart = 0
	}

	var sumPV, sumV float64

	// Используем HLC3 (как в Pine Script: hlc3 = (high + low + close) / 3)
	// Для упрощения берем только close цену
	for i := dayStart; i < len(prices); i++ {
		hlc3 := prices[i] // В реальности: (high + low + close) / 3
		sumPV += hlc3 * volumes[i]
		sumV += volumes[i]
	}

	if sumV == 0 {
		return prices[len(prices)-1]
	}

	return sumPV / sumV
}

// Упрощенный расчет ATR
func calculateQuickATR(prices []float64, period int) float64 {
	if len(prices) < 2 {
		return 1.0
	}

	// Упрощенно: средний размах последних свечей
	var totalRange float64
	count := int(math.Min(float64(period), float64(len(prices)-1)))

	for i := len(prices) - count; i < len(prices)-1; i++ {
		rang := math.Abs(prices[i+1] - prices[i])
		totalRange += rang
	}

	return totalRange / float64(count)
}
