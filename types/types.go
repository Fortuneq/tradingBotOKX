package types

import (
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
	InstID    string    `json:"instId"`
	Side      OrderSide `json:"side"`
	OrderType string    `json:"ordType"`
	Size      float64   `json:"sz,string"`
	Price     *float64  `json:"px,string,omitempty"`
}

// AlgoOrderRequest represents an algorithmic order request
type AlgoOrderRequest struct {
	InstID     string    `json:"instId"`
	Side       OrderSide `json:"side"`
	Size       float64   `json:"sz,string"`
	StopLoss   float64   `json:"slTriggerPx,string"`
	TakeProfit float64   `json:"tpTriggerPx,string"`
	OrderType  string    `json:"ordType"`
}

// OrderResponse represents an order response from API
type OrderResponse struct {
	Code    string `json:"code"`
	Message string `json:"msg"`
	Data    []struct {
		OrderID string `json:"ordId"`
	} `json:"data"`
}

// OrderInfo represents order information
type OrderInfo struct {
	OrderID    string      `json:"ordId"`
	InstID     string      `json:"instId"`
	Side       OrderSide   `json:"side"`
	Price      float64     `json:"px,string"`
	Size       float64     `json:"sz,string"`
	Status     OrderStatus `json:"state"`
	CreateTime time.Time   `json:"cTime,string"`
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

// BalanceInfo represents balance information
type BalanceInfo struct {
	Currency     string  `json:"ccy"`
	Equity       float64 `json:"eq,string"`
	Availablebal float64 `json:"availBal,string"`
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

// IsCompleted checks if the candle is completed (more than 1 minute old)
func (c *Candle) IsCompleted() bool {
	return c.Timestamp.Add(time.Minute).Before(time.Now().UTC())
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
}

// TradingViewSignal represents a webhook signal from TradingView
type TradingViewSignal struct {
	Side  string  `json:"Side"`
	Price float64 `json:"Price"`
	Time  int64   `json:"Time"`
}
