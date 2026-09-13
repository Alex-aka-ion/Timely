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

// --- /rename -------------------------------------------------------------

// TestHandleRenameStart_NotRegistered — /rename до /start не должен ставить
// StateAwaitingNewName (иначе следующее сообщение "уйдёт" в UpdateUserName
// для несуществующего пользователя).
func TestHandleRenameStart_NotRegistered(t *testing.T) {
	h := makeHandler(t)
	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 1}, Text: "/rename"}
	h.handleRenameStart(context.Background(), msg)

	assert.Equal(t, StateIdle, h.dialog.Get(1).State)
	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "/start")
}

// TestHandleRenameStart_TeacherGuarded — /rename от преподавателя не должен
// ничего трогать в его состоянии диалога.
func TestHandleRenameStart_TeacherGuarded(t *testing.T) {
	h := makeHandler(t) // TeacherTelegramID = 999
	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 999}, Text: "/rename"}
	h.handleRenameStart(context.Background(), msg)

	assert.Equal(t, StateIdle, h.dialog.Get(999).State)
	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "родителей")
}

// TestHandleRenameStart_PromptsWithCurrentName — зарегистрированный родитель
// переходит в StateAwaitingNewName, а подсказка содержит его текущее имя.
func TestHandleRenameStart_PromptsWithCurrentName(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	u, err := h.store.CreateUser(ctx, "Вася Пупкин")
	require.NoError(t, err)
	require.NoError(t, h.store.SaveAccount(ctx, u.ID, MessengerName, "1", "vasya"))

	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 1}, Text: "/rename"}
	h.handleRenameStart(ctx, msg)

	assert.Equal(t, StateAwaitingNewName, h.dialog.Get(1).State)
	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "Вася Пупкин")
}

// TestHandleParentMessage_RenameUpdatesName — полный сценарий: /rename,
// затем ввод нового имени должен обновить store и сбросить диалог, а
// изменение — быть видно там, где имя контакта достаётся живым JOIN
// (GetStudentContacts), без отдельной синхронизации.
func TestHandleParentMessage_RenameUpdatesName(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	u, err := h.store.CreateUser(ctx, "Вася Пупкин")
	require.NoError(t, err)
	require.NoError(t, h.store.SaveAccount(ctx, u.ID, MessengerName, "1", "vasya"))
	st, err := h.store.CreateStudent(ctx, "Сергей")
	require.NoError(t, err)
	require.NoError(t, h.store.LinkContact(ctx, st.ID, u.ID, "Отец"))

	h.handleRenameStart(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 1}, Text: "/rename"})
	require.Equal(t, StateAwaitingNewName, h.dialog.Get(1).State)

	h.handleParentMessage(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 1}, Text: "Василий Пупкин"})

	assert.Equal(t, StateIdle, h.dialog.Get(1).State, "диалог должен сброситься после успешного переименования")

	contacts, err := h.store.GetStudentContacts(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, contacts, 1)
	assert.Equal(t, "Василий Пупкин", contacts[0].FullName)

	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "обновлено")
}

// TestHandleParentMessage_RenameRejectsEmptyName — пустой ввод не должен
// сбрасывать StateAwaitingNewName: родитель остаётся в диалоге и может
// попробовать снова, вместо того чтобы "потерять" запрос молча.
func TestHandleParentMessage_RenameRejectsEmptyName(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	u, err := h.store.CreateUser(ctx, "Вася Пупкин")
	require.NoError(t, err)
	require.NoError(t, h.store.SaveAccount(ctx, u.ID, MessengerName, "1", "vasya"))

	h.handleRenameStart(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 1}, Text: "/rename"})
	h.handleParentMessage(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 1}, Text: "   "})

	assert.Equal(t, StateAwaitingNewName, h.dialog.Get(1).State)
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
