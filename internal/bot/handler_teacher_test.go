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

// --- /unlinked_students ------------------------------------------------------

// TestHandleUnlinkedStudents_ShowsOnlyStudentsWithoutContacts — проверяет,
// что в список попадают ровно ученики без единого привязанного контакта,
// а не наоборот (это как раз обратная выборка к /unlinked, которую легко
// перепутать местами).
func TestHandleUnlinkedStudents_ShowsOnlyStudentsWithoutContacts(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()

	without, err := h.store.CreateStudent(ctx, "Сергей")
	require.NoError(t, err)
	with, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)
	u, err := h.store.CreateUser(ctx, "Мама")
	require.NoError(t, err)
	require.NoError(t, h.store.LinkContact(ctx, with.ID, u.ID, "Мама"))

	h.handleUnlinkedStudents(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 999}})

	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	// Имена учеников — это подписи кнопок, а не текст сообщения (см.
	// handleUnlinkedStudents), поэтому проверяем именно клавиатуру.
	buttons := inlineButtonTexts(t, last)
	assert.Contains(t, buttons, without.DisplayName)
	assert.NotContains(t, buttons, with.DisplayName)
}

// inlineButtonTexts достаёт подписи всех inline-кнопок сообщения — удобно
// для проверки списков, где полезная нагрузка (имя ученика/родителя) лежит
// в кнопке, а не в тексте сообщения.
func inlineButtonTexts(t *testing.T, msg tgbotapi.MessageConfig) []string {
	t.Helper()
	kb, ok := msg.ReplyMarkup.(tgbotapi.InlineKeyboardMarkup)
	if !ok {
		return nil
	}
	var texts []string
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			texts = append(texts, btn.Text)
		}
	}
	return texts
}

// TestHandleUnlinkedStudents_AllLinked — если у всех учеников есть контакт,
// должно быть вежливое сообщение, а не пустая клавиатура.
func TestHandleUnlinkedStudents_AllLinked(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()

	st, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)
	u, err := h.store.CreateUser(ctx, "Мама")
	require.NoError(t, err)
	require.NoError(t, h.store.LinkContact(ctx, st.ID, u.ID, "Мама"))

	h.handleUnlinkedStudents(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 999}})

	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "хотя бы один")
}

// --- переименование ученика ------------------------------------------------

// TestCallback_RenameStudent_SetsState — нажатие "Переименовать" в меню
// ученика переводит преподавателя в StateAwaitingStudentRename с ID ученика
// в Data (тот же ключ "student_id", что и у StateAwaitingIntervals).
func TestCallback_RenameStudent_SetsState(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	st, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)

	cb := &tgbotapi.CallbackQuery{
		ID:      "cb1",
		From:    &tgbotapi.User{ID: 999},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 999}, MessageID: 1},
		Data:    cbRenameStudent + ":" + itoa(st.ID),
	}
	h.handleCallback(ctx, cb)

	entry := h.dialog.Get(999)
	assert.Equal(t, StateAwaitingStudentRename, entry.State)
	assert.Equal(t, st.ID, entry.Data["student_id"])
}

// TestHandleTeacherMessage_RenameStudentUpdatesName — полный сценарий: после
// входа в StateAwaitingStudentRename следующее сообщение обновляет
// display_name ученика и сбрасывает диалог.
func TestHandleTeacherMessage_RenameStudentUpdatesName(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	st, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)

	h.dialog.Set(999, StateAwaitingStudentRename, map[string]any{"student_id": st.ID})
	h.handleTeacherMessage(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 999}, Text: "Пётр"})

	assert.Equal(t, StateIdle, h.dialog.Get(999).State)

	students, err := h.store.GetStudents(ctx)
	require.NoError(t, err)
	require.Len(t, students, 1)
	assert.Equal(t, "Пётр", students[0].DisplayName)

	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "обновлено")
}

// TestHandleTeacherMessage_RenameStudentRejectsEmptyName — пустой ввод не
// должен сбрасывать StateAwaitingStudentRename: преподаватель остаётся в
// диалоге и может попробовать снова.
func TestHandleTeacherMessage_RenameStudentRejectsEmptyName(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	st, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)

	h.dialog.Set(999, StateAwaitingStudentRename, map[string]any{"student_id": st.ID})
	h.handleTeacherMessage(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 999}, Text: "   "})

	assert.Equal(t, StateAwaitingStudentRename, h.dialog.Get(999).State)
	students, err := h.store.GetStudents(ctx)
	require.NoError(t, err)
	require.Len(t, students, 1)
	assert.Equal(t, "Петя", students[0].DisplayName, "имя не должно было измениться")
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

