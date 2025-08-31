package client

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"okx-trading-bot/types"
)

const (
	APIBaseURL = "https://www.okx.com"
	APIVersion = "v5"
	Timeout    = 5 * time.Second
)

// RateLimitInfo stores rate limiting information
type RateLimitInfo struct {
	Limit     int
	Remaining int
	ResetTime time.Time
}

// OKXRestClient handles REST API requests to OKX
type OKXRestClient struct {
	apiKey     string
	apiSecret  string
	passphrase string
	httpClient *http.Client
	rateLimits sync.Map
}

// NewOKXRestClient creates a new OKX REST client
func NewOKXRestClient(apiKey, apiSecret, passphrase string) *OKXRestClient {
	return &OKXRestClient{
		apiKey:     apiKey,
		apiSecret:  apiSecret,
		passphrase: passphrase,
		httpClient: &http.Client{
			Timeout: Timeout,
		},
	}
}

// PlaceOrder places a market or limit order
func (c *OKXRestClient) PlaceOrder(order *types.OrderRequest) (*types.OrderResponse, error) {
	endpoint := fmt.Sprintf("/api/%s/trade/order", APIVersion)
	return c.sendRequest("POST", endpoint, order)
}

// CancelOrder cancels an existing order
func (c *OKXRestClient) CancelOrder(orderID, instID string) (bool, error) {
	endpoint := fmt.Sprintf("/api/%s/trade/cancel-order", APIVersion)
	request := map[string]string{
		"ordId":  orderID,
		"instId": instID,
	}

	resp, err := c.sendRequest("POST", endpoint, request)
	if err != nil {
		return false, err
	}
	return resp.Code == "0", nil
}

// GetOrderInfo retrieves order information
func (c *OKXRestClient) GetOrderInfo(orderID, instID string) (*types.OrderInfo, error) {
	endpoint := fmt.Sprintf("/api/%s/trade/order?ordId=%s&instId=%s", APIVersion, orderID, instID)

	var response struct {
		Code    string            `json:"code"`
		Message string            `json:"msg"`
		Data    []types.OrderInfo `json:"data"`
	}

	if err := c.sendRequestAndDecode("GET", endpoint, nil, &response); err != nil {
		return nil, err
	}

	if len(response.Data) == 0 {
		return &types.OrderInfo{
			OrderID: orderID,
			InstID:  instID,
			Status:  types.OrderStatusCanceled,
		}, nil
	}

	return &response.Data[0], nil
}

// GetPositions retrieves open positions
func (c *OKXRestClient) GetPositions(instType string) ([]types.PositionInfo, error) {
	endpoint := fmt.Sprintf("/api/%s/account/positions?instType=%s", APIVersion, instType)

	var response struct {
		Code    string               `json:"code"`
		Message string               `json:"msg"`
		Data    []types.PositionInfo `json:"data"`
	}

	if err := c.sendRequestAndDecode("GET", endpoint, nil, &response); err != nil {
		return nil, err
	}

	return response.Data, nil
}

// GetSpotBalance retrieves spot trading balance for specific currency
func (c *OKXRestClient) GetSpotBalance(currency string) (*types.BalanceInfo, error) {
	endpoint := fmt.Sprintf("/api/%s/account/balance", APIVersion)
	if currency != "" {
		endpoint += "?ccy=" + currency
	}

	var response struct {
		Code    string              `json:"code"`
		Message string              `json:"msg"`
		Data    []types.BalanceInfo `json:"data"`
	}

	if err := c.sendRequestAndDecode("GET", endpoint, nil, &response); err != nil {
		return nil, err
	}

	if len(response.Data) == 0 {
		return &types.BalanceInfo{Currency: currency}, nil
	}

	return &response.Data[0], nil
}

// GetBalance retrieves account balance
func (c *OKXRestClient) GetBalance(currency string) (*types.BalanceInfo, error) {
	endpoint := fmt.Sprintf("/api/%s/account/balance", APIVersion)
	if currency != "" {
		endpoint += "?ccy=" + currency
	}

	var response struct {
		Code    string              `json:"code"`
		Message string              `json:"msg"`
		Data    []types.BalanceInfo `json:"data"`
	}

	if err := c.sendRequestAndDecode("GET", endpoint, nil, &response); err != nil {
		return nil, err
	}

	if len(response.Data) == 0 {
		return &types.BalanceInfo{Currency: currency}, nil
	}

	return &response.Data[0], nil
}

// GetTicker retrieves ticker information
func (c *OKXRestClient) GetTicker(instID string) (*types.TickerInfo, error) {
	endpoint := fmt.Sprintf("/api/%s/market/ticker?instId=%s", APIVersion, instID)

	var response struct {
		Code    string             `json:"code"`
		Message string             `json:"msg"`
		Data    []types.TickerInfo `json:"data"`
	}

	if err := c.sendRequestAndDecode("GET", endpoint, nil, &response); err != nil {
		return nil, err
	}

	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no ticker data found")
	}

	return &response.Data[0], nil
}

// PlaceAlgoOrder places an algorithmic order (stop-loss/take-profit)
func (c *OKXRestClient) PlaceAlgoOrder(order *types.AlgoOrderRequest) (*types.OrderResponse, error) {
	endpoint := fmt.Sprintf("/api/%s/trade/order-algo", APIVersion)
	return c.sendRequest("POST", endpoint, order)
}

// CancelAlgoOrder cancels an algorithmic order
func (c *OKXRestClient) CancelAlgoOrder(algoID, instID string) (bool, error) {
	endpoint := fmt.Sprintf("/api/%s/trade/cancel-algos", APIVersion)
	request := map[string]string{
		"algoId": algoID,
		"instId": instID,
	}

	resp, err := c.sendRequest("POST", endpoint, request)
	if err != nil {
		return false, err
	}
	return resp.Code == "0", nil
}

