# OKX Trading Bot — RSI + VWAP strategy

An automated trading bot for the OKX exchange. It trades an **RSI + VWAP rejection** strategy with ATR-based position sizing and risk management. Originally written in C#, then rewritten in Go.

> ⚠️ For educational purposes. Trading crypto derivatives carries a high risk of loss — test on a demo account first.

## Strategy

**Long entry**
- short-period RSI crosses **above the oversold level** (`RsiOversold`)
- the candle closes higher than the previous one but **below VWAP** (bounce off VWAP)
- candle volume is above the minimum threshold

**Short entry**
- RSI crosses **below the overbought level** (`RsiOverbought`)
- the candle closes lower than the previous one but **above VWAP**
- candle volume is above the minimum threshold

**Risk management**
- position size derived from ATR with a fixed risk per trade (`RiskPercent`)
- stop loss and take profit at configurable ATR multiples (`ATRMultiplierSL`, `ATRMultiplierTP`)
- optional trailing stop
- WebSocket market data with REST fallback when the stream goes silent
- one-candle cooldown between signals to avoid overtrading

## Project structure

```
main.go                 — entry point
bot/                    — strategy and order execution
client/okx_client.go    — OKX REST client (signed requests)
marketdata/websocket.go — OKX WebSocket market data
handlers/webhooks.go    — HTTP API: TradingView signals (`POST /tv-signal`), `/ping`, `/status`
config/                 — configuration loading
Program.cs, botautotraide.cs — the original C# implementation
```

## Getting started

Requirements: Go 1.21+, an OKX account with API keys.

```bash
go mod tidy
# fill in API keys and strategy settings in appsettings.json
go run .
```

Configuration (`appsettings.json`):

```json
{
  "OKX": {
    "ApiKey": "<api key>",
    "ApiSecret": "<api secret>",
    "Passphrase": "<passphrase>",
    "InstrumentId": "SOL-USDT-SWAP",
    "Timeframe": "15m"
  },
  "Strategy": {
    "RsiLength": 2,
    "RsiOverbought": 80,
    "RsiOversold": 20,
    "ATRLength": 14,
    "ATRMultiplierSL": 1.5,
    "ATRMultiplierTP": 1.5,
    "CooldownBars": 1,
    "RiskPercent": 2
  }
}
```

Docker: `docker compose up -d --build`. See [`README-Go.md`](README-Go.md), [`QUICK_START.md`](QUICK_START.md) and [`README-Deployment.md`](README-Deployment.md).