// --- "Отвязать событие" (карточка ученика) ----------------------------------

// TestCallback_StudentEvents_NoEvents — у ученика без привязанных событий
// список должен сказать об этом, а не показать пустую клавиатуру.
func TestCallback_StudentEvents_NoEvents(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	s, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)

	cb := &tgbotapi.CallbackQuery{
		ID:      "cb1",
		From:    &tgbotapi.User{ID: 999},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 999}, MessageID: 1},
		Data:    cbStudentEvents + ":" + itoa(s.ID),
	}
	h.handleCallback(ctx, cb)

	api := h.api.(*fakeTelegramAPI)
	last := lastEdit(t, api)
	assert.Contains(t, last.Text, "не привязано ни одного события")
}

// TestCallback_StudentEvents_UnlinkFlow — полный сценарий: у ученика есть
// привязанное событие → "Отвязать событие" показывает его кнопкой →
// нажатие → подтверждение → UnlinkEvent действительно снимает привязку.
// Проверяем заодно, что callback_data кладёт короткий токен (eventtoken.go),
// а не сам master_event_id — так же, как в /events.
func TestCallback_StudentEvents_UnlinkFlow(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	s, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)
	const masterEventID = "evt-1"
	require.NoError(t, h.store.LinkEvent(ctx, masterEventID, s.ID))

	cb := &tgbotapi.CallbackQuery{
		ID:      "cb1",
		From:    &tgbotapi.User{ID: 999},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 999}, MessageID: 1},
		Data:    cbStudentEvents + ":" + itoa(s.ID),
	}
	h.handleCallback(ctx, cb)

	api := h.api.(*fakeTelegramAPI)
	list := lastEdit(t, api)
	require.Len(t, list.ReplyMarkup.InlineKeyboard, 1)
	unlinkCB := list.ReplyMarkup.InlineKeyboard[0][0].CallbackData
	require.NotNil(t, unlinkCB)
	assert.NotContains(t, *unlinkCB, masterEventID, "callback_data должен содержать токен, а не сам event ID")

	// Нажатие "Отвязать: ..." → запрос подтверждения.
	cb.Data = *unlinkCB
	h.handleCallback(ctx, cb)
	confirm := lastEdit(t, api)
	assert.Contains(t, confirm.Text, "Точно отвязать событие")
	require.Len(t, confirm.ReplyMarkup.InlineKeyboard, 1)
	confirmCB := confirm.ReplyMarkup.InlineKeyboard[0][0].CallbackData
	require.NotNil(t, confirmCB)

	// Подтверждение → реальная отвязка в store.
	cb.Data = *confirmCB
	h.handleCallback(ctx, cb)
	done := lastEdit(t, api)
	assert.Contains(t, done.Text, "отвязано")

	_, err = h.store.GetStudentForEvent(ctx, masterEventID)
	assert.ErrorIs(t, err, store.ErrNotFound)
}

// TestCallback_StudentEvents_AutoUnlinksDeletedEvent — баг-репорт: событие,
// удалённое из Google Calendar, оставалось привязанным к ученику навсегда
// (UpcomingMasters, на которое раньше опирался вывод списка, ограничен
// 14-дневным горизонтом и молча не находит событие что удалённое, что
// просто вне окна — отличить одно от другого не может). GetEvent ищет по ID
// напрямую, поэтому при ErrEventNotFound cbStudentEvents должен сам вызвать
// UnlinkEvent, а не просто показать невнятную подпись.
func TestCallback_StudentEvents_AutoUnlinksDeletedEvent(t *testing.T) {
	h := makeHandler(t)
	h.calClient = &fakeCalendarForBot{deleted: map[string]bool{"evt-deleted": true}}
	ctx := context.Background()
	s, err := h.store.CreateStudent(ctx, "Петя")
	require.NoError(t, err)
	require.NoError(t, h.store.LinkEvent(ctx, "evt-deleted", s.ID))

	cb := &tgbotapi.CallbackQuery{
		ID:      "cb1",
		From:    &tgbotapi.User{ID: 999},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 999}, MessageID: 1},
		Data:    cbStudentEvents + ":" + itoa(s.ID),
	}
	h.handleCallback(ctx, cb)

	api := h.api.(*fakeTelegramAPI)
	last := lastEdit(t, api)
	assert.Contains(t, last.Text, "отвязано автоматически: 1")
	assert.Contains(t, last.Text, "не привязано ни одного события")

	// Привязка в store должна была реально исчезнуть, а не только пропасть
	// из вывода.
	_, err = h.store.GetStudentForEvent(ctx, "evt-deleted")
	assert.ErrorIs(t, err, store.ErrNotFound)
}

