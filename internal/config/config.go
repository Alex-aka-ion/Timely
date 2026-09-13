// Package config читает конфигурацию из переменных окружения и валидирует её.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config — все настройки приложения, прочитанные при старте.
type Config struct {
	// Telegram
	BotToken          string
	TeacherTelegramID int64
	// DevTelegramID — необязательный второй telegram_id с теми же правами,
	// что у преподавателя (isTeacher в bot.go проверяет оба). Нужен, чтобы
	// разработчик мог параллельно с преподавателем видеть все действия и
	// уведомления (admin.UI шлёт их обоим — см. TelegramAdminUI) и при
	// необходимости сам нажимать кнопки/выполнять команды преподавателя,
	// не деля с ним один аккаунт. 0, если не задан.
	DevTelegramID int64

	// База данных
	DBPath string

	// Google Calendar
	GoogleCredentialsPath string
	GoogleTokenPath       string
	GoogleCalendarID      string

	// Напоминания и планировщик
	ReminderIntervals []time.Duration
	SchedulerTick     time.Duration

	// Логи
	LogLevel  string // debug | info | warn | error
	LogFormat string // json | text
}

// Load читает .env (если он есть) и возвращает валидированный Config.
//
// .env загружается только если он существует — в продакшене переменные
// окружения обычно подаются через systemd EnvironmentFile.
func Load() (*Config, error) {
	// Игнорируем ошибку: .env может не существовать в проде.
	_ = godotenv.Load()

	c := &Config{
		BotToken:              strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		DBPath:                getenvOr("DB_PATH", "./data/booking.db"),
		GoogleCredentialsPath: getenvOr("GOOGLE_CREDENTIALS_PATH", "./credentials.json"),
		GoogleTokenPath:       getenvOr("GOOGLE_TOKEN_PATH", "./token.json"),
		GoogleCalendarID:      getenvOr("GOOGLE_CALENDAR_ID", "primary"),
		LogLevel:              strings.ToLower(getenvOr("LOG_LEVEL", "info")),
		LogFormat:             strings.ToLower(getenvOr("LOG_FORMAT", "json")),
	}

	teacherIDStr := strings.TrimSpace(os.Getenv("TEACHER_TELEGRAM_ID"))
	if teacherIDStr == "" {
		return nil, errors.New("TEACHER_TELEGRAM_ID не задан")
	}
	teacherID, err := strconv.ParseInt(teacherIDStr, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("TEACHER_TELEGRAM_ID должен быть числом: %w", err)
	}
	if teacherID <= 0 {
		return nil, errors.New("TEACHER_TELEGRAM_ID должен быть положительным числом")
	}
	c.TeacherTelegramID = teacherID

	// DEV_TELEGRAM_ID необязателен — в отличие от TEACHER_TELEGRAM_ID,
	// пустое значение не ошибка, а просто "не задан" (0).
	if devIDStr := strings.TrimSpace(os.Getenv("DEV_TELEGRAM_ID")); devIDStr != "" {
		devID, err := strconv.ParseInt(devIDStr, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("DEV_TELEGRAM_ID должен быть числом: %w", err)
		}
		if devID <= 0 {
			return nil, errors.New("DEV_TELEGRAM_ID должен быть положительным числом")
		}
		c.DevTelegramID = devID
	}

	intervals, err := ParseIntervals(getenvOr("REMINDER_INTERVALS", "24h,2h"))
	if err != nil {
		return nil, fmt.Errorf("REMINDER_INTERVALS: %w", err)
	}
	c.ReminderIntervals = intervals

	tick, err := time.ParseDuration(getenvOr("SCHEDULER_TICK", "5m"))
	if err != nil {
		return nil, fmt.Errorf("SCHEDULER_TICK: %w", err)
	}
	if tick <= 0 {
		return nil, errors.New("SCHEDULER_TICK должен быть положительным")
	}
	c.SchedulerTick = tick

	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Validate проверяет что обязательные поля заполнены и корректны.
func (c *Config) Validate() error {
	if c.BotToken == "" {
		return errors.New("BOT_TOKEN не задан")
	}
	if c.TeacherTelegramID <= 0 {
		return errors.New("TEACHER_TELEGRAM_ID должен быть положительным")
	}
	if c.DevTelegramID < 0 {
		return errors.New("DEV_TELEGRAM_ID не может быть отрицательным")
	}
	if c.DBPath == "" {
		return errors.New("DB_PATH не задан")
	}
	if len(c.ReminderIntervals) == 0 {
		return errors.New("REMINDER_INTERVALS пуст")
	}
	if !validLogLevel(c.LogLevel) {
		return fmt.Errorf("LOG_LEVEL должен быть debug|info|warn|error, получено %q", c.LogLevel)
	}
	if c.LogFormat != "json" && c.LogFormat != "text" {
		return fmt.Errorf("LOG_FORMAT должен быть json|text, получено %q", c.LogFormat)
	}
	return nil
}

// MaxInterval возвращает наибольший интервал напоминания.
// Используется планировщиком для определения окна выборки событий.
func (c *Config) MaxInterval() time.Duration {
	var max time.Duration
	for _, d := range c.ReminderIntervals {
		if d > max {
			max = d
		}
	}
	return max
}

// ParseIntervals парсит строку вида "24h,2h,30m" в []time.Duration.
// Чистая функция — легко тестируется.
func ParseIntervals(s string) ([]time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("пустая строка")
	}
	parts := strings.Split(s, ",")
	out := make([]time.Duration, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, errors.New("пустой элемент в списке интервалов")
		}
		d, err := time.ParseDuration(p)
		if err != nil {
			return nil, fmt.Errorf("неверный интервал %q: %w", p, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("интервал должен быть положительным: %q", p)
		}
		out = append(out, d)
	}
	return out, nil
}

func getenvOr(key, fallback string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return fallback
}

func validLogLevel(s string) bool {
	switch s {
	case "debug", "info", "warn", "error":
		return true
	}
	return false
}
