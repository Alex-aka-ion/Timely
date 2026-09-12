package bot

import (
	"context"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/booking-bot/booking-bot/internal/admin"
	"github.com/booking-bot/booking-bot/internal/config"
	"github.com/booking-bot/booking-bot/internal/store"
)

// newTestStore — копия helper'а из store/sqlite_test.go в этом пакете
// для тестов хэндлеров.
func newTestStore(t *testing.T) store.Store {
	t.Helper()
	s, err := store.NewSQLiteInMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// makeHandler — Handler с in-memory store и без реального Calendar.
// API — fakeTelegramAPI: не ходит в сеть, но и не паникует на вызовах
// Send/Request/answerCallback, которые случаются даже для заблокированных
// (не-teacher) пользователей — так того требует протокол Telegram.
func makeHandler(t *testing.T) *Handler {
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
	})
}

func TestIsTeacher(t *testing.T) {
	h := makeHandler(t)
	assert.True(t, h.isTeacher(999))
	assert.False(t, h.isTeacher(1))
}

func TestRequireTeacher_BlocksNonTeacher(t *testing.T) {
	h := makeHandler(t)
	// Имитируем callback от не-преподавателя.
	cb := &tgbotapi.CallbackQuery{
		ID:   "x",
		From: &tgbotapi.User{ID: 1},
		Data: cbCancel,
	}
	// Нет паники, нет state-changes для других пользователей.
	h.handleCallback(context.Background(), cb)
	// dialog для произвольного userID остаётся Idle.
	assert.Equal(t, StateIdle, h.dialog.Get(1).State)
}

func TestStudentDetails_NotFound(t *testing.T) {
	h := makeHandler(t)
	text, _, err := h.studentDetails(context.Background(), 9999)
	require.NoError(t, err)
	assert.Contains(t, text, "не найден")
}

func TestStudentDetails_WithContacts(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	s, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)
	u, err := h.store.CreateUser(ctx, "Мама")
	require.NoError(t, err)
	require.NoError(t, h.store.LinkContact(ctx, s.ID, u.ID, "Мама"))

	text, _, err := h.studentDetails(ctx, s.ID)
	require.NoError(t, err)
	assert.Contains(t, text, "Петя")
	assert.Contains(t, text, "Мама")
}

func TestParseCallback_OK(t *testing.T) {
	prefix, args, ok := parseCallback("foo:1:2")
	require.True(t, ok)
	assert.Equal(t, "foo", prefix)
	assert.Equal(t, []int64{1, 2}, args)
}

func TestParseCallback_NonNumeric(t *testing.T) {
	_, _, ok := parseCallback("foo:abc")
	assert.False(t, ok)
}