// --- "Новый ученик" при выборе события (/events) -----------------------------

// TestCallback_PickEvent_OffersNewStudent — список для привязки события
// должен всегда предлагать создать нового ученика, а не только выбирать из
// уже существующих: раньше пустой список учеников был тупиком ("Сначала
// создайте ученика." без какого-либо действия) — учитель должен был выйти
// из /events, создать ученика через /students_new и начинать привязку
// заново.
func TestCallback_PickEvent_OffersNewStudent(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	token := h.eventTokens.tokenFor("evt-1")

	cb := &tgbotapi.CallbackQuery{
		ID:      "cb1",
		From:    &tgbotapi.User{ID: 999},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 999}, MessageID: 1},
		Data:    cbPickEvent + ":" + token,
	}
	h.handleCallback(ctx, cb)

	last := lastEdit(t, h.api.(*fakeTelegramAPI))
	require.NotNil(t, last.ReplyMarkup)
	var texts []string
	var newStudentCB string
	for _, row := range last.ReplyMarkup.InlineKeyboard {
		for _, btn := range row {
			texts = append(texts, btn.Text)
			if btn.Text == "➕ Новый ученик" {
				require.NotNil(t, btn.CallbackData)
				newStudentCB = *btn.CallbackData
			}
		}
	}
	assert.Contains(t, texts, "➕ Новый ученик")
	assert.Equal(t, cbNewStudentForEvent+":"+token, newStudentCB)
}

// TestCallback_NewStudentForEvent_CreatesAndLinks — полный сценарий:
// /events → выбрать событие → "➕ Новый ученик" → ввести имя → ученик
// создан и событие сразу привязано к нему, без отдельного похода в
// /students_new и обратно в /events.
func TestCallback_NewStudentForEvent_CreatesAndLinks(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	token := h.eventTokens.tokenFor("evt-1")

	cb := &tgbotapi.CallbackQuery{
		ID:      "cb1",
		From:    &tgbotapi.User{ID: 999},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 999}, MessageID: 1},
		Data:    cbNewStudentForEvent + ":" + token,
	}
	h.handleCallback(ctx, cb)

	entry := h.dialog.Get(999)
	assert.Equal(t, StateAwaitingStudentName, entry.State)
	assert.Equal(t, token, entry.Data[pendingEventKey])

	h.handleTeacherMessage(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 999}, Text: "Сергей"})

	assert.Equal(t, StateIdle, h.dialog.Get(999).State, "диалог должен закрыться после создания")

	students, err := h.store.GetStudents(ctx)
	require.NoError(t, err)
	require.Len(t, students, 1)
	assert.Equal(t, "Сергей", students[0].DisplayName)

	linked, err := h.store.GetStudentForEvent(ctx, "evt-1")
	require.NoError(t, err)
	assert.Equal(t, students[0].ID, linked.ID)

	api := h.api.(*fakeTelegramAPI)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "создан")
	assert.Contains(t, last.Text, "событие привязано")
}

// TestCallback_NewStudentForEvent_StaleToken — бот перезапустили между
// показом /events и вводом имени нового ученика: eventTokens не переживает
// перезапуск (см. eventtoken.go), поэтому привязать событие уже нельзя.
// Ученик всё равно должен быть создан — данные, которые ввёл преподаватель,
// не должны потеряться, — но с явным сообщением, что до привязки дело не
// дошло.
func TestCallback_NewStudentForEvent_StaleToken(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()

	h.dialog.Set(999, StateAwaitingStudentName, map[string]any{
		pendingEventKey: "does-not-exist",
	})
	h.handleTeacherMessage(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 999}, Text: "Сергей"})

	assert.Equal(t, StateIdle, h.dialog.Get(999).State)
	students, err := h.store.GetStudents(ctx)
	require.NoError(t, err)
	require.Len(t, students, 1, "ученик должен быть создан, несмотря на устаревший токен")

	api := h.api.(*fakeTelegramAPI)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "устарел")
}

// lastEdit достаёт последнее отправленное EditMessageTextConfig — то, чем
// editText() обновляет сообщение с inline-клавиатурой.
func lastEdit(t *testing.T, api *fakeTelegramAPI) tgbotapi.EditMessageTextConfig {
	t.Helper()
	require.NotEmpty(t, api.sent)
	edit, ok := api.sent[len(api.sent)-1].(tgbotapi.EditMessageTextConfig)
	require.True(t, ok, "последний Send должен быть EditMessageTextConfig")
	return edit
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
