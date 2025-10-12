using System;
using System.IO;
using System.Buffers;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.Diagnostics;
using System.Globalization;
using System.Linq;
using System.Net;
using System.Net.Http;
using System.Net.Http.Headers;
using System.Net.WebSockets;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json;
using System.Threading;
using System.Threading.Tasks;
using Newtonsoft.Json;
using Newtonsoft.Json.Linq;
using Microsoft.Extensions.Configuration;

namespace OKXTraderBot
{
    // Перечисление направления ордера (покупка/продажа)
    public enum OrderSide { Buy, Sell }

    // Перечисление статусов ордера
    public enum OrderStatus { New, Filled, Canceled, Rejected }

    // Перечисление статусов позиции
    public enum PositionStatus { Open, Closed }

    public record OrderRequest(string InstId,        // Инструмент (напр. BTC-USDT)
                               OrderSide Side,       // Направление (Buy/Sell)
                               string OrderType,     // Тип ордера (limit, market)
                               decimal Size,         // Размер позиции
                               decimal? Price = null // Цена (для лимитных ордеров)
    );

    public record AlgoOrderRequest(string InstId,     // Инструмент
                                   OrderSide Side,    // Направление
                                   decimal Size,      // Размер
                                   decimal StopLoss,  // Цена стоп-лосса
                                   decimal TakeProfit // Цена тейк-профита
    );

    public record OrderResponse(string Code,    // Код ответа (0 - успех)
                                string Message, // Сообщение
                                string OrderId  // ID ордера
    );

    public record OrderInfo(string OrdId,       // ID ордера
                            string InstId,      // Инструмент
                            OrderSide Side,     // Направление
                            decimal Price,      // Цена
                            decimal Size,       // Размер
                            OrderStatus Status, // Статус
                            DateTime CreateTime // Время создания
    );

    public record PositionInfo(string InstId,  // Инструмент
                               OrderSide Side, // Направление
                               decimal Size,   // Размер позиции
                               decimal AvgPx,  // Средняя цена входа
                               decimal MarkPx, // Текущая цена
                               decimal Upl     // Нереализованный PnL
    );

    public record BalanceInfo(string Ccy,      // Валюта (напр. USDT)
                              decimal Equity,  // Общий баланс
                              decimal AvailBal // Доступный баланс
    );

    public record TickerInfo(string InstId, // Инструмент
                             decimal Last,  // Последняя цена
                             decimal Bid,   // Лучшая цена покупки
                             decimal Ask    // Лучшая цена продажи
    );

    // Добавляем недостающие модели ответов API
    public record OrderInfoResponse(string Code, string Message, OrderInfo[] Data);
    public record PositionInfoResponse(string Code, string Message,
                                       PositionInfo[] Data);
    public record BalanceInfoResponse(string Code, string Message,
                                      BalanceInfo[] Data);
    public record TickerInfoResponse(string Code, string Message,
                                     TickerInfo[] Data);
    public record ApiErrorResponse(string Code, string Message);

    // Исключение для API OKX
    public class OKXApiException : Exception
    {
        public string ErrorCode { get; }

        public OKXApiException(string code, string message) : base(message)
        {
            ErrorCode = code;
        }
    }

    // Модель торгового сигнала
    public record TradeSignal(DateTime Timestamp,   // Время генерации сигнала
                              OrderSide Side,       // Направление (Buy/Sell)
                              decimal EntryPrice,   // Цена входа
                              decimal Size,         // Размер позиции
                              decimal StopLoss,     // Цена стоп-лосса
                              decimal TakeProfit,   // Цена тейк-профита
                              string Strategy,      // Название стратегии
                              bool IsTrailingStop); // Использовать трейлинг-стоп

    // Модель позиции
    public record Position(string Id,          // Уникальный идентификатор позиции
                           OrderSide Side,     // Направление позиции
                           decimal EntryPrice, // Цена входа
                           decimal Size,       // Размер позиции
                           decimal StopLoss,   // Текущий стоп-лосс
                           decimal TakeProfit, // Текущий тейк-профит
                           PositionStatus Status,  // Статус (Open/Closed)
                           DateTime OpenTime,      // Время открытия
                           DateTime? CloseTime,    // Время закрытия (если закрыта)
                           decimal? ClosePrice,    // Цена закрытия
                           decimal? PnL,           // Прибыль/убыток
                           string OrderId,         // ID ордера
                           string StopLossOrderId, // ID стоп-ордера
                           string TakeProfitOrderId, // ID тейк-профит ордера
                           bool IsTrailingStop,
                           string StopLossAlgoId,   // ID алго-ордера SL
                           string TakeProfitAlgoId);  // ID алго-ордера TP);
    // Класс свечи (OHLCV данные)
    public class Candle
    {
        public DateTime Timestamp { get; init; }  // Временная метка
        public decimal Open { get; init; }        // Цена открытия
        public decimal High { get; init; }        // Максимальная цена
        public decimal Low { get; init; }         // Минимальная цена
        public decimal Close { get; init; }       // Цена закрытия
        public decimal Volume { get; init; }      // Объем в BTC
        public decimal QuoteVolume { get; init; } // Объем в USDT

        // Создание свечи из сырых данных OKX
        public static Candle FromRaw(List<string> rawCandle) => new()
        {
            Timestamp = DateTimeOffset
                          .FromUnixTimeMilliseconds(long.Parse(rawCandle[0]))
                          .UtcDateTime,
            Open = decimal.Parse(rawCandle[1], CultureInfo.InvariantCulture),
            High = decimal.Parse(rawCandle[2], CultureInfo.InvariantCulture),
            Low = decimal.Parse(rawCandle[3], CultureInfo.InvariantCulture),
            Close = decimal.Parse(rawCandle[4], CultureInfo.InvariantCulture),
            Volume = decimal.Parse(rawCandle[5], CultureInfo.InvariantCulture),
            QuoteVolume = decimal.Parse(rawCandle[7], CultureInfo.InvariantCulture)
        };

        // Проверка, завершилась ли свеча (прошло больше 1 минуты)
        public bool IsCompleted => Timestamp.AddMinutes(1) < DateTime.UtcNow;

        // Форматированный вывод свечи
        public override string ToString() =>
            $"🕒 {Timestamp:yyyy-MM-dd HH:mm:ss} UTC | O:{Open:F2} | H:{High:F2} | L:{Low:F2} | C:{Close:F2} | V:{Volume:F4} BTC";
    }

    // Интерфейс сервиса рыночных данных
    public interface IMarketDataService : IAsyncDisposable
    {
        event Action<Candle> OnCandleUpdate; // Событие обновления свечи
        event Action<decimal> OnPriceUpdate; // Событие обновления цены
        Task InitializeAsync();              // Инициализация сервиса
        Task<IReadOnlyList<Candle>>
        GetHistoricalDataAsync(int limit = 500); // Получение исторических данных
    }

