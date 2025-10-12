package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"okx-trading-bot/config"
)

// PositionModeResponse represents the API response
type PositionModeResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		PosMode string `json:"posMode"`
	} `json:"data"`
}

// AccountConfigResponse represents the account config response
type AccountConfigResponse struct {
	Code string `json:"code"`
	Msg  string `json:"msg"`
	Data []struct {
		PosMode string `json:"posMode"`
		// Add other fields as needed
	} `json:"data"`
}

// OKXAPIClient handles OKX API requests
type OKXAPIClient struct {
	apiKey     string
	secretKey  string
	passphrase string
	baseURL    string
	httpClient *http.Client
}

// NewOKXAPIClient creates a new OKX API client
func NewOKXAPIClient(apiKey, secretKey, passphrase string) *OKXAPIClient {
	return &OKXAPIClient{
		apiKey:     apiKey,
		secretKey:  secretKey,
		passphrase: passphrase,
		baseURL:    "https://www.okx.com",
		httpClient: &http.Client{Timeout: 10 * time.Second},
	}
}

// generateSignature creates HMAC-SHA256 signature for OKX API
func (c *OKXAPIClient) generateSignature(timestamp, method, requestPath, body string) string {
	message := timestamp + method + requestPath + body
	h := hmac.New(sha256.New, []byte(c.secretKey))
	h.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// makeRequest makes an authenticated request to OKX API
func (c *OKXAPIClient) makeRequest(ctx context.Context, method, path string, body interface{}) ([]byte, error) {
	var bodyBytes []byte
	var err error

	if body != nil {
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Generate timestamp and signature
	timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	signature := c.generateSignature(timestamp, method, path, string(bodyBytes))

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("OK-ACCESS-KEY", c.apiKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.passphrase)

	// Make request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	return respBody, nil
}

// GetAccountConfig gets current account configuration
func (c *OKXAPIClient) GetAccountConfig(ctx context.Context) (*AccountConfigResponse, error) {
	respBody, err := c.makeRequest(ctx, "GET", "/api/v5/account/config", nil)
	if err != nil {
		return nil, err
	}

	var response AccountConfigResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &response, nil
}

// SetPositionMode sets the position mode (net_mode or long_short_mode)
func (c *OKXAPIClient) SetPositionMode(ctx context.Context, posMode string) (*PositionModeResponse, error) {
	body := map[string]string{
		"posMode": posMode,
	}

	respBody, err := c.makeRequest(ctx, "POST", "/api/v5/account/set-position-mode", body)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Raw API Response: %s\n", string(respBody))

	var response PositionModeResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &response, nil
}

func main() {
	// Load configuration
	cfg, err := config.LoadConfig("appsettings.json")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if len(os.Args) < 2 {
		fmt.Println("Usage: go run position_mode_switcher.go [check|net|long_short]")
		fmt.Println("  check      - Check current position mode")
		fmt.Println("  net        - Switch to net mode")
		fmt.Println("  long_short - Switch to long/short mode")
		os.Exit(1)
	}

	action := os.Args[1]

	// Create API client
	client := NewOKXAPIClient(cfg.OKX.APIKey, cfg.OKX.APISecret, cfg.OKX.Passphrase)
	ctx := context.Background()

	switch action {
	case "check":
		fmt.Println("🔍 Checking current position mode...")
		resp, err := client.GetAccountConfig(ctx)
		if err != nil {
			log.Fatalf("Failed to get account config: %v", err)
		}

		if resp.Code != "0" {
			log.Fatalf("API error: %s - %s", resp.Code, resp.Msg)
		}

		if len(resp.Data) > 0 {
			fmt.Printf("✅ Current position mode: %s\n", resp.Data[0].PosMode)
			if resp.Data[0].PosMode == "net_mode" {
				fmt.Println("📊 Net mode is active - posSide parameter is NOT required")
			} else {
				fmt.Println("⚠️  Long/Short mode is active - posSide parameter IS required")
			}
		} else {
			fmt.Println("❌ No position mode data found")
		}

	case "net":
		fmt.Println("🔄 Switching to net mode...")
		resp, err := client.SetPositionMode(ctx, "net_mode")
		if err != nil {
			log.Fatalf("Failed to set position mode: %v", err)
		}

		if resp.Code != "0" {
			log.Fatalf("API error: %s - %s", resp.Code, resp.Msg)
		}

		fmt.Println("✅ Successfully switched to net mode!")
		fmt.Println("📊 Your trading bot will now work without needing posSide parameter")

	case "long_short":
		fmt.Println("🔄 Switching to long/short mode...")
		resp, err := client.SetPositionMode(ctx, "long_short_mode")
		if err != nil {
			log.Fatalf("Failed to set position mode: %v", err)
		}

		if resp.Code != "0" {
			log.Fatalf("API error: %s - %s", resp.Code, resp.Msg)
		}

		fmt.Println("✅ Successfully switched to long/short mode!")
		fmt.Println("⚠️  Your trading bot will now require posSide parameter in orders")

	default:
		fmt.Printf("❌ Unknown action: %s\n", action)
		fmt.Println("Available actions: check, net, long_short")
		os.Exit(1)
	}
}
