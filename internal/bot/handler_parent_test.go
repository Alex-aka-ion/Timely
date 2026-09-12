package bot

import (
	"context"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
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
