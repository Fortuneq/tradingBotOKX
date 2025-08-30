# OKX Trading Bot - Go Version

Это Go-версия автоматического торгового бота для биржи OKX, переписанная с C# на Go. Бот реализует стратегию RSI + VWAP Rejection с продвинутым управлением рисками.

## 🚀 Установка и запуск

### Требования

- Go 1.21 или выше
- Активный аккаунт на бирже OKX с API ключами
- Доступ к интернету для WebSocket соединений

### Установка зависимостей

```bash
go mod tidy
```

### Настройка конфигурации

1. Отредактируйте файл `appsettings.json`:

```json
{
  "OKX": {
    "ApiKey": "ваш_api_ключ",
    "ApiSecret": "ваш_api_секрет", 
    "Passphrase": "ваша_парольная_фраза",
    "InstrumentId": "SOL-USDT",
    "Timeframe": "30m"
  },
  "Strategy": {
    "RiskPercent": 2,
    "RewardRatio": 2.0,
    "RsiLength": 14,
    "AtrLength": 14,
    "UseTrailingStop": false,
    "TrailingPercent": 1.5
  }
}
```

### Запуск

```bash
go run main.go
```

## 📦 Архитектура Go-версии

### Структура проекта

```
okx-trading-bot/
├── main.go                    # Точка входа приложения
├── go.mod                     # Зависимости Go модуля
├── appsettings.json          # Конфигурация
├── config/
│   └── config.go             # Управление конфигурацией
├── types/
│   └── types.go              # Типы данных и структуры
├── client/
│   └── okx_client.go         # REST клиент для OKX API
├── marketdata/
│   └── websocket.go          # WebSocket клиент для рыночных данных
├── strategy/
│   ├── advanced_strategy.go  # Торговая стратегия
│   ├── indicators.go         # Технические индикаторы
│   └── circular_buffer.go    # Кольцевой буфер для данных
├── bot/
│   └── trading_bot.go        # Основной оркестратор бота
└── handlers/
    └── webhooks.go           # HTTP обработчики для API
```

### Ключевые отличия от C# версии

**1. Горутины вместо Task/async-await:**
```go
// Асинхронная обработка сигналов
go s.GenerateSignal(types.OrderSideBuy, candle, atr, vwap, "RSI+VWAP")

// Периодические задачи
go bot.pollCandles()
go bot.updateTrailingStopsOnExchange()
```

**2. Каналы для синхронизации:**
```go
// Graceful shutdown через контекст
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

// Ожидание сигнала завершения
sigChan := make(chan os.Signal, 1)
signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
<-sigChan
```

**3. Интерфейсы вместо наследования:**
```go
type MarketDataService interface {
    Initialize(ctx context.Context) error
    GetHistoricalData(limit int) ([]*types.Candle, error)
    Close() error
}

type TradingStrategy interface {
    Initialize(ctx context.Context) error
    Update(candle *types.Candle)
    GetPositions() []*types.Position
}
```

**4. Обработка ошибок:**
```go
// Явная проверка ошибок
if err := bot.Start(ctx); err != nil {
    log.Fatalf("Failed to start bot: %v", err)
}
```

## 🛠 Основные компоненты

### 1. Market Data Service (`marketdata/websocket.go`)

Обрабатывает WebSocket соединения с OKX:
- Аутентификация через HMAC-SHA256
- Подписка на тикеры реального времени
- Автоматические переподключения
- Получение исторических данных через REST

### 2. Trading Strategy (`strategy/advanced_strategy.go`)

Реализует торговую логику:
- Расчет индикаторов (RSI, ATR, VWAP)
- Генерация торговых сигналов
- Управление позициями и рисками
- Трейлинг-стопы

### 3. OKX REST Client (`client/okx_client.go`)

Взаимодействие с REST API OKX:
- Размещение и отмена ордеров
- Получение баланса и позиций
- Rate limiting
- Обработка ошибок API

### 4. Trading Bot (`bot/trading_bot.go`)

Главный оркестратор:
- Координация всех компонентов
- Периодический опрос данных
- Обновление трейлинг-стопов
- Graceful shutdown

### 5. HTTP Handlers (`handlers/webhooks.go`)

REST API для внешних интеграций:
- Webhook от TradingView
- Статус бота
- Информация о позициях
- Ручные торговые операции