    // Реализация сервиса рыночных данных для OKX
    public sealed class OKXMarketDataService : IMarketDataService
    {
        private const string WebSocketUri =
            "wss://ws.okx.com:8443/ws/v5/public";
        private const int PingIntervalSec = 25;     // Интервал ping-сообщений
        private const int ReconnectDelayMs = 5500;  // Задержка переподключения
        private const int MaxRetries = 5;           // Максимальное количество попыток

        private readonly string _instrumentId;       // Инструмент (напр. BTC-USDT)
        private readonly string _timeframe;          // Таймфрейм (напр. 1m)
        private readonly string _apiKey;
        private readonly string _apiSecret;
        private readonly string _passphrase;
        private readonly HttpClient _httpClient;     // HTTP-клиент для REST API
        private readonly ArrayPool<byte> _arrayPool; // Пул массивов для оптимизации

        private ClientWebSocket? _webSocket;
        private CancellationTokenSource? _cts;
        private Timer? _pingTimer; // Таймер для ping-сообщений
        private int _retryCount;   // Счетчик попыток переподключения
        private bool _disposed;    // Флаг disposed

        // События интерфейса
        public event Action<Candle>? OnCandleUpdate;
        public event Action<decimal>? OnPriceUpdate;

        // Конструктор
        public OKXMarketDataService(string instrumentId = "SOL-USDT",
                            string timeframe = "30m",
                            string apiKey = "",
                            string apiSecret = "",
                            string passphrase = "")
    {
        _instrumentId = instrumentId;
        _timeframe = timeframe;
        _apiKey = apiKey;
        _apiSecret = apiSecret;
        _passphrase = passphrase;
        _httpClient = new HttpClient { BaseAddress = new Uri("https://www.okx.com") };
        _arrayPool = ArrayPool<byte>.Shared;
        _cts = new CancellationTokenSource();
        _pingTimer = new Timer(_ => SendPingAsync().ConfigureAwait(false),
                            null, Timeout.Infinite, Timeout.Infinite);
    }

        public async Task InitializeAsync()
        {
            if (_webSocket != null && _webSocket.State == WebSocketState.Open)
                return;

            if (_cts == null)
            {
                _cts = new CancellationTokenSource();
            }

            await ConnectWebSocketAsync();
        }

        private async Task ConnectWebSocketAsync()
        {
            try
            {
                _webSocket?.Dispose();
                _webSocket = new ClientWebSocket();

                if (_cts == null)
                {
                    _cts = new CancellationTokenSource();
                }

                await _webSocket.ConnectAsync(new Uri(WebSocketUri), _cts.Token);

                _pingTimer?.Change(TimeSpan.FromSeconds(PingIntervalSec),
                                   TimeSpan.FromSeconds(PingIntervalSec));

                await SubscribeToChannelsAsync();

                _ = Task.Run(ProcessWebSocketMessages, _cts.Token);

                Console.WriteLine(
                    $"✅ WebSocket подключен к OKX ({_instrumentId}, {_timeframe})");
                _retryCount = 0;
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка подключения WebSocket: {ex.Message}");
                await HandleReconnectionAsync();
            }
        }

        // Подписка на каналы данных
        private async Task SubscribeToChannelsAsync()
        {
            await AuthenticateWebSocketAsync();
            var subscribeMessage = new
            {
                op = "subscribe",
                args = new[]
                { 
                    new { channel = "tickers", instId = _instrumentId },
                }
            };

            var message = JsonConvert.SerializeObject(subscribeMessage);
            Console.WriteLine($"WS Sub: {message}"); // Логируем сообщение о подписке
            await SendWebSocketMessageAsync(message);
        }

        // Обработка входящих сообщений WebSocket
        private async Task ProcessWebSocketMessages()
        {
            var buffer = _arrayPool.Rent(8192);
            try
            {
                while (_webSocket?.State == WebSocketState.Open &&
                       (_cts?.IsCancellationRequested ?? false) == false)
                {
                    var result = await _webSocket.ReceiveAsync(
                        new ArraySegment<byte>(buffer),
                        _cts?.Token ?? CancellationToken.None);

                    if (result.MessageType == WebSocketMessageType.Close)
                    {
                        Console.WriteLine("⚠️ Соединение закрыто сервером");
                        await HandleReconnectionAsync();
                        break;
                    }

                    var message = Encoding.UTF8.GetString(buffer, 0, result.Count);
                    ProcessMessage(message);
                }
            }
            catch (OperationCanceledException)
            {
                // Нормальное завершение при отмене
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка обработки сообщения: {ex.Message}");
                await HandleReconnectionAsync();
            }
            finally
            {
                _arrayPool.Return(buffer);
            }
        }

        // Разбор и обработка сообщения
        private void ProcessMessage(string message)
        {
            try
            {
                var json = JObject.Parse(message);

                if (json["event"]?.ToString() == "error")
                {
                    Console.WriteLine($"⚠️ WebSocket error: {json["msg"]}");
                    return;
                }

                if (json["arg"]?["channel"]?.ToString() == "tickers" && json["data"] != null)
                {
                    var tickerData = json["data"]?.First?.ToObject<JObject>();
                    if (tickerData != null)
                    {
                        var lastPrice = tickerData["last"]?.Value<decimal>();
                        if (lastPrice.HasValue)
                        {
                            OnPriceUpdate?.Invoke(lastPrice.Value);
                        }
                    }
                }
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка парсинга сообщения: {ex.Message}");
            }
        }

        // Отправка ping-сообщения
        private async Task SendPingAsync()
        {
            try
            {
                if (_webSocket?.State == WebSocketState.Open)
                {
                    //await SendWebSocketMessageAsync("{\"op\":\"ping\"}");
                }
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка отправки ping: {ex.Message}");
            }
        }

        // Отправка сообщения через WebSocket
        private async Task SendWebSocketMessageAsync(string message)
        {
            if (_webSocket == null || _cts == null)
            {
                Console.WriteLine(
                    "WebSocket или CancellationTokenSource не инициализированы.");
                return;
            }

            try
            {
                var bytes = Encoding.UTF8.GetBytes(message);
                await _webSocket.SendAsync(new ArraySegment<byte>(bytes),
                                           WebSocketMessageType.Text, true, _cts.Token);
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка отправки сообщения: {ex.Message}");
                await HandleReconnectionAsync();
            }
        }

        // Обработка переподключения
        private async Task HandleReconnectionAsync()
        {
            if (_retryCount >= MaxRetries)
            {
                Console.WriteLine(
                    "❌ Достигнуто максимальное количество попыток переподключения");
                return;
            }

            _retryCount++;
            Console.WriteLine(
                $"⏳ Попытка переподключения {_retryCount}/{MaxRetries}...");

            // Экспоненциальная задержка перед повторной попыткой
            await Task.Delay(ReconnectDelayMs * _retryCount, _cts.Token);
            await ConnectWebSocketAsync();
        }

