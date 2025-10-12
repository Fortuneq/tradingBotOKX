package main

import (
	"encoding/json"
	"fmt"
	"log"
	"strconv"
)

// Copy the relevant types here to avoid import issues during testing
type OrderSide string
type OrderStatus string

const (
	OrderSideBuy  OrderSide = "buy"
	OrderSideSell OrderSide = "sell"
	
	OrderStatusNew      OrderStatus = "new"
	OrderStatusFilled   OrderStatus = "filled"
	OrderStatusCanceled OrderStatus = "canceled"
	OrderStatusRejected OrderStatus = "rejected"
)

// OrderInfo represents order information (duplicate for testing)
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

// GetPrice returns the price value or 0 if nil
func (o *OrderInfo) GetPrice() float64 {
	if o.Price != nil {
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

func main() {
	fmt.Println("🧪 Testing OrderInfo JSON unmarshaling with empty string values...")
	
	// Test JSON with empty string values that were causing the issue
	jsonData := `{
		"ordId": "123456",
		"instId": "BTC-USDT-SWAP",
		"side": "sell",
		"px": "",
		"sz": "0.2",
		"state": "filled",
		"cTime": "1700000000000",
		"avgPx": "",
		"fee": "",
		"pxUsd": "",
		"pxVol": "",
		"slOrdPx": "",
		"tpOrdPx": ""
	}`
	
	var orderInfo OrderInfo
	err := json.Unmarshal([]byte(jsonData), &orderInfo)
	if err != nil {
		log.Fatalf("❌ Error unmarshaling JSON: %v", err)
	}
	
	fmt.Printf("✅ OrderInfo unmarshaled successfully:\n")
	fmt.Printf("   OrderID: %s\n", orderInfo.OrderID)
	fmt.Printf("   InstID: %s\n", orderInfo.InstID)
	fmt.Printf("   Side: %s\n", orderInfo.Side)
	fmt.Printf("   Price: %.2f (from GetPrice())\n", orderInfo.GetPrice())
	fmt.Printf("   Size: %.2f\n", orderInfo.Size)
	fmt.Printf("   Status: %s\n", orderInfo.Status)
	fmt.Printf("   AvgPx: %.2f (from GetAvgPx())\n", orderInfo.GetAvgPx())
	fmt.Printf("   Fee: %.2f (from GetFee())\n", orderInfo.GetFee())
	
	fmt.Println("\n✅ Test passed! The updated OrderInfo struct can handle empty string values correctly.")
}