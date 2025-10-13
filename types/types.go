package types

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// OrderSide represents the side of an order (buy/sell)
type OrderSide string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
)

// OrderStatus represents the status of an order
type OrderStatus string

const (
	OrderStatusNew      OrderStatus = "new"
	OrderStatusFilled   OrderStatus = "filled"
	OrderStatusCanceled OrderStatus = "canceled"
	OrderStatusRejected OrderStatus = "rejected"
)

// PositionStatus represents the status of a position
type PositionStatus string

const (
	PositionStatusOpen   PositionStatus = "open"
	PositionStatusClosed PositionStatus = "closed"
)

// OrderRequest represents an order request
type OrderRequest struct {
	InstID         string          `json:"instId"`
	TdMode         string          `json:"tdMode"` // Trading mode: cash for spot
	Side           OrderSide       `json:"side"`
	PosSide        string          `json:"posSide,omitempty"` // Position side: long/short (only required for hedge mode)
	OrderType      string          `json:"ordType"`
	Size           float64         `json:"sz,string"`
	Price          *float64        `json:"px,string,omitempty"`
	ClOrdID        string          `json:"clOrdId,omitempty"` // Client order ID for uniqueness
	// новое поле – массив прицепленных алго-ордеров
	AttachAlgoOrds []AttachAlgoOrd `json:"attachAlgoOrds,omitempty"`
}

// AttachAlgoOrd – один прицеплённый алго-ордер
type AttachAlgoOrd struct {
	AttachAlgoId string  `json:"attachAlgoId,omitempty"` // оставляем пустым
	OrdType      string  `json:"ordType"`                // "conditional"
	Side         OrderSide `json:"side"`
	PosSide      string  `json:"posSide"`
	SlTriggerPx  float64 `json:"slTriggerPx,string,omitempty"`
	SlOrdPx      string  `json:"slOrdPx,omitempty"`   // "-1"
	TpTriggerPx  float64 `json:"tpTriggerPx,string,omitempty"`
	TpOrdPx      string  `json:"tpOrdPx,omitempty"`   // "-1"
}

// AlgoOrderRequest represents an algorithmic order request
type AlgoOrderRequest struct {
	InstID      string    `json:"instId"`
	TdMode      string    `json:"tdMode"` // Trading mode: cash for spot
	Side        OrderSide `json:"side"`
	PosSide     string    `json:"posSide,omitempty"` // Position side: long/short (hedge mode) or net (one-way mode)
	Size        float64   `json:"sz,string"`
	OrderType   string    `json:"ordType"`        // "conditional" for SL/TP orders, "market" for entry with conditional exits
	ClOrdID     string    `json:"clOrdId,omitempty"` // Client order ID for uniqueness
	// Fields for conditional orders (SL/TP)
	SlTriggerPx float64 `json:"slTriggerPx,string,omitempty"`  // Stop Loss Trigger Price
	SlOrdPx     string  `json:"slOrdPx,omitempty"`             // Stop Loss Order Price ("-1" for market)
	TpTriggerPx float64 `json:"tpTriggerPx,string,omitempty"`  // Take Profit Trigger Price
	TpOrdPx     string  `json:"tpOrdPx,omitempty"`             // Take Profit Order Price ("-1" for market)
}

// OrderResponse represents an order response from API
type OrderResponse struct {
	Code    string `json:"code"`
	Message string `json:"msg"`
	Data    []struct {
		OrderID string `json:"ordId"`
		// Added fields to capture attached algo order information
		AttachAlgoOrds []struct {
			AlgoID      string  `json:"algoId"`
			SlTriggerPx *string `json:"slTriggerPx,omitempty"`
			TpTriggerPx *string `json:"tpTriggerPx,omitempty"`
		} `json:"attachAlgoOrds,omitempty"`
		// Fields for algo order responses
		AlgoID      string `json:"algoId,omitempty"`      // For algo orders like move_order_stop
		AlgoClOrdID string `json:"algoClOrdId,omitempty"` // Client order ID for algo orders
		ClOrdID     string `json:"clOrdId,omitempty"`     // Client order ID
		SCode       string `json:"sCode,omitempty"`       // Success code
		SMsg        string `json:"sMsg,omitempty"`        // Success message
		Tag         string `json:"tag,omitempty"`         // Tag
	} `json:"data"`
}

// AlgoOrderResponse represents an algorithmic order response from API
type AlgoOrderResponse struct {
	Code    string `json:"code"`
	Message string `json:"msg"`
	Data    []struct {
		AlgoID      string `json:"algoId"`
		AlgoClOrdID string `json:"algoClOrdId"`
		ClOrdID     string `json:"clOrdId"`
		SCode       string `json:"sCode"`
		SMsg        string `json:"sMsg"`
		Tag         string `json:"tag"`
	} `json:"data"`
}

