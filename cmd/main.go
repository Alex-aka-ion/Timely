// Package main — точка входа Booking Bot.
//
// Один бинарник запускает:
//   - Telegram бот (long-polling)
//   - Планировщик напоминаний (горутина с тикером)
//
// Поддерживает:
//   --auth — первичная OAuth-авторизация Google Calendar (создаёт token.json).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/booking-bot/booking-bot/internal/bot"
	"github.com/booking-bot/booking-bot/internal/calendar"
	"github.com/booking-bot/booking-bot/internal/config"
	"github.com/booking-bot/booking-bot/internal/logger"
	"github.com/booking-bot/booking-bot/internal/notify"
	"github.com/booking-bot/booking-bot/internal/scheduler"
	"github.com/booking-bot/booking-bot/internal/store"
)

func main() {
	authMode := flag.Bool("auth", false, "первичная OAuth-авторизация Google Calendar")
	flag.Parse()

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	log := logger.New(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(log)

	// --auth: только OAuth flow, без запуска бота.
	if *authMode {
		ctx := context.Background()
		if err := calendar.RunAuthFlow(ctx, cfg.GoogleCredentialsPath, cfg.GoogleTokenPath); err != nil {
			log.Error("auth flow", "error", err)
			os.Exit(1)
		}
		return
	}

	// Готовим директорию БД, если её нет.
	if dir := filepath.Dir(cfg.DBPath); dir != "" {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			log.Error("создание директории БД", "error", err)
			os.Exit(1)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	ctx = logger.WithContext(ctx, log)
	defer cancel()

	// Перехват SIGINT / SIGTERM для graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		s := <-sigCh
		log.Info("получен сигнал, останавливаемся", "signal", s.String())
		cancel()
	}()

	// Store + миграции.
	st, err := store.NewSQLite(cfg.DBPath)
	if err != nil {
		log.Error("инициализация БД", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("закрытие БД", "error", err)
		}
	}()

	// Telegram API.
	tgAPI, err := tgbotapi.NewBotAPI(cfg.BotToken)
	if err != nil {
		log.Error("инициализация telegram", "error", err)
		os.Exit(1)
	}
	tgAPI.Debug = false

	// Calendar.
	calClient, err := calendar.NewGoogleClient(ctx, cfg.GoogleCredentialsPath, cfg.GoogleTokenPath)
	if err != nil {
		log.Error("инициализация calendar", "error", err)
		os.Exit(1)
	}

	// Sender + Dispatcher.
	tgSender := bot.NewTelegramSender(tgAPI)
	dispatcher := notify.NewDispatcher(st, tgSender)

	// Admin UI.
	adminUI := bot.NewTelegramAdminUI(tgAPI, cfg.TeacherTelegramID)

	// Bot Handler.
	handler := bot.NewHandler(bot.HandlerDeps{
		API:         tgAPI,
		BotUsername: tgAPI.Self.UserName,
		Cfg:         cfg,
		Store:       st,
		Dispatch:    dispatcher,
		AdminUI:     adminUI,
		CalClient:   calClient,
	})

	// Scheduler.
	sched := scheduler.New(scheduler.Deps{
		Cfg:        cfg,
		Store:      st,
		CalClient:  calClient,
		Dispatcher: dispatcher,
	})

	// Запускаем обоих в горутинах.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if err := handler.Run(ctx); err != nil {
			log.Error("bot exit", "error", err)
		}
	}()
	go func() {
		defer wg.Done()
		if err := sched.Run(ctx); err != nil {
			log.Error("scheduler exit", "error", err)
		}
	}()

	log.Info("система запущена")
	wg.Wait()

	// Даём логам прокачаться.
	time.Sleep(50 * time.Millisecond)
	log.Info("выход")
}