## 🔧 API Endpoints

### Webhook от TradingView
```bash
POST http://localhost:5000/tv-signal
Content-Type: application/json

{
  "Side": "buy",
  "Price": 185.50,
  "Time": 1640995200
}
```

### Статус бота
```bash
GET http://localhost:5000/status
```

### Позиции
```bash
GET http://localhost:5000/positions
```

### Ручная торговля
```bash
POST http://localhost:5000/manual-trade
Content-Type: application/json

{
  "side": "buy",
  "size": 0.5
}
```

## 📊 Технические индикаторы

### RSI (Relative Strength Index)
```go
func (s *AdvancedTradingStrategy) calculateRSI(closes []float64, period int) []float64 {
    // Exponential moving average implementation
    avgGain = (avgGain*float64(period-1) + gain) / float64(period)
    avgLoss = (avgLoss*float64(period-1) + loss) / float64(period)
    rsi[i] = 100 - (100 / (1 + avgGain/avgLoss))
}
```

### ATR (Average True Range)
```go
func (s *AdvancedTradingStrategy) calculateATR(highs, lows, closes []float64, period int) float64 {
    trueRange := math.Max(hl, math.Max(hc, lc))
    return average(trueRanges[:period])
}
```

### VWAP (Volume Weighted Average Price)
```go
func (s *AdvancedTradingStrategy) calculateDailyVWAP(candles []*types.Candle) float64 {
    typicalPrice := (high + low + close) / 3.0
    return sum(typicalPrice * volume) / sum(volume)
}
```

## ⚡ Производительность

**Преимущества Go версии:**
- **Память**: Меньше использование памяти благодаря отсутствию GC давления
- **Конкурентность**: Легковесные горутины вместо тяжелых потоков
- **Сборка**: Один исполняемый файл без внешних зависимостей
- **Производительность**: Компилируемый язык с оптимизацией

**Бенчмарки:**
```bash
go test -bench=. ./strategy/
```

## 🔒 Безопасность

**API ключи:**
- Храните в переменных окружения в продакшене
- Используйте только necessary permissions
- Ротируйте ключи регулярно

**Сетевая безопасность:**
- HTTPS для всех внешних запросов
- Rate limiting на API endpoints
- Валидация входных данных

## 🐳 Docker

```dockerfile
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download
RUN go build -o trading-bot .

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/trading-bot .
COPY appsettings.json .
CMD ["./trading-bot"]
```

```bash
docker build -t okx-trading-bot .
docker run -d -p 5000:5000 okx-trading-bot
```

## 📈 Monitoring

### Логирование
```go
log.Printf("✅ Entry %s %.3f @ %.2f", side, size, price)
log.Printf("📊 RSI: %.2f, VWAP: %.2f, ATR: %.4f", rsi, vwap, atr)
```

### Метрики
- Количество активных позиций
- P&L по сделкам
- Статистика API вызовов
- Время выполнения стратегии

## 🚀 Deployment

### Systemd сервис
```ini
[Unit]
Description=OKX Trading Bot
After=network.target

[Service]
Type=simple
User=trading
ExecStart=/opt/trading-bot/trading-bot
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

### Graceful Shutdown
Бот поддерживает корректное завершение работы:
- Закрытие WebSocket соединений
- Завершение HTTP сервера
- Сохранение состояния позиций
- Остановка всех горутин

```go
// Обработка SIGINT/SIGTERM
c := make(chan os.Signal, 1)
signal.Notify(c, os.Interrupt, syscall.SIGTERM)
<-c
```

## ⚠️ Отличия от C# версии

1. **Отсутствует Newtonsoft.Json** - используется стандартный `encoding/json`
2. **Нет LINQ** - используются циклы и функции высшего порядка
3. **Управление памятью** - ручное управление через `sync.Pool` для буферов
4. **Обработка ошибок** - явная проверка на каждом шаге
5. **Конфигурация** - JSON вместо appsettings.json механизма .NET

## 🔍 Отладка

```bash
# Запуск с подробным логированием
go run main.go -v

# Профилирование
go tool pprof http://localhost:5000/debug/pprof/profile

# Race detection
go run -race main.go
```

Эта Go версия полностью воспроизводит функциональность C# оригинала с учетом идиом и лучших практик Go.