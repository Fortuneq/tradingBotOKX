# 🧪 Тестирование торговой стратегии Go

Эти тестовые файлы помогут вам понять как работает Go-версия торгового бота и сравнить логику с Pine Script.

## 📁 Тестовые файлы

### 1. `test_indicators.go` - Тест технических индикаторов

Простой тест расчета индикаторов RSI, ATR, VWAP и проверки торговых условий.

```bash
# Запуск теста индикаторов
go run test_indicators.go
```

**Что показывает:**
- ✅ Расчет RSI (2-периодный как в Pine Script)
- ✅ Расчет ATR (14-периодный) для стоп-лоссов
- ✅ Расчет VWAP для определения уровней
- ✅ Проверку всех торговых условий
- ✅ Генерацию торговых сигналов

### 2. `test_strategy_demo.go` - Симуляция стратегии

Полная симуляция работы торговой стратегии с тестовыми данными.

```bash
# Запуск симуляции стратегии  
go run test_strategy_demo.go
```

**Что показывает:**
- 🔄 Обработку потока свечей
- 📊 Расчет индикаторов в реальном времени
- 🎯 Генерацию торговых сигналов
- 📈 Управление позициями
- 💰 Расчет P&L

## 🎯 Сравнение с Pine Script

### Pine Script логика:
```pinescript
// LONG условие
longCond = ta.crossover(rsi, rsiOversold) and volume > minVol and bullishRejection and canLong

// SHORT условие  
shortCond = ta.crossunder(rsi, rsiOverbought) and volume > minVol and bearishRejection and canShort

// Rejection patterns
bearishRejection = close < close[1] and close > vwapDaily
bullishRejection = close > close[1] and close < vwapDaily
```

### Go эквивалент:
```go
// LONG условие
rsiCrossover := prevRSI < 28 && currentRSI >= 28
bullishRejection := candle.Close > prevCandle.Close && candle.Close < vwap
longCond := rsiCrossover && candle.Volume > 500 && bullishRejection

// SHORT условие
rsiCrossunder := prevRSI > 72 && currentRSI <= 72  
bearishRejection := candle.Close < prevCandle.Close && candle.Close > vwap
shortCond := rsiCrossunder && candle.Volume > 500 && bearishRejection
```

## 📊 Пример вывода

### Тест индикаторов:
```
🔬 Тестирование технических индикаторов
=======================================
📊 Входные данные: 30 цен от 179.30 до 196.20

🎯 RSI (2-периодный как в Pine Script):
Цена    | RSI-2  | Сигнал
--------|-------|--------
188.90 | 45.3  | -
192.10 | 78.1  | 🔴 SHORT

📏 ATR (14-периодный):
Current ATR: 0.943
SL Distance (ATR × 1.5): 1.415

⚖️ VWAP (Volume Weighted Average Price):
Current Price: 192.10
VWAP:         188.45
Difference:   3.65 (1.94%)

🎯 Проверка торговых условий:
Условие              | Статус
---------------------|--------
RSI Crossover (28)   | ❌ НЕТ
RSI Crossunder (72)  | ✅ ДА
Bullish Rejection    | ❌ НЕТ
Bearish Rejection    | ✅ ДА
Volume > 500         | ✅ ДА

🔻 ТОРГОВЫЙ СИГНАЛ: SHORT!
Entry: 192.10
Stop Loss: 193.52
Take Profit: 190.68
```

### Симуляция стратегии:
```
🧪 Тестирование торговой стратегии Go
=====================================
✅ Стратегия инициализирована с 200 историческими свечами
⚙️ Параметры: RSI=2, ATR=14, Трейлинг=1.5%

📊 Симуляция входящих свечей:
Time     | OHLC               | RSI  | VWAP    | Сигнал
---------|-------------------|------|---------|--------
14:30    | O:185.2 H:186.1 L:184.3 C:185.8 | 67.2 | 185.45 | -
14:50    | O:187.1 H:188.2 L:186.4 C:187.9 | 24.1 | 186.12 | 🟢 LONG
15:10    | O:189.3 H:190.5 L:188.7 C:189.8 | 81.3 | 187.23 | 🔴 SHORT

📈 Результаты симуляции:
• Обработано свечей: 150
• Сгенерировано сигналов: 8
• Позиции: 8

📋 Позиции:
  🟢 Открыта | buy | 0.054 @ 185.80 | PnL: N/A
  🔴 Закрыта | sell | 0.032 @ 189.40 | PnL: 12.45 USDT
```

## 🔧 Как использовать

1. **Понимание логики**: Запустите `test_indicators.go` чтобы увидеть как рассчитываются индикаторы

2. **Проверка стратегии**: Запустите `test_strategy_simulation.go` для полной симуляции

3. **Модификация параметров**: Измените параметры в тестах:
   ```go
   cfg := &config.StrategyConfig{
       RiskPercent:     2.0,    // Измените риск
       RSILength:       14,     // Измените период RSI  
       ATRLength:       20,     // Измените период ATR
       TrailingPercent: 2.0,    // Измените трейлинг
   }
   ```

4. **Добавление данных**: Замените `generateTestData()` на реальные исторические данные

## 🎓 Что вы изучите

- ✅ Как работают технические индикаторы в Go
- ✅ Логику генерации торговых сигналов  
- ✅ Управление рисками и позициями
- ✅ Разницу между Go и Pine Script подходами
- ✅ Архитектуру торгового бота

## 📝 Следующие шаги

После понимания тестов вы можете:

1. **Запустить полный бот**: `go run main.go`
2. **Модифицировать стратегию** в `strategy/advanced_strategy.go`
3. **Добавить новые индикаторы** в `strategy/indicators.go`
4. **Создать свои тесты** по образцу этих файлов

---

💡 **Совет**: Используйте эти тесты для отладки торговой логики перед запуском на реальном рынке!