// OrderInfo represents order information
type OrderInfo struct {
	OrderID    string      `json:"ordId"`
	InstID     string      `json:"instId"`
	Side       OrderSide   `json:"side"`
	Price      *float64    `json:"px,omitempty"`        // Change to pointer to handle empty values
	Size       float64     `json:"sz,string"`           // Keep string tag for size since it's always a number
	Status     OrderStatus `json:"state"`
	CreateTime int64       `json:"-"`
	AvgPx      *float64    `json:"avgPx,omitempty"`     // Add common fields that might be empty
	Fee        *float64    `json:"fee,omitempty"`       // Add common fields that might be empty
	PxUsd      *float64    `json:"pxUsd,omitempty"`     // Add common fields that might be empty
	PxVol      *float64    `json:"pxVol,omitempty"`     // Add common fields that might be empty
	SlOrdPx    *float64    `json:"slOrdPx,omitempty"`   // Add common fields that might be empty
	TpOrdPx    *float64    `json:"tpOrdPx,omitempty"`   // Add common fields that might be empty
}

// Custom unmarshaling to handle Unix timestamp in milliseconds
func (o *OrderInfo) UnmarshalJSON(data []byte) error {
	// Define an alias to avoid recursion during unmarshaling
	type Alias OrderInfo

	aux := &struct {
		CreateTime string  `json:"cTime"`
		Price      *string `json:"px"`        // Temporarily store as string
		AvgPx      *string `json:"avgPx"`     // Temporarily store as string
		Fee        *string `json:"fee"`       // Temporarily store as string
		PxUsd      *string `json:"pxUsd"`     // Temporarily store as string
		PxVol      *string `json:"pxVol"`     // Temporarily store as string
		SlOrdPx    *string `json:"slOrdPx"`   // Temporarily store as string
		TpOrdPx    *string `json:"tpOrdPx"`   // Temporarily store as string
		*Alias
	}{
		Alias: (*Alias)(o),
	}

	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	// Parse the timestamp string to int64
	if aux.CreateTime != "" {
		timestamp, err := strconv.ParseInt(aux.CreateTime, 10, 64)
		if err != nil {
			return err
		}
		o.CreateTime = timestamp
	}

	// Parse Price from string to float64 if not empty
	if aux.Price != nil && *aux.Price != "" {
		price, err := strconv.ParseFloat(*aux.Price, 64)
		if err != nil {
			return err
		}
		o.Price = &price
	}

	// Parse AvgPx from string to float64 if not empty
	if aux.AvgPx != nil && *aux.AvgPx != "" {
		avgPx, err := strconv.ParseFloat(*aux.AvgPx, 64)
		if err != nil {
			return err
		}
		o.AvgPx = &avgPx
	}

	// Parse Fee from string to float64 if not empty
	if aux.Fee != nil && *aux.Fee != "" {
		fee, err := strconv.ParseFloat(*aux.Fee, 64)
		if err != nil {
			return err
		}
		o.Fee = &fee
	}

	// Parse PxUsd from string to float64 if not empty
	if aux.PxUsd != nil && *aux.PxUsd != "" {
		pxUsd, err := strconv.ParseFloat(*aux.PxUsd, 64)
		if err != nil {
			return err
		}
		o.PxUsd = &pxUsd
	}

	// Parse PxVol from string to float64 if not empty
	if aux.PxVol != nil && *aux.PxVol != "" {
		pxVol, err := strconv.ParseFloat(*aux.PxVol, 64)
		if err != nil {
			return err
		}
		o.PxVol = &pxVol
	}

	// Parse SlOrdPx from string to float64 if not empty
	if aux.SlOrdPx != nil && *aux.SlOrdPx != "" {
		slOrdPx, err := strconv.ParseFloat(*aux.SlOrdPx, 64)
		if err != nil {
			return err
		}
		o.SlOrdPx = &slOrdPx
	}

	// Parse TpOrdPx from string to float64 if not empty
	if aux.TpOrdPx != nil && *aux.TpOrdPx != "" {
		tpOrdPx, err := strconv.ParseFloat(*aux.TpOrdPx, 64)
		if err != nil {
			return err
		}
		o.TpOrdPx = &tpOrdPx
	}

	return nil
}

// GetPrice returns avgPx if px is nil or 0, otherwise returns the original price
func (o *OrderInfo) GetPrice() float64 {
	if o.AvgPx != nil && *o.AvgPx > 0 {
		return *o.AvgPx
	}
	if o.Price != nil && *o.Price > 0 {
		return *o.Price
	}
	return 0
}

// GetAvgPx returns the average price value or 0 if nil
func (o *OrderInfo) GetAvgPx() float64 {
	if o.AvgPx != nil {
		return *o.AvgPx
	}
	return 0
}

