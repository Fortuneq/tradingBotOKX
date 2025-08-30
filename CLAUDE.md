# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is a C# trading bot application for the OKX cryptocurrency exchange that implements automated trading strategies for the SOL-USDT pair. The bot uses ASP.NET Core for webhook endpoints and real-time WebSocket connections for market data.

## Architecture

### Core Components

- **TradingBot** (`TradingBot` class in `botautotraide.cs:1427-1625`): Main orchestrator that coordinates market data, strategy execution, and order management
- **Market Data Service** (`OKXMarketDataService` class in `botautotraide.cs:173-504`): Handles WebSocket connections to OKX for real-time price data and historical candle data
- **REST Client** (`OKXRestClient` class in `botautotraide.cs:549-929`): Manages HTTP API calls for order placement, position management, and account data
- **Trading Strategy** (`AdvancedTradingStrategy` class in `botautotraide.cs:944-1424`): Implements RSI + VWAP rejection strategy with risk management

### Key Design Patterns

- **Async/Await**: Extensive use of async patterns throughout for non-blocking I/O operations
- **Event-driven Architecture**: Uses events for real-time market data updates and trading signals
- **Circular Buffer**: Custom `CircularBuffer<T>` implementation for efficient historical data storage
- **Record Types**: Heavy use of C# records for immutable data structures (orders, positions, signals)

## Configuration

Configuration is managed via `appsettings.json` with the following structure:
- `OKX` section: API credentials and trading instrument settings
- `Strategy` section: Trading strategy parameters including risk management settings

## Common Development Commands

### Running the Application
```bash
dotnet run
```

### Building
```bash
dotnet build
```

### Dependencies
The project uses:
- ASP.NET Core for web endpoints
- Newtonsoft.Json for JSON serialization
- Native WebSocket client for real-time data
- HMAC-SHA256 for OKX API authentication

## API Integration

The bot integrates with OKX's REST API v5 and WebSocket API:
- **REST endpoints**: Order management, account data, historical candles
- **WebSocket channels**: Real-time ticker data for price updates
- **Authentication**: Uses API key, secret, and passphrase with HMAC signatures

## Trading Logic

- **Entry Signals**: RSI crossover (2-period) combined with VWAP rejection patterns
- **Risk Management**: ATR-based position sizing with configurable risk percentage
- **Exit Strategy**: Stop-loss and take-profit orders, with optional trailing stop functionality
- **Position Management**: Single position limit with timeout handling

## Key Files

- `Program.cs`: Application entry point and webhook endpoints setup
- `botautotraide.cs`: All trading logic, API clients, and strategy implementation
- `appsettings.json`: Configuration file for API credentials and strategy parameters