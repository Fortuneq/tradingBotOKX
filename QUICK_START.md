# ⚡ Быстрый старт - Тестирование Go Trading Bot

## 🚀 Простое тестирование (1 минута)

```bash
# Быстрый тест торговой логики  
go run demo_trading_logic.go
```

Этот тест покажет:
- ✅ Как рассчитываются индикаторы RSI, ATR, VWAP
- ✅ Проверку торговых условий (точно как в Pine Script)  
- ✅ Генерацию торговых сигналов LONG/SHORT
- ✅ Расчет Stop Loss и Take Profit

## 📊 Более подробные тесты

```bash
# Детальный тест индикаторов
go run test_indicators.go

# Полная симуляция стратегии (требует фикса импортов)
# go run test_strategy_demo.go
```

## 🎯 Основной бот

```bash
# Запуск полного бота (требует API ключи в appsettings.json)
go run main.go
```

## 📝 Пример вывода `demo_trading_logic.go`

```
⚡ Быстрый тест торговой логики (аналог Pine Script)
==================================================
📊 Анализируем 10 свечей, текущая цена: 189.10
⚙️  Параметры: RSI=2, Oversold=28, Overbought=72

📊 Текущие индикаторы:
   Price:  189.10 (prev: 190.50)
   RSI:    25.3 (prev: 30.3)
   VWAP:   187.45
   ATR:    2.156
   Volume: 1630

🔍 Анализ условий:
   RSI Crossover (>28):  ❌ НЕТ
   RSI Crossunder (<72): ❌ НЕТ
   Bullish Rejection:    ❌ НЕТ (price > prev && price < vwap)
   Bearish Rejection:    ✅ ДА (price < prev && price > vwap)
   Volume > 500:         ✅ ДА

🎯 ТОРГОВЫЕ СИГНАЛЫ:
   LONG условие:  ❌ НЕТ
   SHORT условие: ❌ НЕТ

⏳ НЕТ СИГНАЛОВ
   Причины:
   • RSI не пересекает уровни (текущий: 25.3)

📝 Эквивалент в Pine Script:
   rsi = ta.rsi(close, 2)  // 25.3
   atr = ta.atr(14)         // 2.156
   vwapDaily = vwap         // 187.45
   
   bearishRejection = close < close[1] and close > vwapDaily
   bullishRejection = close > close[1] and close < vwapDaily
   
   longCond = ta.crossover(rsi, 28) and volume > 50 and bullishRejection
   shortCond = ta.crossunder(rsi, 72) and volume > 50 and bearishRejection
```

## 🔧 Что дальше?

1. **Понимание логики**: Запустите `demo_trading_logic.go` несколько раз
2. **Изучение кода**: Посмотрите на Go-реализацию в `strategy/`
3. **Настройка**: Измените параметры в тестах
4. **Полный запуск**: Настройте `appsettings.json` и запустите `main.go`

## 🎓 Ключевые отличия от Pine Script

| Pine Script | Go |
|-------------|-----|
| `ta.rsi(close, 2)` | `calculateRSI(closes, 2)` |
| `ta.crossover(rsi, 28)` | `prevRSI < 28 && currentRSI >= 28` |
| `close < close[1]` | `currentPrice < prevPrice` |
| `strategy.entry("Long")` | `PlaceOrder(types.OrderSideBuy, size)` |

Логика торговли абсолютно идентичная, только синтаксис разный! 🎯