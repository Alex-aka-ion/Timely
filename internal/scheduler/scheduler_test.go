package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/booking-bot/booking-bot/internal/calendar"
	"github.com/booking-bot/booking-bot/internal/config"
	"github.com/booking-bot/booking-bot/internal/notify"
	"github.com/booking-bot/booking-bot/internal/store"
)

// fakeCalendar — реализация calendar.Client для тестов.
type fakeCalendar struct {
	instances []calendar.Instance
	err       error
}

func (f *fakeCalendar) UpcomingMasters(context.Context, string, time.Duration) ([]calendar.Event, error) {
	return nil, nil
}
func (f *fakeCalendar) UpcomingInstances(context.Context, string, time.Time, time.Time) ([]calendar.Instance, error) {
	return f.instances, f.err
}
func (f *fakeCalendar) UpdateSummary(context.Context, string, string, string) error {
	return nil
}

// fakeSender регистрирует, кому что отправлено.
type fakeSender struct {
	mu   sync.Mutex
	name string
	sent map[string]int // external_id → count
	err  error
}

func newFakeSender(name string) *fakeSender {
	return &fakeSender{name: name, sent: make(map[string]int)}
}
func (f *fakeSender) Messenger() string { return f.name }
func (f *fakeSender) Send(_ context.Context, externalID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent[externalID]++
	return nil
}
func (f *fakeSender) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, v := range f.sent {
		n += v
	}
	return n
}

// setup создаёт типовое состояние БД с одним учеником, контактом и привязкой события.
type setupResult struct {
	store    *store.SQLiteStore
	user     store.User
	student  store.Student
	masterID string
	dispatch *notify.Dispatcher
	sender   *fakeSender
}

func setup(t *testing.T) setupResult {
	t.Helper()
	s, err := store.NewSQLiteInMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	ctx := context.Background()
	u, err := s.CreateUser(ctx, "Мама")
	require.NoError(t, err)
	require.NoError(t, s.SaveAccount(ctx, u.ID, "telegram", "tg-1", "mama"))

	st, err := s.CreateStudent(ctx, "Петя")
	require.NoError(t, err)
	require.NoError(t, s.LinkContact(ctx, st.ID, u.ID, "Мама"))

	masterID := "master-1"
	require.NoError(t, s.LinkEvent(ctx, masterID, st.ID))

	sender := newFakeSender("telegram")
	dispatch := notify.NewDispatcher(s, sender)

	return setupResult{store: s, user: u, student: st, masterID: masterID, dispatch: dispatch, sender: sender}
}

func makeScheduler(t *testing.T, r setupResult, cal calendar.Client, now time.Time) *Scheduler {
	t.Helper()
	cfg := &config.Config{
		ReminderIntervals: []time.Duration{2 * time.Hour, 24 * time.Hour},
		SchedulerTick:     5 * time.Minute,
		GoogleCalendarID:  "primary",
	}
	sc := New(Deps{Cfg: cfg, Store: r.store, CalClient: cal, Dispatcher: r.dispatch})
	sc.nowFunc = func() time.Time { return now }
	return sc
}

// 1. Отправляет напоминание в нужном временном окне.
func TestScheduler_SendsInWindow(t *testing.T) {
	r := setup(t)
	now := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	// Событие через 2h ровно — должно сработать на интервале 2h.
	cal := &fakeCalendar{instances: []calendar.Instance{
		{ID: "inst-1", MasterID: r.masterID, Summary: "Занятие",
			Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour),
			Status: calendar.StatusConfirmed},
	}}
	sc := makeScheduler(t, r, cal, now)
	sc.runOnce(context.Background())

	assert.Equal(t, 1, r.sender.total(), "должно быть 1 напоминание")
}

// 2. Пропускает отменённые события.
func TestScheduler_SkipsCancelled(t *testing.T) {
	r := setup(t)
	now := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	cal := &fakeCalendar{instances: []calendar.Instance{
		{ID: "inst-1", MasterID: r.masterID,
			Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour),
			Status: calendar.StatusCancelled},
	}}
	sc := makeScheduler(t, r, cal, now)
	sc.runOnce(context.Background())
	assert.Equal(t, 0, r.sender.total())
}

// 3. Пропускает события без привязанного ученика.
func TestScheduler_SkipsUnlinked(t *testing.T) {
	r := setup(t)
	now := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	cal := &fakeCalendar{instances: []calendar.Instance{
		{ID: "inst-1", MasterID: "no-such-master",
			Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour),
			Status: calendar.StatusConfirmed},
	}}
	sc := makeScheduler(t, r, cal, now)
	sc.runOnce(context.Background())
	assert.Equal(t, 0, r.sender.total())
}

