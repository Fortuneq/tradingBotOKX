package strategy

import (
	"fmt"
	"math"
	"okx-trading-bot/client"
)

// PinePositionSize возвращает размер контракта, чтобы
// задействовать ровно EquityPercent % баланса при SL = ATR*mul
func PinePositionSize(rest *client.OKXRestClient,
	instrumentID string,
	equityPercent float64,
	atr float64,
	atrMul float64) (float64, error) {

	bal, err := rest.GetBalance("USDT")
	if err != nil {
		return 0, err
	}
	equity := bal.Equity
	riskAmt := equity * equityPercent / 100.0 // 10 % equity

	// получаем текущую цену для проверки лимита позиции
	ticker, err := rest.GetTicker(instrumentID)
	if err != nil {
		return 0, err
	}
	price := ticker.Last

	// Ограничиваем размер позиции по доступному балансу при 1x плече (кросс-маржа)
	maxContractSize := (bal.Availablebal * 0.9) / price // 90% баланса для надежности

	slDistance := atr * atrMul               // доллары
	if slDistance <= 0 {
		return 0, fmt.Errorf("invalid ATR distance: %f", slDistance)
	}
	size := riskAmt / slDistance // контракты
	
	// Ограничиваем по максимальному доступному размеру
	if size > maxContractSize {
		size = maxContractSize
	}

	// округляем до шага 0.1
	size = math.Round(size*10) / 10
	if size < 0.1 {
		size = 0.1
	}
	return size, nil
}