        // Получение исторических данных
        public async Task<IReadOnlyList<Candle>> GetHistoricalDataAsync(int limit = 500)
        {
            try
            {
                var response = await _httpClient.GetAsync(
                    $"/api/v5/market/candles?instId={_instrumentId}&bar={_timeframe}&limit={limit}");

                response.EnsureSuccessStatusCode();

                var content = await response.Content.ReadAsStringAsync();
                Console.WriteLine($"API Response: {content}");

                var data = JsonConvert.DeserializeObject<JObject>(content)?["data"]
                        ?.ToObject<List<List<string>>>();

                return data?.Select(Candle.FromRaw)
                        .OrderBy(c => c.Timestamp)
                        .ToList()
                        .AsReadOnly() ??
                    new List<Candle>().AsReadOnly();
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка получения исторических данных: {ex.Message}");
                return new List<Candle>().AsReadOnly();
            }
        }
        // Освобождение ресурсов
        public async ValueTask DisposeAsync()
        {
            if (_disposed)
                return;
            _disposed = true;

            // Отмена операций
            _cts?.Cancel();
            _pingTimer?.Dispose();

            // Закрытие WebSocket соединения
            if (_webSocket?.State == WebSocketState.Open)
            {
                try
                {
                    await _webSocket.CloseAsync(WebSocketCloseStatus.NormalClosure,
                                                "Shutting down", CancellationToken.None);
                }
                catch
                {
                    // Игнорируем ошибки при закрытии
                }
            }

            // Освобождение ресурсов
            _webSocket?.Dispose();
            _httpClient.Dispose();
            _cts?.Dispose();
        }
                private async Task AuthenticateWebSocketAsync()
        {
            var timestamp = DateTimeOffset.UtcNow.ToUnixTimeSeconds().ToString();
            const string method = "GET";
            const string path = "/users/self/verify";
            var signature = GenerateSignature(timestamp, method, path, "");

            var authMsg = new
            {
                op = "login",
                args = new[]
                {
                    new
                    {
                        apiKey = _apiKey,
                        passphrase = _passphrase,
                        timestamp = timestamp,
                        sign = signature
                    }
                }
            };

            var json = JsonConvert.SerializeObject(authMsg);
            await SendWebSocketMessageAsync(json);
        }

        private string GenerateSignature(string timestamp, string method,
                                        string endpoint, string body)
        {
            var prehash = timestamp + method.ToUpper() + endpoint + body;
            var secretBytes = Encoding.UTF8.GetBytes(_apiSecret);
            var prehashBytes = Encoding.UTF8.GetBytes(prehash);

            using var hmac = new HMACSHA256(secretBytes);
            return Convert.ToBase64String(hmac.ComputeHash(prehashBytes));
        }
    }

    // Интерфейс REST клиента для OKX
    public interface IOKXRestClient : IDisposable
    {
        // Размещение ордера
        Task<OrderResponse> PlaceOrderAsync(OrderRequest order,
                                            CancellationToken ct = default);

        // Отмена ордера
        Task<bool> CancelOrderAsync(string orderId, string instId,
                                    CancellationToken ct = default);

        // Получение информации об ордере
        Task<OrderInfo> GetOrderInfoAsync(string orderId, string instId,
                                          CancellationToken ct = default);

        // Получение открытых позиций
        Task<PositionInfo[]> GetPositionsAsync(string instType = "SWAP",
                                               CancellationToken ct = default);

        // Получение баланса
        Task<BalanceInfo> GetBalanceAsync(string ccy = "USDT",
                                          CancellationToken ct = default);

        // Получение данных тикера
        Task<TickerInfo> GetTickerAsync(string instId,
                                        CancellationToken ct = default);

        // Размещение алгоритмического ордера (стоп-лосс/тейк-профит)
        Task<OrderResponse> PlaceAlgoOrderAsync(AlgoOrderRequest order,
                                                CancellationToken ct = default);

        // Отмена алгоритмического ордера
        Task<bool> CancelAlgoOrderAsync(string algoId, string instId,
                                        CancellationToken ct = default);

        Task<OrderResponse> PlaceTrailingStopAsync(string instId,
                                               OrderSide closeSide,
                                               decimal size,
                                               decimal activationPx,
                                               CancellationToken ct = default);
    }

    // Реализация REST клиента для OKX
    public sealed class OKXRestClient : IOKXRestClient
    {
        private const string ApiBaseUrl = "https://www.okx.com"; // Базовый URL API
        private const string ApiVersion = "v5";                  // Версия API
        private static readonly TimeSpan Timeout =
            TimeSpan.FromSeconds(5); // Таймаут запросов

        // Настройки JSON сериализации
        private static readonly JsonSerializerOptions JsonOptions = new()
        {
            PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
            NumberHandling =
              System.Text.Json.Serialization.JsonNumberHandling.AllowReadingFromString
        };

        private readonly string _apiKey;         // API ключ
        private readonly string _apiSecret;      // Секрет API
        private readonly string _passphrase;     // Парольная фраза
        private readonly HttpClient _httpClient; // HTTP клиент
        private readonly ConcurrentDictionary<string, RateLimitInfo> _rateLimits =
            new(); // Лимиты запросов
        private readonly ArrayPool<byte> _arrayPool =
            ArrayPool<byte>.Shared;   
            private readonly Timer _rateLimitCleanupTimer;                // Пул массивов
         // Таймер очистки лимитов
        private bool _disposed;                        // Флаг disposed

        // Конструктор 1 метод
        public OKXRestClient(string apiKey, string apiSecret, string passphrase)
        {
            _apiKey = apiKey ?? throw new ArgumentNullException(nameof(apiKey));
            _apiSecret =
                apiSecret ?? throw new ArgumentNullException(nameof(apiSecret));
            _passphrase =
                passphrase ?? throw new ArgumentNullException(nameof(passphrase));

            // Настройка HTTP клиента
            _httpClient = new HttpClient(new HttpClientHandler
            {
                AutomaticDecompression =
                  DecompressionMethods.GZip | DecompressionMethods.Deflate,
                MaxConnectionsPerServer = 20
            })
            { BaseAddress = new Uri(ApiBaseUrl), Timeout = Timeout };

            // Таймер для периодической очистки устаревших лимитов
            _rateLimitCleanupTimer = new Timer(
                _ => CleanupRateLimits(), null, TimeSpan.FromMinutes(5),
                TimeSpan.FromMinutes(5));
        }

        // Размещение ордера 2 метод
        public async Task<OrderResponse>
        PlaceOrderAsync(OrderRequest order, CancellationToken ct = default)
        {
            if (order == null)
                throw new ArgumentNullException(nameof(order));

            var endpoint = $"/api/{ApiVersion}/trade/order";
            var response = await SendRequestAsync<OrderResponse>(
                endpoint, HttpMethod.Post, order, ct);
            return response;
        }

        // Отмена ордера 3 метод
        public async Task<bool> CancelOrderAsync(string orderId, string instId,
                                                 CancellationToken ct = default)
        {
            if (string.IsNullOrEmpty(orderId))
                throw new ArgumentNullException(nameof(orderId));
            if (string.IsNullOrEmpty(instId))
                throw new ArgumentNullException(nameof(instId));

            var endpoint = $"/api/{ApiVersion}/trade/cancel-order";
            var request = new { ordId = orderId, instId };
            var response = await SendRequestAsync<OrderResponse>(
                endpoint, HttpMethod.Post, request, ct);
            return response.Code == "0";
        }

