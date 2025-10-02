package bot

import (
	"context"
	"fmt"
	"log"
	"math"

	"okx-trading-bot/client"
	"okx-trading-bot/types"
)

// AllInExecutor входит на 100 % доступного USDT без плеча (1×)
type AllInExecutor struct {
	rest   *client.OKXRestClient
	instID string
}

func NewAllInExecutor(rest *client.OKXRestClient, instID string) *AllInExecutor {
	return &AllInExecutor{rest: rest, instID: instID}
}

// EnterLongShort вычисляет максимальный объём и ставит рыночный ордер
func (e *AllInExecutor) EnterLongShort(ctx context.Context, side types.OrderSide) error {
	// 1. Баланс USDT в «cross» (без плеча)
	bal, err := e.rest.GetBalance("USDT")
	if err != nil {
		return fmt.Errorf("balance: %w", err)
	}
	avail := bal.Availablebal
	if avail <= 0.1 {
		return fmt.Errorf("no free USDT")
	}

	// 2. Текущая цена
	tick, err := e.rest.GetTicker(e.instID)
	if err != nil {
		return fmt.Errorf("ticker: %w", err)
	}
	price := tick.Last

	// 5. Размер контракта (OKX: 1 контракт = 1.0 QTUM для фьючерсов)
	contractVal := 1.0 // минимальный размер для QTUM фьючерса
	maxSize := (avail - 0.5) / price // вычитаем 0.5 на комиссии
	size := math.Floor(maxSize/contractVal) * contractVal // округляем вниз до 1.0
	if size < contractVal {
		size = contractVal
	}

	// 4. Рыночный ордер в режиме «cash» (1×)
	order := &types.OrderRequest{
		InstID:    e.instID,
		TdMode:    "cash",        // ← без плеча
		Side:      side,
		OrderType: "market",
		Size:      size,
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