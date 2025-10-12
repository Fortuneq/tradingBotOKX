package client

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"okx-trading-bot/types"
)

const (
	APIBaseURL = "https://www.okx.com"
	APIVersion = "v5"
	Timeout    = 10 * time.Second
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

// GetBalance retrieves spot account balance
func (c *OKXRestClient) GetBalance(currency string) (*types.BalanceInfo, error) {
	endpoint := fmt.Sprintf("/api/%s/account/balance", APIVersion)
	queryParams := []string{}
	if currency != "" {
		queryParams = append(queryParams, "ccy="+currency)
	}
	// Для получения баланса спотового аккаунта в Unified-режиме
	queryParams = append(queryParams, "instType=SPOT")

	if len(queryParams) > 0 {
		endpoint += "?" + strings.Join(queryParams, "&")
	}

	// Создадим отдельный метод для получения raw-ответа
	resp, err := c.sendRequestAndReadRawResponse("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	log.Printf("[BALANCE-RAW] %s", string(resp))

	// Теперь распарсим ответ
	var response struct {
		Code    string              `json:"code"`
		Message string              `json:"msg"`
		Data    []types.BalanceInfo `json:"data"`
	}
	if err := json.Unmarshal(resp, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(response.Data) == 0 {
		log.Printf("[BALANCE-DEBUG] Empty data array received for currency: %s", currency)
		return &types.BalanceInfo{Currency: currency}, nil
	}

	// Проверяем, есть ли детали баланса и ищем нужную валюту
	result := &response.Data[0]
	if len(result.Details) > 0 {
		for _, detail := range result.Details {
			if detail.Currency == currency {
				// Возвращаем баланс для конкретной валюты
				return &types.BalanceInfo{
					Currency:     detail.Currency,
					Equity:       detail.Equity,
					Availablebal: detail.Availablebal,
					Details:      result.Details,
				}, nil
			}
		}
		// Если не нашли конкретную валюту в details, возвращаем первый результат
	} else {
		// Если details пустой, используем основные поля
	}

	log.Printf("[BALANCE-DEBUG] Balance data: %+v", result)
	return result, nil
}

// GetMarginBalance retrieves margin account balance
func (c *OKXRestClient) GetMarginBalance(currency string) (*types.BalanceInfo, error) {
	endpoint := fmt.Sprintf("/api/%s/account/balance", APIVersion)
	queryParams := []string{}
	if currency != "" {
		queryParams = append(queryParams, "ccy="+currency)
	}
	// Для получения баланса маржинального аккаунта в Unified-режиме
	queryParams = append(queryParams, "instType=MARGIN")

	if len(queryParams) > 0 {
		endpoint += "?" + strings.Join(queryParams, "&")
	}

	var response struct {
		Code    string              `json:"code"`
		Message string              `json:"msg"`
		Data    []types.BalanceInfo `json:"data"`
	}

	// Создадим отдельный метод для получения raw-ответа
	resp, err := c.sendRequestAndReadRawResponse("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	log.Printf("[MARGIN-BALANCE-RAW] %s", string(resp))

	// Теперь распарсим ответ
	if err := json.Unmarshal(resp, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(response.Data) == 0 {
		log.Printf("[MARGIN-BALANCE-DEBUG] Empty data array received for currency: %s", currency)
		return &types.BalanceInfo{Currency: currency}, nil
	}

	// Проверяем, есть ли детали баланса и ищем нужную валюту
	result := &response.Data[0]
	if len(result.Details) > 0 {
		for _, detail := range result.Details {
			if detail.Currency == currency {
				// Возвращаем баланс для конкретной валюты
				return &types.BalanceInfo{
					Currency:     detail.Currency,
					Equity:       detail.Equity,
					Availablebal: detail.Availablebal,
					Details:      result.Details,
				}, nil
			}
		}
		// Если не нашли конкретную валюту в details, возвращаем первый результат
	} else {
		// Если details пустой, используем основные поля
	}

	log.Printf("[MARGIN-BALANCE-DEBUG] Margin balance data: %+v", result)
	return result, nil
}

// GetIsolatedBalance retrieves isolated margin balance for a specific instrument
func (c *OKXRestClient) GetIsolatedBalance(instID string) (avail float64, err error) {
	// /api/v5/account/balance?instType=SWAP&instId=SOL-USDT-SWAP
	endpoint := fmt.Sprintf("/api/%s/account/balance?instType=SWAP&instId=%s", APIVersion, instID)
	var r struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Details []struct {
				Ccy      string `json:"ccy"`
				AvailBal string `json:"availBal"`
			} `json:"details"`
		} `json:"data"`
	}
	if err = c.sendRequestAndDecode("GET", endpoint, nil, &r); err != nil {
		return
	}
	if r.Code != "0" {
		return 0, fmt.Errorf("iso-balance: %s", r.Msg)
	}
	if len(r.Data) == 0 || len(r.Data[0].Details) == 0 {
		return 0, nil
	}
	return strconv.ParseFloat(r.Data[0].Details[0].AvailBal, 64)
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

// PlaceTrailingStop places a trailing stop order (native OKX 2025)
func (c *OKXRestClient) PlaceTrailingStop(instID string, tdMode string, closeSide types.OrderSide, size, activationPrice, callbackRatio float64) (*types.AlgoOrderResponse, error) {
	endpoint := fmt.Sprintf("/api/%s/trade/order-algo", APIVersion)

	body := map[string]interface{}{
		"instId": instID,
		"tdMode": tdMode, // "cross" or "isolated"
		"side":   string(closeSide),
		// "posSide":       mapPosSide(closeSide), // НЕ НУЖЕН в Net Mode!
		"ordType":       "move_order_stop",
		"sz":            strconv.FormatFloat(size, 'f', -1, 64),
		"activationPx":  strconv.FormatFloat(activationPrice, 'f', -1, 64),
		"callbackRatio": strconv.FormatFloat(callbackRatio, 'f', -1, 64), // ratio (e.g. 0.015 = 1.5%)
		"orderPx":       "-1",                                            // market execution
	}

	// Create a separate send request function for algo orders to get proper response type
	var response types.AlgoOrderResponse
	err := c.sendRequestAndDecode("POST", endpoint, body, &response)
	return &response, err
}

// mapPosSide maps the order side to position side
func mapPosSide(s types.OrderSide) string {
	if s == types.OrderSideBuy {
		return "long"
	}
	return "short"
}

// AmendAlgoOrder amends an existing algorithmic order (stop-loss/take-profit)
func (c *OKXRestClient) AmendAlgoOrder(instID, algoID, algoOrderType string, triggerPx, orderPx *float64) (*types.OrderResponse, error) {
	endpoint := fmt.Sprintf("/api/%s/trade/amend-algos", APIVersion)

	body := map[string]interface{}{
		"instId":  instID,
		"algoId":  algoID,
		"ordType": algoOrderType, // "conditional", "oco", "trigger", "move_order_stop"
	}

	if triggerPx != nil {
		body["triggerPx"] = strconv.FormatFloat(*triggerPx, 'f', -1, 64)
	}

	// Handle market vs limit orders correctly
	if orderPx != nil {
		// For limit orders, include the specific price
		body["ordPx"] = strconv.FormatFloat(*orderPx, 'f', -1, 64)
	} else if triggerPx != nil {
		// For market orders with a trigger price, set ordPx to "-1"
		// This is needed for SL/TP orders that should execute at market price
		body["ordPx"] = "-1"
	}

	return c.sendRequest("POST", endpoint, body)
}

// GetTrailingAlgoInfo retrieves trailing stop algo order information
func (c *OKXRestClient) GetTrailingAlgoInfo(algoID, instID string) (activationPx, callbackRatio float64, err error) {
	endpoint := fmt.Sprintf("/api/%s/trade/orders-algo-pending?algoId=%s&instId=%s", APIVersion, algoID, instID)

	var response struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			AlgoID        string `json:"algoId"`
			ActivationPx  string `json:"activationPx"`
			CallbackRatio string `json:"callbackRatio"`
		} `json:"data"`
	}

	if err := c.sendRequestAndDecode("GET", endpoint, nil, &response); err != nil {
		return 0, 0, err
	}

	if response.Code != "0" {
		return 0, 0, fmt.Errorf("API error: %s - %s", response.Code, response.Msg)
	}

	if len(response.Data) == 0 {
		return 0, 0, fmt.Errorf("no algo order found with ID: %s", algoID)
	}

	activationPx, err = strconv.ParseFloat(response.Data[0].ActivationPx, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse activation price: %w", err)
	}

	callbackRatio, err = strconv.ParseFloat(response.Data[0].CallbackRatio, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to parse callback ratio: %w", err)
	}

	return activationPx, callbackRatio, nil
}

// GetOrderBook retrieves the order book for an instrument
func (c *OKXRestClient) GetOrderBook(instID string, size int) (*types.OrderBook, error) {
	endpoint := fmt.Sprintf("/api/%s/market/books?instId=%s", APIVersion, instID)
	if size > 0 {
		endpoint += fmt.Sprintf("&sz=%d", size)
	}

	var response struct {
		Code    string            `json:"code"`
		Message string            `json:"msg"`
		Data    []types.OrderBook `json:"data"`
	}

	if err := c.sendRequestAndDecode("GET", endpoint, nil, &response); err != nil {
		return nil, err
	}

	if len(response.Data) == 0 {
		return nil, fmt.Errorf("no order book data found")
	}

	return &response.Data[0], nil
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

	// Log the full raw response for debugging
	log.Printf("[OKX-RAW-RESPONSE] %s", string(respBody))

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

// sendRequestAndReadRawResponse sends a request and returns the raw response body
func (c *OKXRestClient) sendRequestAndReadRawResponse(method, endpoint string, body interface{}) ([]byte, error) {
	// Wait for rate limit
	if err := c.waitForRateLimit(endpoint); err != nil {
		return nil, err
	}

	// Prepare request body
	var reqBody []byte
	var err error
	if body != nil {
		reqBody, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
	}

	// Generate signature
	timestamp := c.getISOTimestamp()
	signature := c.generateSignature(timestamp, method, endpoint, string(reqBody))

	// Create HTTP request
	fullURL := APIBaseURL + endpoint
	req, err := http.NewRequest(method, fullURL, bytes.NewBuffer(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
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
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Update rate limits
	c.updateRateLimits(endpoint, resp)

	// Read response
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Log the full raw response for debugging
	log.Printf("[OKX-RAW-RESPONSE] %s", string(respBody))

	// Check for HTTP errors
	if resp.StatusCode >= 400 {
		var errorResp struct {
			Code    string `json:"code"`
			Message string `json:"msg"`
		}
		json.Unmarshal(respBody, &errorResp)
		return nil, fmt.Errorf("API error: %s - %s", errorResp.Code, errorResp.Message)
	}

	return respBody, nil
}

// SetLeverage sets the leverage for a specific instrument
func (c *OKXRestClient) SetLeverage(instID, marginMode string, leverage int, posSide string) error {
	body := map[string]interface{}{
		"instId":  instID,
		"lever":   strconv.Itoa(leverage),
		"mgnMode": marginMode, // "cross" or "isolated"
	}
	if posSide != "" { // Adding posSide for hedge mode
		body["posSide"] = posSide
	}

	resp, err := c.sendRequest("POST", "/api/v5/account/set-leverage", body)
	if err != nil {
		return fmt.Errorf("set leverage request failed: %w", err)
	}

	if resp.Code != "0" {
		return fmt.Errorf("set leverage error: %s - %s", resp.Code, resp.Message)
	}

	log.Printf("✅ Set leverage to %d for %s in %s mode (posSide=%s)", leverage, instID, marginMode, posSide)
	return nil
}

// TransferToMargin transfers funds from funding account to margin account
func (c *OKXRestClient) TransferToMargin(ccy string, amt float64) error {
	const minTransfer = 1.0 // OKX минимум 1 USDT на перевод
	if amt < minTransfer {
		return fmt.Errorf("transfer amt %.4f < min %.2f", amt, minTransfer)
	}
	body := map[string]interface{}{
		"ccy":  ccy,
		"amt":  fmt.Sprintf("%.6f", amt),
		"from": "6",  // Funding
		"to":   "18", // Isolated-margin
	}

	respBody, err := c.sendRequestAndReadRawResponse("POST", "/api/v5/asset/transfer", body)
	if err != nil {
		return fmt.Errorf("transfer request failed: %w", err)
	}
	log.Printf("[TRANSFER-RAW] %s", string(respBody))

	var r struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
	}
	if err = json.Unmarshal(respBody, &r); err != nil {
		return fmt.Errorf("unmarshal transfer: %w", err)
	}
	if r.Code != "0" {
		return fmt.Errorf("transfer error %s: %s", r.Code, r.Msg)
	}
	log.Printf("✅ Transferred %.4f %s funding→isolated", amt, ccy)
	return nil
}

// GetFundingBalance returns USDT amount that really sits in funding account
func (c *OKXRestClient) GetFundingBalance() (float64, error) {
	type resp struct {
		Code string `json:"code"`
		Msg  string `json:"msg"`
		Data []struct {
			Details []struct {
				Ccy      string `json:"ccy"`
				AvailBal string `json:"availBal"`
			} `json:"details"`
		} `json:"data"`
	}
	var r resp
	if err := c.sendRequestAndDecode("GET",
		"/api/v5/asset/balances?ccy=USDT", nil, &r); err != nil {
		return 0, err
	}
	if r.Code != "0" {
		return 0, fmt.Errorf("get funding balance: %s", r.Msg)
	}
	if len(r.Data) == 0 || len(r.Data[0].Details) == 0 {
		return 0, nil
	}
	return strconv.ParseFloat(r.Data[0].Details[0].AvailBal, 64)
}

// Borrow borrows funds for margin trading
func (c *OKXRestClient) Borrow(ccy string, amt float64) error {
	body := map[string]interface{}{
		"ccy": ccy,
		"amt": fmt.Sprintf("%.6f", amt),
	}

	var response struct {
		Code    string `json:"code"`
		Message string `json:"msg"`
	}

	_, err := c.sendRequest("POST", "/api/v5/margin/borrow", body)
	if err != nil {
		return fmt.Errorf("borrow request failed: %w", err)
	}

	if response.Code != "0" {
		return fmt.Errorf("borrow error: %s - %s", response.Code, response.Message)
	}

	log.Printf("✅ Borrowed %.6f %s", amt, ccy)
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
