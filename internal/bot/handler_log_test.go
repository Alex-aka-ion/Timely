package bot

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/booking-bot/booking-bot/internal/admin"
	"github.com/booking-bot/booking-bot/internal/config"
	"github.com/booking-bot/booking-bot/internal/logger"
)

// makeHandlerWithRing — то же, что makeHandler, но с заданным LogRing:
// makeHandler его не заводит (LogRing остаётся nil), а этим тестам он нужен.
func makeHandlerWithRing(t *testing.T, ring *logger.Ring) *Handler {
	t.Helper()
	cfg := &config.Config{
		TeacherTelegramID: 999,
		ReminderIntervals: []time.Duration{2 * time.Hour},
		SchedulerTick:     time.Minute,
	}
	return NewHandler(HandlerDeps{
		API:     &fakeTelegramAPI{},
		Cfg:     cfg,
		Store:   newTestStore(t),
		AdminUI: admin.Noop{},
		LogRing: ring,
	})
}

func lastSentText(t *testing.T, h *Handler) string {
	t.Helper()
	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	msg, ok := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	require.True(t, ok)
	return msg.Text
}

// TestHandleLog_NoRing — makeHandler (как в остальных тестах пакета) не
// заводит LogRing; /log не должен паниковать на nil, а вежливо ответить.
func TestHandleLog_NoRing(t *testing.T) {
	h := makeHandler(t)
	h.handleLog(context.Background(), &tgbotapi.Message{From: &tgbotapi.User{ID: 999}})
	assert.Contains(t, lastSentText(t, h), "недоступен")
}

func TestHandleLog_EmptyRing(t *testing.T) {
	h := makeHandlerWithRing(t, logger.NewRing(50))
	h.handleLog(context.Background(), &tgbotapi.Message{From: &tgbotapi.User{ID: 999}})
	assert.Contains(t, lastSentText(t, h), "пуст")
}

// TestHandleLog_ReturnsRecentEntries — сквозной сценарий: запись через
// реальный slog.Logger, обёрнутый WrapWithRing, должна дойти до /log в
// читаемом виде (не JSON), с сообщением и атрибутами.
func TestHandleLog_ReturnsRecentEntries(t *testing.T) {
	ring := logger.NewRing(50)
	base := logger.NewWithWriter(io.Discard, "info", "json")
	log := slog.New(logger.WrapWithRing(base.Handler(), ring))
	log.Info("bot started", "username", "test_bot")
	log.Info("scheduler tick", "tick", "5m0s")

	h := makeHandlerWithRing(t, ring)
	h.handleLog(context.Background(), &tgbotapi.Message{From: &tgbotapi.User{ID: 999}})

	text := lastSentText(t, h)
	assert.Contains(t, text, "bot started")
	assert.Contains(t, text, "username=test_bot")
	assert.Contains(t, text, "scheduler tick")
	assert.Contains(t, text, "INFO")
}

// TestHandleLog_RequiresTeacher — не-преподаватель не должен получить
// вообще никакого ответа, как и у остальных teacher-хэндлеров.
func TestHandleLog_RequiresTeacher(t *testing.T) {
	h := makeHandlerWithRing(t, logger.NewRing(50))
	h.handleLog(context.Background(), &tgbotapi.Message{From: &tgbotapi.User{ID: 1}})
	api := h.api.(*fakeTelegramAPI)
	assert.Empty(t, api.sent)
}

// TestHandleLog_TruncatesToNewest — если записей больше, чем влезает в
// лимит Telegram, должны остаться самые СВЕЖИЕ (не самые старые) —
// для отладки "что сейчас происходит" важен именно хвост.
func TestHandleLog_TruncatesToNewest(t *testing.T) {
	ring := logger.NewRing(500)
	base := logger.NewWithWriter(io.Discard, "info", "json")
	log := slog.New(logger.WrapWithRing(base.Handler(), ring))
	for i := 0; i < 200; i++ {
		log.Info("line", "n", strconv.Itoa(i))
	}

	h := makeHandlerWithRing(t, ring)
	h.handleLog(context.Background(), &tgbotapi.Message{From: &tgbotapi.User{ID: 999}})

	text := lastSentText(t, h)
	assert.LessOrEqual(t, len(text), 4096, "сообщение не должно превышать лимит Telegram")
	assert.Contains(t, text, "n=199", "самая свежая запись должна присутствовать")
	assert.False(t, strings.Contains(text, "n=0\n") || strings.HasPrefix(text, "n=0"),
		"самая старая запись должна быть обрезана первой")
}
