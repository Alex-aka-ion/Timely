package bot

import (
	"context"
	"strconv"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/booking-bot/booking-bot/internal/admin"
	"github.com/booking-bot/booking-bot/internal/calendar"
	"github.com/booking-bot/booking-bot/internal/config"
	"github.com/booking-bot/booking-bot/internal/notify"
)

// fakeAdminUI записывает вызовы NotifyStopRequest — чтобы проверить, что
// handleStop действительно уведомляет преподавателя, а не только
// деактивирует аккаунт.
type fakeAdminUI struct {
	stopCalls []struct {
		UserID   int64
		FullName string
	}
}

func (f *fakeAdminUI) NotifyNewUser(context.Context, int64, string) error { return nil }

func (f *fakeAdminUI) NotifyStopRequest(_ context.Context, userID int64, fullName string) error {
	f.stopCalls = append(f.stopCalls, struct {
		UserID   int64
		FullName string
	}{userID, fullName})
	return nil
}

func (f *fakeAdminUI) NotifyError(context.Context, string, string) error { return nil }

var _ admin.UI = (*fakeAdminUI)(nil)

// fakeCalendarForBot — минимальная реализация calendar.Client для тестов
// bot-пакета (свой fakeCalendar в internal/scheduler недоступен отсюда —
// другой пакет).
type fakeCalendarForBot struct {
	masters []calendar.Event
	// deleted — master_event_id, которые GetEvent должен считать удалёнными
	// из календаря (calendar.ErrEventNotFound), даже если они присутствуют
	// в masters — имитирует событие, которое пропало между /events и тем,
	// как преподаватель открыл карточку ученика.
	deleted map[string]bool
}

func (f *fakeCalendarForBot) UpcomingMasters(context.Context, string, time.Duration) ([]calendar.Event, error) {
	return f.masters, nil
}
func (f *fakeCalendarForBot) UpcomingInstances(context.Context, string, time.Time, time.Time) ([]calendar.Instance, error) {
	return nil, nil
}
func (f *fakeCalendarForBot) GetEvent(_ context.Context, _ string, eventID string) (calendar.Event, error) {
	if f.deleted[eventID] {
		return calendar.Event{}, calendar.ErrEventNotFound
	}
	for _, e := range f.masters {
		if e.ID == eventID {
			return e, nil
		}
	}
	return calendar.Event{}, calendar.ErrEventNotFound
}
func (f *fakeCalendarForBot) UpdateSummary(context.Context, string, string, string) error {
	return nil
}

var _ calendar.Client = (*fakeCalendarForBot)(nil)

// fakeBotSender — записывает, кому и что отправили через notify.Dispatcher.
type fakeBotSender struct {
	sent []struct{ ExternalID, Text string }
}

func (f *fakeBotSender) Messenger() string { return MessengerName }
func (f *fakeBotSender) Send(_ context.Context, externalID, text string) error {
	f.sent = append(f.sent, struct{ ExternalID, Text string }{externalID, text})
	return nil
}

// --- /stop -------------------------------------------------------------

// TestHandleStop_NotRegistered — /stop от незарегистрированного пользователя
// не должен пытаться деактивировать несуществующий аккаунт или дёргать
// adminUI, а просто вежливо ответить.
func TestHandleStop_NotRegistered(t *testing.T) {
	admin := &fakeAdminUI{}
	cfg := &config.Config{TeacherTelegramID: 999}
	h := NewHandler(HandlerDeps{
		API: &fakeTelegramAPI{}, Cfg: cfg, Store: newTestStore(t), AdminUI: admin,
	})

	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 1}, Text: "/stop"}
	h.handleStop(context.Background(), msg)

	assert.Empty(t, admin.stopCalls)
	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "не зарегистрированы")
}

// TestHandleStop_TeacherGuarded — /stop от самого преподавателя не должен
// ничего деактивировать (аккаунта-то для деактивации у него как у родителя
// и нет) и не должен вызывать adminUI.
func TestHandleStop_TeacherGuarded(t *testing.T) {
	admin := &fakeAdminUI{}
	cfg := &config.Config{TeacherTelegramID: 999}
	h := NewHandler(HandlerDeps{
		API: &fakeTelegramAPI{}, Cfg: cfg, Store: newTestStore(t), AdminUI: admin,
	})

	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 999}, Text: "/stop"}
	h.handleStop(context.Background(), msg)

	assert.Empty(t, admin.stopCalls)
	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "родителей")
}