// GetFee returns the fee value or 0 if nil
func (o *OrderInfo) GetFee() float64 {
	if o.Fee != nil {
		return *o.Fee
	}
	return 0
}

// GetCreateTime converts the Unix timestamp to time.Time
func (o *OrderInfo) GetCreateTime() time.Time {
	return time.Unix(0, o.CreateTime*int64(time.Millisecond))
}

// PositionInfo represents position information
type PositionInfo struct {
	InstID   string    `json:"instId"`
	Side     OrderSide `json:"posSide"`
	Size     float64   `json:"pos,string"`
	AvgPrice float64   `json:"avgPx,string"`
	MarkPx   float64   `json:"markPx,string"`
	UPL      float64   `json:"upl,string"`
}

// BalanceDetail represents balance details for a specific currency
type BalanceDetail struct {
	Currency     string  `json:"ccy"`
	Equity       float64 `json:"eq,string"`
	Availablebal float64 `json:"availBal,string"`
}

// BalanceInfo represents balance information
type BalanceInfo struct {
	Currency     string          `json:"ccy"`
	Equity       float64         `json:"eq,string"`
	Availablebal float64         `json:"availBal,string"`
	Details      []BalanceDetail `json:"details"`
}

// OrderBook represents order book information
type OrderBook struct {
	InstID string     `json:"instId"`
	Bids   [][]string `json:"bids"` // [price, size, count, ...]
	Asks   [][]string `json:"asks"` // [price, size, count, ...]
	Ts     string     `json:"ts"`
}

// TickerInfo represents ticker information
type TickerInfo struct {
	InstID string  `json:"instId"`
	Last   float64 `json:"last,string"`
	Bid    float64 `json:"bidPx,string"`
	Ask    float64 `json:"askPx,string"`
}

// Candle represents OHLCV candle data
type Candle struct {
	Timestamp   time.Time
	Open        float64
	High        float64
	Low         float64
	Close       float64
	Volume      float64
	QuoteVolume float64
}

// NewCandleFromRaw creates a candle from raw OKX data
func NewCandleFromRaw(raw []string) (*Candle, error) {
	if len(raw) < 8 {
		return nil, fmt.Errorf("insufficient raw data")
	}

	timestamp, err := strconv.ParseInt(raw[0], 10, 64)
	if err != nil {
		return nil, err
	}

	open, _ := strconv.ParseFloat(raw[1], 64)
	high, _ := strconv.ParseFloat(raw[2], 64)
	low, _ := strconv.ParseFloat(raw[3], 64)
	close, _ := strconv.ParseFloat(raw[4], 64)
	volume, _ := strconv.ParseFloat(raw[5], 64)
	quoteVolume, _ := strconv.ParseFloat(raw[7], 64)

	return &Candle{
		Timestamp:   time.Unix(0, timestamp*int64(time.Millisecond)),
		Open:        open,
		High:        high,
		Low:         low,
		Close:       close,
		Volume:      volume,
		QuoteVolume: quoteVolume,
	}, nil
}

// IsCompleted checks if the candle is completed (more than 1 minute old for 1m timeframe)
func (c *Candle) IsCompleted() bool {
	return c.Timestamp.Add(1 * time.Minute).Before(time.Now().UTC())
}

// TradeSignal represents a trading signal
type TradeSignal struct {
	Timestamp      time.Time
	Side           OrderSide
	EntryPrice     float64
	Size           float64
	StopLoss       float64
	TakeProfit     float64
	Strategy       string
	IsTrailingStop bool
}

// Position represents a trading position
type Position struct {
	ID                string
	Side              OrderSide
	EntryPrice        float64
	Size              float64
	StopLoss          float64
	TakeProfit        float64
	Status            PositionStatus
	OpenTime          time.Time
	CloseTime         *time.Time
	ClosePrice        *float64
	PnL               *float64
	OrderID           string
	StopLossOrderID   string
	TakeProfitOrderID string
	IsTrailingStop    bool
	StopLossAlgoID    string
	TakeProfitAlgoID  string
	// Дополнительные поля для трейлинга по Pine-скрипту
	PeakPrice     float64  // Лучшая цена после входа
	TrailOffset   float64  // Отступ для трейлинга (ATR * multiplier)
	EntryATR      float64  // ATR на момент входа
	LastLoggedSL  float64  `json:"-"` // последний залогированный уровень SL для трейлинга
	TpReached     bool     `json:"-"` // флаг: коснулись TP
}

// TradingViewSignal represents a webhook signal from TradingView
type TradingViewSignal struct {
	Side  string  `json:"Side"`
	Price float64 `json:"Price"`
	Time  int64   `json:"Time"`
}