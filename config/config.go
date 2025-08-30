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
	RiskPercent     float64 `json:"RiskPercent"`
	RewardRatio     float64 `json:"RewardRatio"`
	RSILength       int     `json:"RsiLength"`
	ATRLength       int     `json:"AtrLength"`
	UseTrailingStop bool    `json:"UseTrailingStop"`
	TrailingPercent float64 `json:"TrailingPercent"`
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