// TestHandleStop_DeactivatesAndNotifiesTeacher — основной сценарий: /stop
// зарегистрированного родителя (1) деактивирует его аккаунт — Dispatcher
// после этого не должен находить у него активных аккаунтов (это то, из-за
// чего реально перестают приходить напоминания), и (2) уведомляет
// преподавателя через adminUI.NotifyStopRequest с его именем.
func TestHandleStop_DeactivatesAndNotifiesTeacher(t *testing.T) {
	admin := &fakeAdminUI{}
	cfg := &config.Config{TeacherTelegramID: 999}
	st := newTestStore(t)
	h := NewHandler(HandlerDeps{
		API: &fakeTelegramAPI{}, Cfg: cfg, Store: st, AdminUI: admin,
	})
	ctx := context.Background()

	u, err := st.CreateUser(ctx, "Вася Пупкин")
	require.NoError(t, err)
	require.NoError(t, st.SaveAccount(ctx, u.ID, MessengerName, "1", "vasya"))

	msg := &tgbotapi.Message{From: &tgbotapi.User{ID: 1}, Text: "/stop"}
	h.handleStop(ctx, msg)

	require.Len(t, admin.stopCalls, 1)
	assert.Equal(t, u.ID, admin.stopCalls[0].UserID)
	assert.Equal(t, "Вася Пупкин", admin.stopCalls[0].FullName)

	accounts, err := st.GetActiveAccounts(ctx, u.ID)
	require.NoError(t, err)
	assert.Empty(t, accounts, "аккаунт должен стать неактивным сразу после /stop")

	api := h.api.(*fakeTelegramAPI)
	require.NotEmpty(t, api.sent)
	last := api.sent[len(api.sent)-1].(tgbotapi.MessageConfig)
	assert.Contains(t, last.Text, "не будете получать")
}

// TestCallback_StopRemove_UnlinksFromAllStudents — преподаватель нажимает
// "Удалить из контактов" после /stop: родитель должен отвязаться СРАЗУ ото
// всех своих учеников, а не только от одного (в отличие от точечного
// cbUnlinkContact).
func TestCallback_StopRemove_UnlinksFromAllStudents(t *testing.T) {
	cfg := &config.Config{TeacherTelegramID: 999}
	st := newTestStore(t)
	h := NewHandler(HandlerDeps{
		API: &fakeTelegramAPI{}, Cfg: cfg, Store: st, AdminUI: admin.Noop{},
	})
	ctx := context.Background()

	u, err := st.CreateUser(ctx, "Вася Пупкин")
	require.NoError(t, err)
	s1, err := st.CreateStudent(ctx, "Сергей")
	require.NoError(t, err)
	s2, err := st.CreateStudent(ctx, "Другой")
	require.NoError(t, err)
	require.NoError(t, st.LinkContact(ctx, s1.ID, u.ID, "Отец"))
	require.NoError(t, st.LinkContact(ctx, s2.ID, u.ID, "Отец"))

	cb := &tgbotapi.CallbackQuery{
		ID:      "cb1",
		From:    &tgbotapi.User{ID: 999},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 999}, MessageID: 1},
		Data:    cbStopRemove + ":" + itoa(u.ID),
	}
	h.handleCallback(ctx, cb)

	students, err := st.GetStudentsByContact(ctx, u.ID)
	require.NoError(t, err)
	assert.Empty(t, students, "родитель должен быть отвязан от всех учеников")
}

// TestCallback_PickStudent_NotifiesContacts — привязка события к ученику
// (cbPickStudent) должна уведомить всех контактов ученика о назначенном
// времени занятия, используя реальный Start из календаря. Текст сообщения
// умышленно не включает Summary события — название в Google Calendar
// заполняет преподаватель для себя, родителю нужно только время.
func TestCallback_PickStudent_NotifiesContacts(t *testing.T) {
	cfg := &config.Config{TeacherTelegramID: 999, GoogleCalendarID: "primary"}
	st := newTestStore(t)
	sender := &fakeBotSender{}
	dispatch := notify.NewDispatcher(st, sender)
	cal := &fakeCalendarForBot{masters: []calendar.Event{
		{ID: "master-1", Summary: "Сергей занятие",
			Start: time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC),
			End:   time.Date(2026, 9, 20, 11, 0, 0, 0, time.UTC)},
	}}
	h := NewHandler(HandlerDeps{
		API: &fakeTelegramAPI{}, Cfg: cfg, Store: st, AdminUI: admin.Noop{},
		Dispatch: dispatch, CalClient: cal,
	})
	ctx := context.Background()

	u, err := st.CreateUser(ctx, "Вася Пупкин")
	require.NoError(t, err)
	require.NoError(t, st.SaveAccount(ctx, u.ID, MessengerName, "1", "vasya"))
	s, err := st.CreateStudent(ctx, "Сергей")
	require.NoError(t, err)
	require.NoError(t, st.LinkContact(ctx, s.ID, u.ID, "Отец"))

	token := h.eventTokens.tokenFor("master-1")
	cb := &tgbotapi.CallbackQuery{
		ID:      "cb1",
		From:    &tgbotapi.User{ID: 999},
		Message: &tgbotapi.Message{Chat: &tgbotapi.Chat{ID: 999}, MessageID: 1},
		Data:    cbPickStudent + ":" + token + ":" + itoa(s.ID),
	}
	h.handleCallback(ctx, cb)

	require.Len(t, sender.sent, 1, "родитель должен получить ровно одно уведомление")
	assert.Equal(t, "1", sender.sent[0].ExternalID)
	assert.Contains(t, sender.sent[0].Text, "Сергей")
	assert.Contains(t, sender.sent[0].Text, "назначено")
	assert.NotContains(t, sender.sent[0].Text, "Сергей занятие",
		"Summary события из календаря не должен попадать в сообщение родителю")
}

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}
