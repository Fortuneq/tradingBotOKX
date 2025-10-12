package config

import (
	"encoding/json"
	"fmt"
	"os"
)

type OKXConfig struct {
	APIKey       string `json:"ApiKey"`
	APISecret    string `json:"ApiSecret"`
	Passphrase   string `json:"Passphrase"`
	InstrumentID string `json:"InstrumentId"`
	Timeframe    string `json:"Timeframe"`
}

type StrategyConfig struct {
	RSILength           int     `json:"RSILength"`
	ATRLength           int     `json:"ATRLength"`
	RiskPercent         float64 `json:"RiskPercent"`
	RewardRatio         float64 `json:"RewardRatio"`
	TrailingPercent     float64 `json:"TrailingPercent"`
	UseTrailingStop     bool    `json:"UseTrailingStop"`
	RsiOverbought       float64 `json:"RsiOverbought"`
	RsiOversold         float64 `json:"RsiOversold"`
	CooldownBars        int     `json:"CooldownBars"`
	AllInMode           bool    `json:"AllInMode"` // новый флаг
	OrderType           string  `json:"OrderType"` // тип ордера: "market" или "limit"
	UseOCO              bool    `json:"UseOCO"` // Use OCO (One-Cancels-Other) orders instead of separate SL/TP
	UseATRTrailingStop    bool    `json:"UseATRTrailingStop"` // Use ATR-based trailing stops instead of percentage-based
	StopLossPercent     float64 `json:"StopLossPercent"`
	TakeProfitPercent   float64 `json:"TakeProfitPercent"`
	PineMode         bool    `json:"PineMode"`         // вкл. «посадку в ноль»
	EquityPercent    float64 `json:"EquityPercent"`    // 10 (% equity на сделку)
	ATRMultiplierSL  float64 `json:"ATRMultiplierSL"`  // 1.5 (SL = 1.5×ATR)
	ATRMultiplierTP  float64 `json:"ATRMultiplierTP"`  // 1.5 (TP = 1.5×ATR)
	TrailPercent     float64 `json:"TrailPercent"`     // 1.5 (% trailing от peak)
	TrailATRMult     float64 `json:"TrailATRMult"`     // 0.5 (trail = 0.5×ATR(2))
	DynamicATRMult   bool    `json:"DynamicATRMult"`   // авто-множитель
	MinATRMult       float64 `json:"MinATRMult"`       // минимум 0.5×ATR
	MaxATRMult       float64 `json:"MaxATRMult"`       // максимум 3×ATR
	MinTakeProfitUSD float64 `json:"MinTakeProfitUSD"` // минимальный TP в USD
	UseLimitOrdersForExit bool `json:"UseLimitOrdersForExit"` // использовать лимитные ордера на выходе
	TdMode           string  `json:"TdMode"`           // Trading mode: "cross" or "isolated"
}

type Config struct {
	OKX      OKXConfig      `json:"OKX"`
	Strategy StrategyConfig `json:"Strategy"`
}

func LoadConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	var config Config
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&config); err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	return &config, nil
}