        // Получение информации об ордере 3 метод
        public async Task<OrderInfo>
        GetOrderInfoAsync(string orderId, string instId,
                          CancellationToken ct = default)
        {
            if (string.IsNullOrEmpty(orderId))
                throw new ArgumentNullException(nameof(orderId));
            if (string.IsNullOrEmpty(instId))
                throw new ArgumentNullException(nameof(instId));

            var endpoint = $"/api/{ApiVersion}/trade/order";
            var query = $"?ordId={orderId}&instId={instId}";

            try
            {
                var response = await SendRequestAsync<OrderInfoResponse>(
                    endpoint + query, HttpMethod.Get, null, ct);

                if (response.Data == null || !response.Data.Any())
                    throw new OKXApiException("404", "Order not found");

                return response.Data.First();
            }
            catch (OKXApiException ex) when (ex.ErrorCode == "51601")
            {
                // Ордер не найден
                return new OrderInfo(OrdId: orderId, InstId: instId, Side: OrderSide.Buy,
                                     Price: 0, Size: 0, Status: OrderStatus.Canceled,
                                     CreateTime: DateTime.UtcNow);
            }
        }

        // Получение открытых позиций 4 метод
        public async Task<PositionInfo[]>
        GetPositionsAsync(string instType = "SWAP", CancellationToken ct = default)
        {
            var endpoint = $"/api/{ApiVersion}/account/positions";
            var query = $"?instType={instType}";
            var response = await SendRequestAsync<PositionInfoResponse>(
                endpoint + query, HttpMethod.Get, null, ct);
            return response.Data ?? Array.Empty<PositionInfo>();
        }

        // Доработанный метод получения баланса
        public async Task<BalanceInfo>
        GetBalanceAsync(string ccy = "USDT", CancellationToken ct = default)
        {
            var endpoint = $"/api/{ApiVersion}/account/balance";
            var query = string.IsNullOrEmpty(ccy) ? "" : $"?ccy={ccy}";

            try
            {
                var response = await SendRequestAsync<BalanceInfoResponse>(
                    endpoint + query, HttpMethod.Get, null, ct);

                return response.Data?.FirstOrDefault() ??
                       new BalanceInfo(Ccy: ccy, Equity: 0, AvailBal: 0);
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка получения баланса: {ex.Message}");
                throw;
            }
        }

        // Получение данных тикера
        public async Task<TickerInfo> GetTickerAsync(string instId,
                                                     CancellationToken ct = default)
        {
            if (string.IsNullOrEmpty(instId))
                throw new ArgumentNullException(nameof(instId));

            var endpoint = $"/api/{ApiVersion}/market/ticker";
            var query = $"?instId={instId}";
            var response = await SendRequestAsync<TickerInfoResponse>(
                endpoint + query, HttpMethod.Get, null, ct);
            return response.Data?.FirstOrDefault();
        }

        // Размещение алгоритмического ордера
        public async Task<OrderResponse>
        PlaceAlgoOrderAsync(AlgoOrderRequest order, CancellationToken ct = default)
        {
            if (order == null)
                throw new ArgumentNullException(nameof(order));

            var endpoint = $"/api/{ApiVersion}/trade/order-algo";
            var response = await SendRequestAsync<OrderResponse>(
                endpoint, HttpMethod.Post, order, ct);
            return response;
        }

        // Отмена алгоритмического ордера
        public async Task<bool> CancelAlgoOrderAsync(string algoId, string instId,
                                                     CancellationToken ct = default)
        {
            if (string.IsNullOrEmpty(algoId))
                throw new ArgumentNullException(nameof(algoId));
            if (string.IsNullOrEmpty(instId))
                throw new ArgumentNullException(nameof(instId));

            var endpoint = $"/api/{ApiVersion}/trade/cancel-algos";
            var request = new { algoId, instId };
            var response = await SendRequestAsync<OrderResponse>(
                endpoint, HttpMethod.Post, request, ct);
            return response.Code == "0";
        }

        public async Task<OrderResponse>
         PlaceTrailingStopAsync(
            string instId,
            OrderSide closeSide,
            decimal size,
            decimal activationPx,
            CancellationToken ct = default)
        {
            if (string.IsNullOrEmpty(instId))
                throw new ArgumentNullException(nameof(instId));

            var body = new
            {
                instId,
                tdMode   = "isolated",
                side     = closeSide.ToString().ToLower(), // buy / sell
                ordType  = "move_order_stop",
                sz       = size.ToString(CultureInfo.InvariantCulture),
                triggerPx = activationPx.ToString(CultureInfo.InvariantCulture),
                orderPx  = "-1"   // рыночное исполнение
            };

            return await SendRequestAsync<OrderResponse>(
                $"/api/{ApiVersion}/trade/order-algo",
                HttpMethod.Post,
                body,
                ct);
        }

        // Отправка запроса к API
        private async Task<T> SendRequestAsync<T>(string endpoint, HttpMethod method,
                                                  object body, CancellationToken ct)
        {
            // Ожидаем доступного лимита запросов
            await WaitForRateLimitAsync(endpoint, ct);

            // Генерируем подпись запроса
            var timestamp = GetIsoTimestamp();
            var requestBody =
                body != null ? JsonConvert.SerializeObject(body) : string.Empty;
            var signature =
                GenerateSignature(timestamp, method.Method, endpoint, requestBody);

            // Формируем HTTP запрос
            using var request = new HttpRequestMessage(method, endpoint);
            request.Headers.Add("OK-ACCESS-KEY", _apiKey);
            request.Headers.Add("OK-ACCESS-SIGN", signature);
            request.Headers.Add("OK-ACCESS-TIMESTAMP", timestamp);
            request.Headers.Add("OK-ACCESS-PASSPHRASE", _passphrase);
            request.Headers.Accept.Add(
                new MediaTypeWithQualityHeaderValue("application/json"));

            // Добавляем тело запроса если есть
            if (body != null)
            {
                request.Content =
                    new StringContent(requestBody, Encoding.UTF8, "application/json");
            }

            // Отправляем запрос
            using var response = await _httpClient.SendAsync(
                request, HttpCompletionOption.ResponseHeadersRead, ct);
            await UpdateRateLimits(endpoint, response); // Обновляем лимиты

            // Обрабатываем ответ
            var responseBuffer = _arrayPool.Rent(8192);
            try
            {
                using var stream = await response.Content.ReadAsStreamAsync(ct);
                var bytesRead = await stream.ReadAsync(responseBuffer, ct);
                var responseJson = Encoding.UTF8.GetString(responseBuffer, 0, bytesRead);

                // Обработка ошибок
                if (!response.IsSuccessStatusCode)
                {
                    var error =
                        JsonConvert.DeserializeObject<ApiErrorResponse>(responseJson);
                    throw new OKXApiException(error?.Code ?? "0",
                                              error?.Message ?? "Unknown error");
                }

                // Десериализация успешного ответа
                var result = JsonConvert.DeserializeObject<T>(responseJson);
                return result ?? throw new InvalidOperationException(
                                     "Failed to deserialize response");
            }
            finally
            {
                _arrayPool.Return(responseBuffer);
            }
        }