// PlaceTrailingStop places a trailing stop order
func (c *OKXRestClient) PlaceTrailingStop(instID string, closeSide types.OrderSide, size, activationPrice float64) (*types.OrderResponse, error) {
	endpoint := fmt.Sprintf("/api/%s/trade/order-algo", APIVersion)

	body := map[string]interface{}{
		"instId":    instID,
		"tdMode":    "cash", // Spot trading mode
		"side":      string(closeSide),
		"ordType":   "move_order_stop",
		"sz":        strconv.FormatFloat(size, 'f', -1, 64),
		"triggerPx": strconv.FormatFloat(activationPrice, 'f', -1, 64),
		"orderPx":   "-1", // market execution
	}

	return c.sendRequest("POST", endpoint, body)
}

// GetHistoricalCandles retrieves historical candlestick data
func (c *OKXRestClient) GetHistoricalCandles(instID, timeframe string, limit int) ([]*types.Candle, error) {
	endpoint := fmt.Sprintf("/api/%s/market/candles?instId=%s&bar=%s&limit=%d", APIVersion, instID, timeframe, limit)

	var response struct {
		Code    string     `json:"code"`
		Message string     `json:"msg"`
		Data    [][]string `json:"data"`
	}

	if err := c.sendRequestAndDecode("GET", endpoint, nil, &response); err != nil {
		return nil, err
	}

	var candles []*types.Candle
	for _, raw := range response.Data {
		candle, err := types.NewCandleFromRaw(raw)
		if err != nil {
			continue
		}
		candles = append(candles, candle)
	}

	// Sort by timestamp (oldest first)
	for i := len(candles)/2 - 1; i >= 0; i-- {
		opp := len(candles) - 1 - i
		candles[i], candles[opp] = candles[opp], candles[i]
	}

	return candles, nil
}

// sendRequest sends a request and returns OrderResponse
func (c *OKXRestClient) sendRequest(method, endpoint string, body interface{}) (*types.OrderResponse, error) {
	var response types.OrderResponse
	err := c.sendRequestAndDecode(method, endpoint, body, &response)
	return &response, err
}

// sendRequestAndDecode sends a request and decodes the response
func (c *OKXRestClient) sendRequestAndDecode(method, endpoint string, body interface{}, result interface{}) error {
	// Wait for rate limit
	if err := c.waitForRateLimit(endpoint); err != nil {
		return err
	}

	// Prepare request body
	var reqBody []byte
	var err error
	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	// Generate signature
	timestamp := c.getISOTimestamp()
	signature := c.generateSignature(timestamp, method, endpoint, string(reqBody))

	// Create HTTP request
	fullURL := APIBaseURL + endpoint
	req, err := http.NewRequest(method, fullURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("OK-ACCESS-KEY", c.apiKey)
	req.Header.Set("OK-ACCESS-SIGN", signature)
	req.Header.Set("OK-ACCESS-TIMESTAMP", timestamp)
	req.Header.Set("OK-ACCESS-PASSPHRASE", c.passphrase)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Send request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Update rate limits
	c.updateRateLimits(endpoint, resp)

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Check for HTTP errors
	if resp.StatusCode >= 400 {
		var errorResp struct {
			Code    string `json:"code"`
			Message string `json:"msg"`
		}
		json.Unmarshal(respBody, &errorResp)
		return fmt.Errorf("API error: %s - %s", errorResp.Code, errorResp.Message)
	}

	// Decode response
	if err := json.Unmarshal(respBody, result); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return nil
}

// generateSignature generates the HMAC-SHA256 signature for authentication
func (c *OKXRestClient) generateSignature(timestamp, method, endpoint, body string) string {
	prehash := timestamp + method + endpoint + body
	h := hmac.New(sha256.New, []byte(c.apiSecret))
	h.Write([]byte(prehash))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// getISOTimestamp returns current timestamp in ISO format
func (c *OKXRestClient) getISOTimestamp() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

// waitForRateLimit waits if rate limit is exceeded
func (c *OKXRestClient) waitForRateLimit(endpoint string) error {
	key := c.getEndpointKey(endpoint)
	if limitInfo, ok := c.rateLimits.Load(key); ok {
		info := limitInfo.(RateLimitInfo)
		if info.Remaining <= 0 && time.Now().Before(info.ResetTime) {
			waitTime := time.Until(info.ResetTime)
			if waitTime > 0 {
				time.Sleep(waitTime)
			}
		}
	}
	return nil
}

// updateRateLimits updates rate limit information from response headers
func (c *OKXRestClient) updateRateLimits(endpoint string, resp *http.Response) {
	key := c.getEndpointKey(endpoint)

	limitStr := resp.Header.Get("x-ratelimit-limit")
	remainingStr := resp.Header.Get("x-ratelimit-remaining")
	resetStr := resp.Header.Get("x-ratelimit-reset")

	if limitStr == "" || remainingStr == "" || resetStr == "" {
		return
	}

	limit, _ := strconv.Atoi(limitStr)
	remaining, _ := strconv.Atoi(remainingStr)
	resetSeconds, _ := strconv.Atoi(resetStr)

	info := RateLimitInfo{
		Limit:     limit,
		Remaining: remaining,
		ResetTime: time.Now().Add(time.Duration(resetSeconds) * time.Second),
	}

	c.rateLimits.Store(key, info)
}

// getEndpointKey extracts the endpoint key without query parameters
func (c *OKXRestClient) getEndpointKey(endpoint string) string {
	if idx := bytes.IndexByte([]byte(endpoint), '?'); idx >= 0 {
		return endpoint[:idx]
	}
	return endpoint
}
