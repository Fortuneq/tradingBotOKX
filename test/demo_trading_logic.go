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
	
	// Симулируем данные последних 10 свечей SOL/USDT
	prices := []float64{185.2, 187.1, 184.8, 186.3, 189.7, 187.4, 185.9, 188.2, 190.5, 189.1}
	volumes := []float64{1200, 1350, 980, 1450, 1680, 1290, 1150, 1520, 1780, 1630}
	
	fmt.Printf("📊 Анализируем %d свечей, текущая цена: %.2f\n", len(prices), prices[len(prices)-1])
	
	// Параметры (как в Pine Script)
	rsiLength := 2
	rsiOverbought := 72.0
	rsiOversold := 28.0
	minVol := 500.0
	atrMultiplier := 1.5
	
	fmt.Printf("⚙️  Параметры: RSI=%d, Oversold=%.0f, Overbought=%.0f\n\n", 
		rsiLength, rsiOversold, rsiOverbought)
	
	// Рассчитываем RSI
	rsi := calculateQuickRSI(prices, rsiLength)
	
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
	fmt.Printf("   Volume: %.0f\n\n", currentVolume)
	
	// Проверяем условия (точно как в Pine Script)
	
	// RSI кроссоверы
	rsiCrossover := prevRSI < rsiOversold && currentRSI >= rsiOversold
	rsiCrossunder := prevRSI > rsiOverbought && currentRSI <= rsiOverbought
	
	// Rejection patterns (как в Pine Script)
	bearishRejection := currentPrice < prevPrice && currentPrice > vwap
	bullishRejection := currentPrice > prevPrice && currentPrice < vwap
	
	// Volume filter
	volumeOK := currentVolume > minVol
	
	fmt.Printf("🔍 Анализ условий:\n")
	fmt.Printf("   RSI Crossover (>28):  %s\n", statusEmoji(rsiCrossover))
	fmt.Printf("   RSI Crossunder (<72): %s\n", statusEmoji(rsiCrossunder))  
	fmt.Printf("   Bullish Rejection:    %s (price > prev && price < vwap)\n", statusEmoji(bullishRejection))
	fmt.Printf("   Bearish Rejection:    %s (price < prev && price > vwap)\n", statusEmoji(bearishRejection))
	fmt.Printf("   Volume > 500:         %s\n", statusEmoji(volumeOK))
	fmt.Println()
	
	// Финальные торговые условия (как в Pine Script)
	longCondition := rsiCrossover && bullishRejection && volumeOK
	shortCondition := rsiCrossunder && bearishRejection && volumeOK
	
	fmt.Printf("🎯 ТОРГОВЫЕ СИГНАЛЫ:\n")
	fmt.Printf("   LONG условие:  %s\n", statusEmoji(longCondition))
	fmt.Printf("   SHORT условие: %s\n\n", statusEmoji(shortCondition))
	
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

// Упрощенный расчет RSI
func calculateQuickRSI(prices []float64, period int) []float64 {
	if len(prices) <= period {
		return []float64{50, 50} // Возвращаем нейтральные значения
	}
	
	rsi := make([]float64, len(prices))
	
	// Упрощенный расчет для последних значений
	var gains, losses float64
	for i := len(prices) - period; i < len(prices)-1; i++ {
		change := prices[i+1] - prices[i]
		if change > 0 {
			gains += change
		} else {
			losses -= change
		}
	}
	
	avgGain := gains / float64(period)
	avgLoss := losses / float64(period)
	
	if avgLoss == 0 {
		rsi[len(rsi)-1] = 100
		rsi[len(rsi)-2] = 100
	} else {
		rsi[len(rsi)-1] = 100 - (100 / (1 + avgGain/avgLoss))
		rsi[len(rsi)-2] = rsi[len(rsi)-1] - 5 // Симулируем предыдущее значение
	}
	
	return rsi
}

// Упрощенный расчет VWAP
func calculateQuickVWAP(prices, volumes []float64) float64 {
	var sumPV, sumV float64
	
	for i := range prices {
		sumPV += prices[i] * volumes[i]
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