        // Генерация подписи запроса
        private string GenerateSignature(string timestamp, string method,
                                         string endpoint, string body)
        {
            var prehash = timestamp + method + endpoint + body;
            var secretBytes = Encoding.UTF8.GetBytes(_apiSecret);
            var prehashBytes = Encoding.UTF8.GetBytes(prehash);

            using var hmac = new HMACSHA256(secretBytes);
            var hashBytes = hmac.ComputeHash(prehashBytes);
            return Convert.ToBase64String(hashBytes);
        }

        // Получение текущего времени в формате ISO
        private static string GetIsoTimestamp()
        {
            return DateTime.UtcNow.ToString("yyyy-MM-ddTHH:mm:ss.fffZ",
                                            CultureInfo.InvariantCulture);
        }

        // Ожидание доступного лимита запросов
        private async Task WaitForRateLimitAsync(string endpoint,
                                                 CancellationToken ct)
        {
            var endpointKey = GetEndpointKey(endpoint);
            while (_rateLimits.TryGetValue(endpointKey, out var rateLimit) &&
                   rateLimit.Remaining <= 0)
            {
                var delay = rateLimit.ResetTime - DateTime.UtcNow;
                if (delay > TimeSpan.Zero)
                {
                    await Task.Delay(delay, ct);
                }
                break;
            }
        }

        // Обновление информации о лимитах из заголовков ответа
        private async Task UpdateRateLimits(string endpoint,
                                            HttpResponseMessage response)
        {
            if (!response.Headers.TryGetValues("x-ratelimit-limit",
                                               out var limitValues) ||
                !response.Headers.TryGetValues("x-ratelimit-remaining",
                                               out var remainingValues) ||
                !response.Headers.TryGetValues("x-ratelimit-reset",
                                               out var resetValues))
            {
                return; // нет заголовков — ничего не делаем
            }

            var endpointKey = GetEndpointKey(endpoint);

            var limit = int.Parse(limitValues?.FirstOrDefault() ?? "0",
                                  CultureInfo.InvariantCulture);
            var remaining = int.Parse(remainingValues?.FirstOrDefault() ?? "0",
                                      CultureInfo.InvariantCulture);
            var resetSeconds = int.Parse(resetValues?.FirstOrDefault() ?? "0",
                                         CultureInfo.InvariantCulture);

            var resetTime = DateTime.UtcNow.AddSeconds(resetSeconds);

            _rateLimits.AddOrUpdate(endpointKey,
                                    _ => new RateLimitInfo(limit, remaining, resetTime),
                                    (_, existing) =>
                                        new RateLimitInfo(limit, remaining, resetTime));
        }

        // Очистка устаревших лимитов
        private void CleanupRateLimits()
        {
            var now = DateTime.UtcNow;
            foreach (var kvp in _rateLimits)
            {
                if (kvp.Value.ResetTime < now)
                {
                    _rateLimits.TryRemove(kvp.Key, out _);
                }
            }
        }

        // Получение ключа endpoint без query параметров
        private static string GetEndpointKey(string endpoint)
        {
            var index = endpoint.IndexOf('?');
            return index >= 0 ? endpoint[..index] : endpoint;
        }

        // Информация о лимитах запросов
        private record RateLimitInfo(int Limit, int Remaining, DateTime ResetTime);

        public void Dispose()
        {
            if (_disposed)
                return;

            _disposed = true;
            _httpClient?.Dispose();
            GC.SuppressFinalize(this);
        }
    }
    // Интерфейс торговой стратегии
    public interface ITradingStrategy
    {
        event Func<TradeSignal, Task> OnTradeSignal; // Событие генерации сигнала
        Task InitializeAsync();                  // Инициализация стратегии
        void Update(Candle candle);              // Обновление данных стратегии
        IReadOnlyList<Position> GetPositions();  // Получение текущих позиций

        Task GenerateSignalAsync(OrderSide side, Candle candle,
                         decimal atr, decimal vwap, string strategyName,
                         CancellationToken ct = default);
    }

    // Продвинутая торговая стратегия
    public class AdvancedTradingStrategy : ITradingStrategy
    {
        public event Func<TradeSignal, Task> OnTradeSignal = _ => Task.CompletedTask;

        private readonly IMarketDataService _marketDataService;
        private readonly IOKXRestClient _restClient;

        private readonly CircularBuffer<Candle> _candleBuffer;
        private readonly CircularBuffer<decimal> _priceBuffer;

        private readonly ConcurrentDictionary<string, Position> _activePositions =
            new();
        private readonly ConcurrentQueue<Position> _closedPositions = new();

        private readonly int _rsiLength = 2;
        private readonly int _atrLength = 14;
        private readonly decimal _minVolume = 500m;
        private readonly decimal _riskPercent = 10.0m;
        private readonly decimal _rewardRatio = 1.0m;
        private readonly decimal _atrMultiplier = 1.5m;
        private readonly decimal _trailingStopPercent = 1.5m;
        private readonly decimal _maxPositionSize = 100000m;
        private readonly TimeSpan _positionTimeout = TimeSpan.FromHours(4);
        private readonly int _rsiOverbought = 72;
        private readonly int _rsiOversold = 28;
        private readonly int _cooldownBars = 1;

        private readonly decimal _trailingPercent;
        private decimal _currentPrice;
        private int? _lastSignalBarIndex = null;
        private bool _isProcessingSignal = false;
        private readonly bool _useTrailing;

        public AdvancedTradingStrategy(
            IMarketDataService marketDataService,
            IOKXRestClient restClient,
            IConfiguration cfg,
            int bufferSize = 500)
        {
            _marketDataService = marketDataService;
            _restClient = restClient;
            _candleBuffer = new CircularBuffer<Candle>(bufferSize);
            _priceBuffer = new CircularBuffer<decimal>(bufferSize);

            _useTrailing     = cfg.GetValue<bool>  ("Strategy:UseTrailingStop");
            _trailingPercent = cfg.GetValue<decimal>("Strategy:TrailingPercent");
        }

        public void UpdateSlAlgoId(string id, string newAlgoId, decimal newSL)
        {
            if (_activePositions.TryGetValue(id, out var pos))
                _activePositions[id] = pos with
                {
                    StopLoss = newSL,
                    StopLossAlgoId = newAlgoId
                };
        }

        public async Task InitializeAsync()
        {
            Console.WriteLine("Initializing market data service...");
            var historicalData = await _marketDataService.GetHistoricalDataAsync();
            foreach (var candle in historicalData)
                _candleBuffer.PushBack(candle);
            Console.WriteLine($"📊 Загружено {historicalData.Count} исторических свечей");
        }

