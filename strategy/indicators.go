package strategy

import (
	"math"
	"time"

	"okx-trading-bot/types"
)

// calculateRSI calculates the Relative Strength Index
func (s *AdvancedTradingStrategy) calculateRSI(closes []float64, period int) []float64 {
	if len(closes) < 2 || period <= 0 { // <-- достаточно 2 свечей для RSI(1)
		return []float64{}
	}

	rsi := make([]float64, len(closes))
	var avgGain, avgLoss float64

	// Calculate initial average gain and loss
	for i := 1; i <= period; i++ {
		diff := closes[i] - closes[i-1]
		if diff > 0 {
			avgGain += diff
		} else {
			avgLoss -= diff
		}
	}

	avgGain /= float64(period)
	avgLoss /= float64(period)

	// Calculate first RSI value
	if avgLoss == 0 {
		rsi[period] = 100
	} else {
		rsi[period] = 100 - (100 / (1 + avgGain/avgLoss))
	}

	// Calculate subsequent RSI values using exponential moving average
	for i := period + 1; i < len(closes); i++ {
		diff := closes[i] - closes[i-1]
		var gain, loss float64

		if diff > 0 {
			gain = diff
		} else {
			loss = -diff
		}

		avgGain = (avgGain*float64(period-1) + gain) / float64(period)
		avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)

		if avgLoss == 0 {
			rsi[i] = 100
		} else {
			rsi[i] = 100 - (100 / (1 + avgGain/avgLoss))
		}
	}

	return rsi
}

// calculateATR calculates the Average True Range
func (s *AdvancedTradingStrategy) calculateATR(highs, lows, closes []float64, period int) float64 {
	if len(highs) < period || len(lows) < period || len(closes) < period {
		return 0
	}

	trueRanges := make([]float64, len(highs))
	trueRanges[0] = highs[0] - lows[0]

	for i := 1; i < len(highs); i++ {
		hl := highs[i] - lows[i]
		hc := math.Abs(highs[i] - closes[i-1])
		lc := math.Abs(lows[i] - closes[i-1])
		trueRanges[i] = math.Max(hl, math.Max(hc, lc))
	}

	// Calculate simple moving average of true ranges
	var atr float64
	for i := 0; i < period; i++ {
		atr += trueRanges[i]
	}

	return atr / float64(period)
}

// calculateDailyVWAP calculates the Volume Weighted Average Price for today
func (s *AdvancedTradingStrategy) calculateDailyVWAP(candles []*types.Candle) float64 {
	if len(candles) == 0 {
		return 0
	}

	todayStart := time.Now().UTC().Truncate(24 * time.Hour)
	var sumPV, sumV float64

	for _, candle := range candles {
		if candle.Timestamp.After(todayStart) || candle.Timestamp.Equal(todayStart) {
			typicalPrice := (candle.High + candle.Low + candle.Close) / 3.0
			sumPV += typicalPrice * candle.Volume
			sumV += candle.Volume
		}
	}

	if sumV == 0 {
		return 0
	}

	return sumPV / sumV
}

// calculateSMA calculates Simple Moving Average
func calculateSMA(values []float64, period int) []float64 {
	if len(values) < period {
		return []float64{}
	}

	sma := make([]float64, len(values))
	for i := period - 1; i < len(values); i++ {
		sum := 0.0
		for j := i - period + 1; j <= i; j++ {
			sum += values[j]
		}
		sma[i] = sum / float64(period)
	}

	return sma
}

// calculateEMA calculates Exponential Moving Average
func calculateEMA(values []float64, period int) []float64 {
	if len(values) < period {
		return []float64{}
	}

	ema := make([]float64, len(values))
	multiplier := 2.0 / (float64(period) + 1.0)

	// Calculate initial SMA for the first EMA value
	sum := 0.0
	for i := 0; i < period; i++ {
		sum += values[i]
	}
	ema[period-1] = sum / float64(period)

	// Calculate EMA for remaining values
	for i := period; i < len(values); i++ {
		ema[i] = (values[i] * multiplier) + (ema[i-1] * (1 - multiplier))
	}

	return ema
}

// calculateBollingerBands calculates Bollinger Bands
func calculateBollingerBands(values []float64, period int, stdDevMultiplier float64) (upper, middle, lower []float64) {
	if len(values) < period {
		return []float64{}, []float64{}, []float64{}
	}

	middle = calculateSMA(values, period)
	upper = make([]float64, len(values))
	lower = make([]float64, len(values))

	for i := period - 1; i < len(values); i++ {
		// Calculate standard deviation
		sum := 0.0
		for j := i - period + 1; j <= i; j++ {
			diff := values[j] - middle[i]
			sum += diff * diff
		}
		stdDev := math.Sqrt(sum / float64(period))

		upper[i] = middle[i] + (stdDevMultiplier * stdDev)
		lower[i] = middle[i] - (stdDevMultiplier * stdDev)
	}

	return upper, middle, lower
}

// calculateMACD calculates Moving Average Convergence Divergence
func calculateMACD(values []float64, fastPeriod, slowPeriod, signalPeriod int) (macd, signal, histogram []float64) {
	if len(values) < slowPeriod {
		return []float64{}, []float64{}, []float64{}
	}

	fastEMA := calculateEMA(values, fastPeriod)
	slowEMA := calculateEMA(values, slowPeriod)

	macd = make([]float64, len(values))
	for i := range macd {
		if i >= slowPeriod-1 {
			macd[i] = fastEMA[i] - slowEMA[i]
		}
	}

	// Calculate signal line (EMA of MACD)
	macdValues := make([]float64, 0)
	for i := slowPeriod - 1; i < len(macd); i++ {
		macdValues = append(macdValues, macd[i])
	}

	signalEMA := calculateEMA(macdValues, signalPeriod)
	signal = make([]float64, len(values))
	copy(signal[slowPeriod-1:], signalEMA)

	// Calculate histogram
	histogram = make([]float64, len(values))
	for i := range histogram {
		if i >= slowPeriod+signalPeriod-2 {
			histogram[i] = macd[i] - signal[i]
		}
	}

	return macd, signal, histogram
}

// calculateStochastic calculates Stochastic oscillator
func calculateStochastic(highs, lows, closes []float64, kPeriod, dPeriod int) (percentK, percentD []float64) {
	if len(highs) < kPeriod || len(lows) < kPeriod || len(closes) < kPeriod {
		return []float64{}, []float64{}
	}

	percentK = make([]float64, len(closes))

	for i := kPeriod - 1; i < len(closes); i++ {
		// Find highest high and lowest low in the period
		highestHigh := highs[i]
		lowestLow := lows[i]

		for j := i - kPeriod + 1; j < i; j++ {
			if highs[j] > highestHigh {
				highestHigh = highs[j]
			}
			if lows[j] < lowestLow {
				lowestLow = lows[j]
			}
		}

		if highestHigh == lowestLow {
			percentK[i] = 50 // Avoid division by zero
		} else {
			percentK[i] = ((closes[i] - lowestLow) / (highestHigh - lowestLow)) * 100
		}
	}

	// Calculate %D (SMA of %K)
	kValues := make([]float64, 0)
	for i := kPeriod - 1; i < len(percentK); i++ {
		kValues = append(kValues, percentK[i])
	}

	dSMA := calculateSMA(kValues, dPeriod)
	percentD = make([]float64, len(closes))
	copy(percentD[kPeriod+dPeriod-2:], dSMA)

	return percentK, percentD
}