// 4. Не отправляет повторно.
func TestScheduler_Dedup(t *testing.T) {
	r := setup(t)
	now := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	cal := &fakeCalendar{instances: []calendar.Instance{
		{ID: "inst-1", MasterID: r.masterID,
			Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour),
			Status: calendar.StatusConfirmed},
	}}
	sc := makeScheduler(t, r, cal, now)
	sc.runOnce(context.Background())
	sc.runOnce(context.Background()) // второй раз — не должен отправить
	assert.Equal(t, 1, r.sender.total())
}

// 5. Использует интервалы ученика, если заданы.
func TestScheduler_UsesStudentIntervals(t *testing.T) {
	r := setup(t)
	now := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)

	// Глобальные: 2h, 24h. Для ученика устанавливаем только 30m.
	require.NoError(t, r.store.SetStudentIntervals(context.Background(), r.student.ID, "30m"))

	// Событие через 2h — глобально совпало бы, но индивидуально нет.
	cal := &fakeCalendar{instances: []calendar.Instance{
		{ID: "inst-1", MasterID: r.masterID,
			Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour),
			Status: calendar.StatusConfirmed},
	}}
	sc := makeScheduler(t, r, cal, now)
	sc.runOnce(context.Background())
	assert.Equal(t, 0, r.sender.total(), "глобальный 2h не должен срабатывать для ученика")

	// А через 30m — должно сработать.
	cal.instances[0].Start = now.Add(30 * time.Minute)
	cal.instances[0].End = now.Add(time.Hour + 30*time.Minute)
	sc.runOnce(context.Background())
	assert.Equal(t, 1, r.sender.total())
}

// 6. При ошибке Sender планировщик продолжает работу.
func TestScheduler_SenderErrorContinues(t *testing.T) {
	r := setup(t)
	r.sender.err = errors.New("boom")
	now := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	cal := &fakeCalendar{instances: []calendar.Instance{
		{ID: "inst-1", MasterID: r.masterID,
			Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour),
			Status: calendar.StatusConfirmed},
	}}
	sc := makeScheduler(t, r, cal, now)

	// Не должно паниковать.
	sc.runOnce(context.Background())

	// Не помечено как отправленное — поэтому при следующей попытке снова попробуем.
	sent, err := r.store.ReminderSent(context.Background(), "inst-1", r.user.ID, "2h0m0s")
	require.NoError(t, err)
	assert.False(t, sent)
}

// 7. Несколько контактов получают напоминания независимо.
func TestScheduler_MultipleContacts(t *testing.T) {
	r := setup(t)
	ctx := context.Background()
	u2, err := r.store.CreateUser(ctx, "Папа")
	require.NoError(t, err)
	require.NoError(t, r.store.SaveAccount(ctx, u2.ID, "telegram", "tg-2", "papa"))
	require.NoError(t, r.store.LinkContact(ctx, r.student.ID, u2.ID, "Папа"))

	now := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	cal := &fakeCalendar{instances: []calendar.Instance{
		{ID: "inst-1", MasterID: r.masterID,
			Start: now.Add(2 * time.Hour), End: now.Add(3 * time.Hour),
			Status: calendar.StatusConfirmed},
	}}
	sc := makeScheduler(t, r, cal, now)
	sc.runOnce(ctx)
	assert.Equal(t, 2, r.sender.total())
}

// 8. Окно ±5 мин — событие чуть в прошлом не должно повторно срабатывать.
func TestScheduler_WindowBoundary(t *testing.T) {
	r := setup(t)
	now := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	// Событие через 2h + 6 минут — не попадает в окно ±5 мин.
	cal := &fakeCalendar{instances: []calendar.Instance{
		{ID: "inst-1", MasterID: r.masterID,
			Start: now.Add(2*time.Hour + 6*time.Minute),
			End:   now.Add(3 * time.Hour),
			Status: calendar.StatusConfirmed},
	}}
	sc := makeScheduler(t, r, cal, now)
	sc.runOnce(context.Background())
	assert.Equal(t, 0, r.sender.total())
}

// 9. Глобальные интервалы используются если у ученика не заданы.
func TestScheduler_FallsBackToGlobal(t *testing.T) {
	r := setup(t)
	now := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)

	// Меняем глобальные на 1h.
	require.NoError(t, r.store.SetSetting(context.Background(), "reminder_intervals", "1h"))

	cal := &fakeCalendar{instances: []calendar.Instance{
		{ID: "inst-1", MasterID: r.masterID,
			Start:  now.Add(time.Hour),
			End:    now.Add(2 * time.Hour),
			Status: calendar.StatusConfirmed},
	}}
	sc := makeScheduler(t, r, cal, now)
	sc.runOnce(context.Background())
	assert.Equal(t, 1, r.sender.total())
}

func TestHumanInterval(t *testing.T) {
	tests := map[time.Duration]string{
		24 * time.Hour:     "1 дн",
		2 * time.Hour:      "2 ч",
		30 * time.Minute:   "30 мин",
		48 * time.Hour:     "2 дн",
	}
	for d, want := range tests {
		assert.Equal(t, want, humanInterval(d))
	}
}