        public void Update(Candle candle)
        {
            try
            {
                _currentPrice = candle.Close;
                _candleBuffer.PushBack(candle);

                Console.WriteLine($"[DEBUG-STRAT] {candle.Timestamp:HH:mm:ss} | " +
                          $"O={candle.Open:F2} H={candle.High:F2} L={candle.Low:F2} " +
                          $"C={candle.Close:F2} V={candle.QuoteVolume:F2}");

                var prevCandle = _candleBuffer.Count > 1
                                     ? _candleBuffer[_candleBuffer.Count - 2]
                                     : null;
                if (prevCandle == null)
                    return;

                var candleList = _candleBuffer.ToList()
                                     .ToList(); // <-- явное преобразование в List<Candle>
                var vwap = CalculateDailyVwap(candleList);
                var closes = candleList.Select(c => c.Close).ToArray();
                var highs = candleList.Select(c => c.High).ToArray();
                var lows = candleList.Select(c => c.Low).ToArray();

                var atr = CalculateATR(highs, lows, closes, _atrLength);
                var rsi = CalculateRSI(closes, _rsiLength);
                var currentRsi = rsi.Length > 0 ? rsi[^1] : 50m;
                var prevRsi = rsi.Length > 1 ? rsi[^2] : currentRsi;

                bool rsiCrossover = prevRsi < _rsiOversold && currentRsi >= _rsiOversold;
                bool rsiCrossunder =
                    prevRsi > _rsiOverbought && currentRsi <= _rsiOverbought;

                bool bullishRejection =
                    candle.Close > prevCandle.Close && candle.Close < vwap;
                bool bearishRejection =
                    candle.Close < prevCandle.Close && candle.Close > vwap;

                bool canTrade = (!_lastSignalBarIndex.HasValue || (_candleBuffer.Count - _lastSignalBarIndex > _cooldownBars)) &&
                candle.Volume > _minVolume &&  // BTC-объём
                !HasActivePosition();

                if (canTrade && rsiCrossover && bullishRejection)
                {
                    _ = GenerateSignalAsync(OrderSide.Buy, candle, atr, vwap, "RSI+VWAP Rejection");
                }
                else if (canTrade && rsiCrossunder && bearishRejection)
                {
                    _ = GenerateSignalAsync(OrderSide.Sell, candle, atr, vwap, "RSI+VWAP Rejection");
                }

                CheckStopConditions(candle.Close);
                UpdateTrailingStops(candle.Close);
                CheckPositionTimeouts();
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка в стратегии: {ex.Message}");
            }
        }

        public IReadOnlyList<Position> GetPositions() =>
            _activePositions.Values.Concat(_closedPositions).ToList().AsReadOnly();

        private bool HasActivePosition() => _activePositions.Count > 0;

        private decimal[] CalculateRSI(decimal[] closes, int period)
        {
            if (closes.Length < period || period <= 0)
                return Array.Empty<decimal>();

            var rsi = new decimal[closes.Length];
            decimal avgGain = 0, avgLoss = 0;

            for (int i = 1; i <= period; i++)
            {
                var diff = closes[i] - closes[i - 1];
                if (diff > 0)
                    avgGain += diff;
                else
                    avgLoss -= diff;
            }

            avgGain /= period;
            avgLoss /= period;
            rsi[period] = avgLoss == 0 ? 100 : 100 - (100 / (1 + avgGain / avgLoss));

            for (int i = period + 1; i < closes.Length; i++)
            {
                var diff = closes[i] - closes[i - 1];
                decimal gain = diff > 0 ? diff : 0;
                decimal loss = diff < 0 ? -diff : 0;

                avgGain = (avgGain * (period - 1) + gain) / period;
                avgLoss = (avgLoss * (period - 1) + loss) / period;

                rsi[i] = avgLoss == 0 ? 100 : 100 - (100 / (1 + avgGain / avgLoss));
            }

            return rsi;
        }

        private decimal CalculateATR(decimal[] highs, decimal[] lows,
                                     decimal[] closes, int period)
        {
            var trueRanges = new decimal[highs.Length];
            trueRanges[0] = highs[0] - lows[0];

            for (int i = 1; i < highs.Length; i++)
            {
                decimal hl = highs[i] - lows[i];
                decimal hc = Math.Abs(highs[i] - closes[i - 1]);
                decimal lc = Math.Abs(lows[i] - closes[i - 1]);
                trueRanges[i] = Math.Max(hl, Math.Max(hc, lc));
            }

            decimal atr = 0;
            for (int i = 0; i < period; i++)
                atr += trueRanges[i];
            return atr / period;
        }

            private decimal CalculateDailyVwap(IReadOnlyList<Candle> candles)
            {
                var todayStart = DateTime.UtcNow.Date;
                var todayCandles = candles.Where(c => c.Timestamp >= todayStart).ToList();
                if (!todayCandles.Any()) return 0;

                decimal sumPV = 0, sumV = 0;
                foreach (var c in todayCandles)
                {
                    decimal price = (c.High + c.Low + c.Close) / 3m;
                    sumPV += price * c.Volume;
                    sumV  += c.Volume;
                }
                return sumV == 0 ? 0 : sumPV / sumV;
}
    
        private void CheckStopConditions(decimal currentPrice)
        {
            foreach (var id in _activePositions.Keys.ToList())
            {
                if (!_activePositions.TryGetValue(id, out var pos))
                    continue;

                if ((pos.Side == OrderSide.Buy && currentPrice <= pos.StopLoss) ||
                    (pos.Side == OrderSide.Sell && currentPrice >= pos.StopLoss))
                    ClosePosition(id, pos.StopLoss, "Stop Loss");

                else if ((pos.Side == OrderSide.Buy && currentPrice >= pos.TakeProfit) ||
                         (pos.Side == OrderSide.Sell && currentPrice <= pos.TakeProfit))
                    ClosePosition(id, pos.TakeProfit, "Take Profit");
            }
        }

