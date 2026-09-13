package bot

import (
	"context"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestHandleStart_TeacherSkipsRegistration — /start от самого преподавателя
// не должен ставить StateAwaitingName. Без этой проверки следующий текстовый
// ответ преподавателя ушёл бы в handleTeacherMessage (там нет case для
// StateAwaitingName) и завис бы без ответа — именно так это и
// воспроизвелось при ручном тестировании ботом с одного аккаунта.
func TestHandleStart_TeacherSkipsRegistration(t *testing.T) {
	h := makeHandler(t) // makeHandler: TeacherTelegramID = 999
	msg := &tgbotapi.Message{
		From: &tgbotapi.User{ID: 999},
		Text: "/start",
	}
	h.handleStart(context.Background(), msg)
	assert.Equal(t, StateIdle, h.dialog.Get(999).State)
}

// TestHandleStart_ParentEntersRegistration — контрольный случай: обычный
// (не-teacher) пользователь после /start действительно попадает в
// StateAwaitingName.
func TestHandleStart_ParentEntersRegistration(t *testing.T) {
	h := makeHandler(t)
	msg := &tgbotapi.Message{
		From: &tgbotapi.User{ID: 1},
		Text: "/start",
	}
	h.handleStart(context.Background(), msg)
	assert.Equal(t, StateAwaitingName, h.dialog.Get(1).State)
}

// TestHandleMyStudents_NotRegistered — родитель, ещё не прошедший
// регистрацию, при нажатии "Мои ученики" получает вежливую подсказку,
// а не ошибку.
func TestHandleMyStudents_NotRegistered(t *testing.T) {
	h := makeHandler(t)
	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 1}}
	h.handleMyStudents(context.Background(), msg)

	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "/start")
}

// TestHandleMyStudents_NoneLinked — зарегистрированный родитель, у которого
// пока нет ни одного привязанного ученика.
func TestHandleMyStudents_NoneLinked(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	u, err := h.store.CreateUser(ctx, "Мама")
	require.NoError(t, err)
	require.NoError(t, h.store.SaveAccount(ctx, u.ID, MessengerName, "1", "mama"))

	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 1}}
	h.handleMyStudents(ctx, msg)

	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "не привязано")
}

// TestHandleMyStudents_WithStudents — основной сценарий: родитель видит
// имена своих (и только своих) учеников.
func TestHandleMyStudents_WithStudents(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()

	u, err := h.store.CreateUser(ctx, "Мама")
	require.NoError(t, err)
	require.NoError(t, h.store.SaveAccount(ctx, u.ID, MessengerName, "1", "mama"))

	st, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)
	require.NoError(t, h.store.LinkContact(ctx, st.ID, u.ID, "Мама"))

	// Чужой ученик, не привязанный к этому родителю — не должен попасть в ответ.
	other, err := h.store.CreateStudent(ctx, "Чужой")
	require.NoError(t, err)
	_ = other

	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 1}}
	h.handleMyStudents(ctx, msg)

	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "Петя")
	assert.NotContains(t, last.Text, "Чужой")
}
