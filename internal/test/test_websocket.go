package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const PublicURL = "wss://ws.okx.com:8443/ws/v5/business"

func testWebsocket() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	u, _ := url.Parse(PublicURL)
	c, _, err := websocket.DefaultDialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	// Subscribe exactly as needed
	sub := map[string]interface{}{
		"op": "subscribe",
		"args": []map[string]string{
			{"channel": "candle30m", "instId": "SOL-USDT"},
		},
	}
	
	fmt.Printf("Sending subscription: %+v\n", sub)
	
	if err := c.WriteJSON(sub); err != nil {
		log.Fatal("subscribe:", err)
	}

	// Listen for responses including errors
	for {
		_, msg, err := c.ReadMessage()
		if err != nil {
			log.Fatal("read:", err)
		}

		fmt.Printf("Received: %s\n", msg)

		var resp struct {
			Event string `json:"event"`
			Arg   struct {
				Channel string `json:"channel"`
			} `json:"arg"`
			Data [][]string `json:"data"`
			Code string     `json:"code"`
			Msg  string     `json:"msg"`
		}
		if err := json.Unmarshal(msg, &resp); err != nil {
			continue
		}

		// Check for error response
		if resp.Event == "error" {
			log.Fatalf("WebSocket error: %s", resp.Msg)
		}

		// Check for successful subscription
		if resp.Event == "subscribe" && resp.Arg.Channel == "candle30m" {
			log.Println("✅ Successfully subscribed to candle30m!")
		}

		// Process candle data
		if resp.Arg.Channel == "candle30m" && len(resp.Data) > 0 {
			raw := resp.Data[0]
			ts, _ := strconv.ParseInt(raw[0], 10, 64)
			close, _ := strconv.ParseFloat(raw[4], 64)
			volume, _ := strconv.ParseFloat(raw[5], 64)
			fmt.Printf("🕯️  %s  O=%.4f H=%.4f L=%.4f C=%.4f V=%.1f\n", 
				time.UnixMilli(ts).Format("15:04"), 
				close, 
				parseFloat(raw[2]), 
				parseFloat(raw[3]), 
				close, 
				volume)
		}
	}
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}