        public async Task GenerateSignalAsync(OrderSide side,
                                      Candle candle,
                                      decimal atr,
                                      decimal vwap,
                                      string strategyName,
                                      CancellationToken ct = default)
        {
            if (_isProcessingSignal) return;
            _isProcessingSignal = true;

            try
            {
                // 0. Проверяем, что позиции ещё нет
                var openPos = await _restClient.GetPositionsAsync("SWAP", ct);
                if (openPos.Any(p => p.InstId == "SOL-USDT"))
                {
                    Console.WriteLine("⚠️ Позиция уже открыта – пропускаем сигнал.");
                    return;
                }

                // 1. Рассчитываем риск и размер
                var balance = await _restClient.GetBalanceAsync("USDT", ct);
                if (balance?.AvailBal < 10m)
                {
                    Console.WriteLine($"⚠️ Недостаточно USDT ({balance.AvailBal:F2})");
                    return;
                }

                var entryPrice = candle.Close;
                var slDistance = atr * _atrMultiplier;
                var riskUsd = balance.AvailBal * (_riskPercent / 100m);
                var size = Math.Round(riskUsd / slDistance, 3); // 3 знака для SOL-USDT

                if (size <= 0) return;

                // 2. Рыночный ордер
                var marketResp = await _restClient.PlaceOrderAsync(
                    new OrderRequest("SOL-USDT", side, "market", size), ct);

                if (marketResp.Code != "0")
                    throw new OKXApiException(marketResp.Code, marketResp.Message);

                // 3. Ожидаем исполнения
                OrderInfo orderInfo;
                do
                {
                    await Task.Delay(500, ct);
                    orderInfo = await _restClient.GetOrderInfoAsync(marketResp.OrderId, "SOL-USDT", ct);
                } while (orderInfo.Status != OrderStatus.Filled);

                var filledPrice = orderInfo.Price; // реальная средняя цена входа
                var filledSize  = orderInfo.Size;

                Console.WriteLine($"✅ Вход {side} {filledSize} @ {filledPrice}");

                // 4. Выставляем выход
                if (_useTrailing)
                {
                    // трейлинг-стоп
                    var triggerPx = side == OrderSide.Buy
                        ? filledPrice - slDistance
                        : filledPrice + slDistance;

                    var trailResp = await _restClient.PlaceTrailingStopAsync(
                        "SOL-USDT",
                        side == OrderSide.Buy ? OrderSide.Sell : OrderSide.Buy,
                        filledSize,
                        triggerPx,
                        ct);

                    if (trailResp.Code != "0")
                        Console.WriteLine($"⚠️ Трейлинг не выставлен: {trailResp.Message}");
                    else
                        Console.WriteLine($"✅ Трейлинг-стоп выставлен на {triggerPx}");
                }
                else
                {
                    // классический SL/TP
                    var slPrice = side == OrderSide.Buy
                        ? filledPrice - slDistance
                        : filledPrice + slDistance;

                    var tpPrice = side == OrderSide.Buy
                        ? filledPrice + slDistance * _rewardRatio
                        : filledPrice - slDistance * _rewardRatio;

                    var bracketResp = await _restClient.PlaceAlgoOrderAsync(
                        new AlgoOrderRequest("SOL-USDT",
                                            side == OrderSide.Buy ? OrderSide.Sell : OrderSide.Buy,
                                            filledSize,
                                            slPrice,
                                            tpPrice),
                        ct);

                    if (bracketResp.Code != "0")
                        Console.WriteLine($"⚠️ SL/TP не выставлены: {bracketResp.Message}");
                    else
                        Console.WriteLine($"✅ SL={slPrice} TP={tpPrice}");
                }

                // 5. Сохраняем позицию в память
                var pos = new Position(
                    Guid.NewGuid().ToString("N"),
                    side,
                    filledPrice,
                    filledSize,
                    0, 0, PositionStatus.Open,
                    DateTime.UtcNow,
                    null, null, null,
                    marketResp.OrderId,
                    "", "", _useTrailing,
                    "", "");
                _activePositions.TryAdd(pos.Id, pos);
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка сигнала: {ex.Message}");
            }
            finally
            {
                _isProcessingSignal = false;
            }
        }

        private async Task<decimal> CalculatePositionSize(decimal entryPrice,
                                                          decimal stopLossPrice)
        {
            try
            {
                var balance = await _restClient.GetBalanceAsync("USDT");
                if (balance?.AvailBal <= 0)
                    return 0;

                var riskAmount = balance.AvailBal * (_riskPercent / 100m);
                var priceDistance = Math.Abs(entryPrice - stopLossPrice);
                if (priceDistance == 0)
                    return 0;

                var size = riskAmount / priceDistance;
                var maxSize = _maxPositionSize / entryPrice;
                return Math.Clamp(size, 0.001m, maxSize);
            }
            catch
            {
                return 0;
            }
        }

        private void UpdateTrailingStops(decimal currentPrice)
        {
            foreach (var id in _activePositions.Keys.ToList())
            {
                if (!_activePositions.TryGetValue(id, out var pos) || !pos.IsTrailingStop)
                    continue;

                var newStop = pos.Side == OrderSide.Buy
                                  ? currentPrice * (1 - _trailingStopPercent / 100m)
                                  : currentPrice * (1 + _trailingStopPercent / 100m);

                if ((pos.Side == OrderSide.Buy && newStop > pos.StopLoss) ||
                    (pos.Side == OrderSide.Sell && newStop < pos.StopLoss))
                {
                    _activePositions[id] = pos with { StopLoss = newStop };
                }
            }
        }

        private void CheckPositionTimeouts()
        {
            var now = DateTime.UtcNow;
            foreach (var id in _activePositions.Keys.ToList())
            {
                if (_activePositions.TryGetValue(id, out var pos) &&
                    now - pos.OpenTime > _positionTimeout)
                {
                    ClosePosition(id, _currentPrice, "Timeout");
                }
            }
        }


        // Закрытие позиции
        private void ClosePosition(string positionId, decimal closePrice,
                                   string reason)
        {
            if (!_activePositions.TryRemove(positionId, out var position))
                return;

            // Расчет PnL
            decimal pnl = position.Side == OrderSide.Buy
                              ? (closePrice - position.EntryPrice) * position.Size
                              : (position.EntryPrice - closePrice) * position.Size;

                              Console.WriteLine($"🚪 ВЫХОД {position.Id} | Цена={closePrice:F2} | " +
                                    $"Причина={reason} | PnL={pnl:F2} USDT");

            // Обновление позиции
            var closedPosition = position with
            {
                Status = PositionStatus.Closed,
                CloseTime = DateTime.UtcNow,
                ClosePrice = closePrice,
                PnL = pnl
            };

            _closedPositions.Enqueue(closedPosition);

            Console.WriteLine(
                $"🚪 Закрыта позиция {positionId} по цене {closePrice:F2}. Причина: {reason}. PnL: {pnl:F2} USDT");
        }

        // Класс кольцевого буфера для хранения данных
        public class CircularBuffer<T>
        {
            private readonly T[] _buffer;
            private int _index;
            private int _count;

            public CircularBuffer(int capacity) { _buffer = new T[capacity]; }

            public void PushBack(T item)
            {
                _buffer[_index] = item;
                _index = (_index + 1) % _buffer.Length;
                _count = Math.Min(_count + 1, _buffer.Length);
            }

            public int Count => _count;

            public T this[int index] // 添加索引器
            {
                get
                {
                    if (index < 0 || index >= _count)
                        throw new ArgumentOutOfRangeException(nameof(index));
                    return _buffer[(_index - _count + index + _buffer.Length) %
                                   _buffer.Length];
                }
            }

            public IEnumerable<T> ToList() // 已有的方法
            {
                var result = new List<T>(_count);
                for (int i = 0; i < _count; i++)
                {
                    result.Add(
                        _buffer[(_index - _count + i + _buffer.Length) % _buffer.Length]);
                }
                return result;
            }
        }

        private static string GetEnvVariableOrThrow(string name)
        {
            return Environment.GetEnvironmentVariable(name) ??
                   throw new ArgumentNullException(
                       $"{name} environment variable not set");
        }
    }

