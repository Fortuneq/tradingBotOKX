using System;
using System.Threading.Tasks;
using Microsoft.AspNetCore.Builder;
using Microsoft.AspNetCore.Http;
using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Configuration;
using System.Linq;

namespace OKXTraderBot
{
    public class Program
    {
        public static async Task Main(string[] args)
        {
            try
            {
                // 2️⃣ Единая конфигурация
                var builder = WebApplication.CreateBuilder(args);
                var config  = builder.Configuration;

                var key    = config["OKX:ApiKey"];
                var secret = config["OKX:ApiSecret"];
                var pass   = config["OKX:Passphrase"];
                var symbol = config["OKX:InstrumentId"] ?? "SOL-USDT";

                // 3️⃣ Создаём бота и передаём IConfiguration
                await using var bot = new TradingBot(key!, secret!, pass!, config, symbol);
                await bot.StartAsync();

                builder.Services.AddSingleton(bot);
                var app = builder.Build();

                // 4️⃣ Web-хуки
                app.MapPost("/tv-signal", async (TvSignalDto dto, TradingBot bot) =>
                {
                    try
                    {
                        Console.WriteLine($"📡 Получен сигнал от TradingView: {dto.Side} {dto.Price}");

                        var side = dto.Side.Equals("buy", StringComparison.OrdinalIgnoreCase)
                                   ? OrderSide.Buy
                                   : OrderSide.Sell;

                        var riskUsd = 10m;
                        var sz      = Math.Round(riskUsd / dto.Price, 3);

                        var ok = await bot.PlaceOrder(side, sz);
                        return ok
                            ? Results.Ok(new { status = "success" })
                            : Results.BadRequest(new { status = "error" });
                    }
                    catch (Exception ex)
                    {
                        Console.WriteLine($"❌ Ошибка обработки сигнала: {ex.Message}");
                        return Results.BadRequest(new { status = "error", message = ex.Message });
                    }
                });

                app.MapGet("/ping",    () => "OK");
                app.MapGet("/status", async (TradingBot bot) =>
                {
                    var positions = bot.Strategy.GetPositions();
                    return Results.Json(new
                    {
                        status          = "running",
                        activePositions = positions.Count(p => p.Status == PositionStatus.Open),
                        totalPositions  = positions.Count
                    });
                });

                Console.WriteLine("🎯 Webhook endpoint: http://localhost:5500/tv-signal");
                Console.WriteLine("📊 Status endpoint:  http://localhost:5500/status");

                _ = app.RunAsync("http://0.0.0.0:5500");

                Console.WriteLine("Нажмите ENTER для остановки…");
                Console.ReadLine();
            }
            catch (Exception ex)
            {
                Console.WriteLine($"❌ Критическая ошибка: {ex.Message}");
                Console.WriteLine(ex.StackTrace);
            }
        }
    }

    public record TvSignalDto(string Side, decimal Price, long Time);
}