    // ✅ Теперь отдельный класс
    public class TradingBot : IAsyncDisposable
    {
        private readonly CancellationTokenSource _cts = new();
        private readonly IMarketDataService _marketData;
        private readonly IOKXRestClient _restClient;
        private readonly ITradingStrategy _strategy;
        private decimal _lastPrice;
        private readonly Timer? _trailingTimer;
        private readonly string _instrumentId;
        private Timer? _candleTimer;
        private readonly Timer? _candlePoller;   
        private DateTime _lastCandleTime = DateTime.MinValue;
        public IMarketDataService MarketData => _marketData;
        public ITradingStrategy Strategy => _strategy;

        public TradingBot(string apiKey, string apiSecret, string passphrase, IConfiguration cfg, 
                  string instrumentId = "SOL-USDT")
        {
            _instrumentId = instrumentId;
            _marketData = new OKXMarketDataService(instrumentId, "30m", apiKey, apiSecret, passphrase);
            _restClient = new OKXRestClient(apiKey, apiSecret, passphrase);
            _strategy = new AdvancedTradingStrategy(_marketData, _restClient, cfg);
            

            _strategy.OnTradeSignal += async signal =>
            {
                try { await OnTradeSignalHandler(signal); }
                catch (Exception ex) { Console.WriteLine($"❌ Signal failed: {ex.Message}"); }
            };

            _marketData.OnCandleUpdate += candle => _strategy.Update(candle);

            async void UpdateTrailingStopsOnExchange(object _)
            {
                if (_lastPrice <= 0) return;      // цена ещё не поступила

                foreach (var pos in _strategy.GetPositions().Where(p => p.Status == PositionStatus.Open))
                {
                    var newSL = pos.Side == OrderSide.Buy
                        ? _lastPrice * (1 - 0.015m)
                        : _lastPrice * (1 + 0.015m);

                    if (newSL == pos.StopLoss) continue;

                    try
                    {
                        await _restClient.CancelAlgoOrderAsync(pos.StopLossAlgoId, "BTC-USDT");

                        var resp = await _restClient.PlaceAlgoOrderAsync(
                            new AlgoOrderRequest("BTC-USDT",
                                                pos.Side == OrderSide.Buy ? OrderSide.Sell : OrderSide.Buy,
                                                pos.Size,
                                                newSL,
                                                0m));

                        (_strategy as AdvancedTradingStrategy)?.UpdateSlAlgoId(pos.Id, resp.OrderId, newSL);
                    }
                    catch (Exception ex)
                    {
                        Console.WriteLine($"⚠️ Trailing error: {ex.Message}");
                    }
                }
            }

                       _trailingTimer = new Timer(UpdateTrailingStopsOnExchange, null,
                                TimeSpan.Zero, TimeSpan.FromSeconds(1));

            // 5-сек REST-поллер 30-мин свечей
            // 5-сек REST-поллер 30-мин свечей
        _candlePoller = new Timer(async _ =>
        {
            try
            {
                var candles = await _marketData.GetHistoricalDataAsync(1);
                if (candles.Count == 0) return;

                var candle = candles[^1];
                if (candle.Timestamp == _lastCandleTime) return; // дубль

                _lastCandleTime = candle.Timestamp;
                _strategy.Update(candle);
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Poller error: {ex.Message}");
            }
        }, null, TimeSpan.Zero, TimeSpan.FromSeconds(5));

        } // ЗАКРЫТИЕ КОНСТРУКТОРА
        // ЗАКРЫТИЕ КОНСТРУКТОРА - ДОБАВИТЬ ЭТУ СКОБКУ

        // ПЕРЕМЕСТИТЬ метод PlaceOrder ВНЕ конструктора
        public async Task<bool> PlaceOrder(OrderSide side, decimal size)
        {
            try
            {
                var ticker = await _restClient.GetTickerAsync(_instrumentId);
                var price = ticker?.Last ?? 0;

                if (price <= 0) return false;

                var order = new OrderRequest(_instrumentId, side, "market", size);
                var response = await _restClient.PlaceOrderAsync(order);

                if (response.Code == "0")
                {
                    Console.WriteLine($"✅ Ручной ордер: {side} {size} {_instrumentId}");
                    return true;
                }

                Console.WriteLine($"❌ Ошибка ручного ордера: {response.Message}");
                return false;
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка размещения ордера: {ex.Message}");
                return false;
            }
        }

        private DateTime _nextCandleUtc;

        public async Task StartAsync()
        {
            await _marketData.InitializeAsync();
            await _strategy.InitializeAsync();

            // тикеры для live-цены
            _marketData.OnPriceUpdate += price => _lastPrice = price;

            // планируем первую загрузку на ближайший :00 или :30
            var now = DateTime.UtcNow;
            var nextBoundary = now.Minute < 30
                ? now.Date.AddHours(now.Hour).AddMinutes(30)
                : now.Date.AddHours(now.Hour + 1);

            _nextCandleUtc = nextBoundary;
        }
        private void OnCandleUpdateHandler(Candle candle)
        {
            Console.WriteLine($"Candle received: {candle.Timestamp} | " +
                            $"O:{candle.Open} H:{candle.High} L:{candle.Low} " +
                            $"C:{candle.Close} V:{candle.Volume}");
            _strategy.Update(candle);
        }

        private async Task OnTradeSignalHandler(TradeSignal signal)
        {
            if (signal.Size <= 0) return;

            try
            {
                var balance = await _restClient.GetBalanceAsync("USDT");
                if (balance.AvailBal <= 0)
                {
                    Console.WriteLine("⚠️ Недостаточно средств");
                    return;
                }

                // 1. Размещаем рыночный ордер
                var order = new OrderRequest("BTC-USDT", signal.Side, "market", signal.Size);
                var orderResp = await _restClient.PlaceOrderAsync(order, _cts.Token);
                if (orderResp.Code != "0")
                    throw new OKXApiException(orderResp.Code, orderResp.Message);

                Console.WriteLine($"✅ Вход: {signal.Side} {signal.Size} @ {signal.EntryPrice:F2}");

                // 2. Размещаем алгоритмический ордер с SL + TP + Trailing
                var algoOrder = new AlgoOrderRequest(
                    InstId: "BTC-USDT",
                    Side: signal.Side == OrderSide.Buy ? OrderSide.Sell : OrderSide.Buy, // противоположное направление
                    Size: signal.Size,
                    StopLoss: signal.StopLoss,
                    TakeProfit: signal.TakeProfit
                );

                var algoResp = await _restClient.PlaceAlgoOrderAsync(algoOrder, _cts.Token);
                if (algoResp.Code != "0")
                    Console.WriteLine($"⚠️ Algo-ордер не выставлен: {algoResp.Message}");
                else
                    Console.WriteLine($"✅ SL/TP выставлены: SL={signal.StopLoss}, TP={signal.TakeProfit}");
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Ошибка: {ex.Message}");
            }
        }

        public async ValueTask DisposeAsync()
        {
            _candlePoller?.Dispose();
            _cts.Cancel();
            _trailingTimer?.Dispose();
            await _marketData.DisposeAsync();
            _restClient.Dispose();
            _strategy.OnTradeSignal -= OnTradeSignalHandler;
            Console.WriteLine("🛑 Торговый бот остановлен");
        }
    }
}