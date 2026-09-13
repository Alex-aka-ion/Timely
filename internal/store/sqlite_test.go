package store

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := NewSQLiteInMemory()
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestMigrationsApplied(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// Глобальная настройка reminder_intervals=24h,2h должна быть после миграции.
	v, err := s.GetSetting(ctx, "reminder_intervals")
	require.NoError(t, err)
	assert.Equal(t, "24h,2h", v)

	// Все таблицы должны существовать.
	tables := []string{"users", "messenger_accounts", "students",
		"student_contacts", "event_students", "sent_reminders", "settings"}
	for _, name := range tables {
		var got string
		err := s.db.QueryRowContext(ctx,
			`SELECT name FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&got)
		require.NoErrorf(t, err, "таблица %s должна существовать", name)
	}
}

func TestUsersAndAccounts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "Иван Иванов")
	require.NoError(t, err)
	assert.NotZero(t, u.ID)
	assert.Equal(t, "Иван Иванов", u.FullName)

	require.NoError(t, s.SaveAccount(ctx, u.ID, "telegram", "111", "ivanov"))

	got, err := s.GetUserByAccount(ctx, "telegram", "111")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)

	accs, err := s.GetActiveAccounts(ctx, u.ID)
	require.NoError(t, err)
	require.Len(t, accs, 1)
	assert.True(t, accs[0].IsActive)
}

func TestUniqueAccount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	a, err := s.CreateUser(ctx, "A")
	require.NoError(t, err)
	b, err := s.CreateUser(ctx, "B")
	require.NoError(t, err)

	require.NoError(t, s.SaveAccount(ctx, a.ID, "telegram", "111", "a"))
	// При повторной регистрации тот же external_id переходит к новому user_id.
	require.NoError(t, s.SaveAccount(ctx, b.ID, "telegram", "111", "b"))

	got, err := s.GetUserByAccount(ctx, "telegram", "111")
	require.NoError(t, err)
	assert.Equal(t, b.ID, got.ID)
}

func TestDeactivateAccount(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, _ := s.CreateUser(ctx, "U")
	_ = s.SaveAccount(ctx, u.ID, "telegram", "1", "u")

	require.NoError(t, s.DeactivateAccount(ctx, "telegram", "1"))
	_, err := s.GetUserByAccount(ctx, "telegram", "1")
	assert.ErrorIs(t, err, ErrNotFound)

	// Повторная регистрация активирует обратно.
	require.NoError(t, s.SaveAccount(ctx, u.ID, "telegram", "1", "u"))
	got, err := s.GetUserByAccount(ctx, "telegram", "1")
	require.NoError(t, err)
	assert.Equal(t, u.ID, got.ID)
}

func TestUpdateUserName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "Старое Имя")
	require.NoError(t, err)

	require.NoError(t, s.UpdateUserName(ctx, u.ID, "  Новое Имя  "))
	got, err := s.getUser(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "Новое Имя", got.FullName, "пробелы должны обрезаться, как и в CreateUser")

	// Несуществующий пользователь — ErrNotFound, а не тихий no-op.
	err = s.UpdateUserName(ctx, 999999, "Кто-то")
	assert.ErrorIs(t, err, ErrNotFound)

	// Пустое имя отклоняется, как и в CreateUser.
	assert.Error(t, s.UpdateUserName(ctx, u.ID, "   "))
}

// TestUpdateUserName_ReflectsInStudentContacts — GetStudentContacts делает
// живой JOIN на users.full_name, а не хранит отдельную копию имени, так что
// обновление имени родителем сразу видно преподавателю без какой-либо
// дополнительной синхронизации.
func TestUpdateUserName_ReflectsInStudentContacts(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, err := s.CreateUser(ctx, "Вася Пупкин")
	require.NoError(t, err)
	st, err := s.CreateStudent(ctx, "Сергей")
	require.NoError(t, err)
	require.NoError(t, s.LinkContact(ctx, st.ID, u.ID, "Отец"))

	require.NoError(t, s.UpdateUserName(ctx, u.ID, "Василий Пупкин"))

	contacts, err := s.GetStudentContacts(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, contacts, 1)
	assert.Equal(t, "Василий Пупкин", contacts[0].FullName)
}

func TestForeignKeysEnforced(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	st, err := s.CreateStudent(ctx, "Петя")
	require.NoError(t, err)

	// Несуществующий user_id — должна быть ошибка FK.
	err = s.LinkContact(ctx, st.ID, 9999, "Мама")
	assert.Error(t, err)
}

func TestStudentContactsLinkUnlink(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u1, _ := s.CreateUser(ctx, "Мама")
	u2, _ := s.CreateUser(ctx, "Папа")
	st, _ := s.CreateStudent(ctx, "Петя")

	require.NoError(t, s.LinkContact(ctx, st.ID, u1.ID, "Мама"))
	require.NoError(t, s.LinkContact(ctx, st.ID, u2.ID, "Папа"))

	contacts, err := s.GetStudentContacts(ctx, st.ID)
	require.NoError(t, err)
	assert.Len(t, contacts, 2)

	// Симметричная отвязка.
	require.NoError(t, s.UnlinkContact(ctx, st.ID, u1.ID))
	contacts, err = s.GetStudentContacts(ctx, st.ID)
	require.NoError(t, err)
	assert.Len(t, contacts, 1)
	assert.Equal(t, u2.ID, contacts[0].UserID)

	// Повторная отвязка несуществующей связи — ErrNotFound.
	err = s.UnlinkContact(ctx, st.ID, u1.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestEventLinkUnlink(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	st, _ := s.CreateStudent(ctx, "Петя")
	require.NoError(t, s.LinkEvent(ctx, "evt-1", st.ID))

	got, err := s.GetStudentForEvent(ctx, "evt-1")
	require.NoError(t, err)
	assert.Equal(t, st.ID, got.ID)

	require.NoError(t, s.UnlinkEvent(ctx, "evt-1"))
	_, err = s.GetStudentForEvent(ctx, "evt-1")
	assert.ErrorIs(t, err, ErrNotFound)

	// Повторная отвязка — ErrNotFound.
	err = s.UnlinkEvent(ctx, "evt-1")
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestGetStudentEvents — обратная выборка к GetStudentForEvent: у одного
// ученика может быть больше одного привязанного мастер-события (например,
// две разные повторяющиеся серии).
func TestGetStudentEvents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	st, _ := s.CreateStudent(ctx, "Петя")
	other, _ := s.CreateStudent(ctx, "Маша")

	links, err := s.GetStudentEvents(ctx, st.ID)
	require.NoError(t, err)
	assert.Empty(t, links)

	require.NoError(t, s.LinkEvent(ctx, "evt-1", st.ID))
	require.NoError(t, s.LinkEvent(ctx, "evt-2", st.ID))
	require.NoError(t, s.LinkEvent(ctx, "evt-3", other.ID))

	links, err = s.GetStudentEvents(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, links, 2)
	ids := []string{links[0].MasterEventID, links[1].MasterEventID}
	assert.ElementsMatch(t, []string{"evt-1", "evt-2"}, ids)

	require.NoError(t, s.UnlinkEvent(ctx, "evt-1"))
	links, err = s.GetStudentEvents(ctx, st.ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	assert.Equal(t, "evt-2", links[0].MasterEventID)
}

func TestReminderDedup(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u, _ := s.CreateUser(ctx, "U")

	sent, err := s.ReminderSent(ctx, "inst-1", u.ID, "24h")
	require.NoError(t, err)
	assert.False(t, sent)

	require.NoError(t, s.MarkReminderSent(ctx, "inst-1", u.ID, "24h"))

	sent, err = s.ReminderSent(ctx, "inst-1", u.ID, "24h")
	require.NoError(t, err)
	assert.True(t, sent)

	// Другой тип — ещё не отправлен.
	sent, err = s.ReminderSent(ctx, "inst-1", u.ID, "2h")
	require.NoError(t, err)
	assert.False(t, sent)

	// Повторный mark — без ошибки (ON CONFLICT DO NOTHING).
	require.NoError(t, s.MarkReminderSent(ctx, "inst-1", u.ID, "24h"))
}

func TestUnlinkedUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u1, _ := s.CreateUser(ctx, "Без ученика")
	u2, _ := s.CreateUser(ctx, "С учеником")
	st, _ := s.CreateStudent(ctx, "Петя")
	require.NoError(t, s.LinkContact(ctx, st.ID, u2.ID, "Папа"))

	users, err := s.GetUnlinkedUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, u1.ID, users[0].ID)
}

// TestGetAllUsers — в отличие от GetUnlinkedUsers, должен вернуть всех
// зарегистрированных пользователей, включая уже привязанных к ученику.
func TestGetAllUsers(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	u1, _ := s.CreateUser(ctx, "Без ученика")
	u2, _ := s.CreateUser(ctx, "С учеником")
	st, _ := s.CreateStudent(ctx, "Петя")
	require.NoError(t, s.LinkContact(ctx, st.ID, u2.ID, "Папа"))

	users, err := s.GetAllUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	ids := []int64{users[0].ID, users[1].ID}
	assert.ElementsMatch(t, []int64{u1.ID, u2.ID}, ids)
}

func TestGetUnlinkedStudents(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	stWithout, _ := s.CreateStudent(ctx, "Без родителя")
	stWith, _ := s.CreateStudent(ctx, "С родителем")
	u, _ := s.CreateUser(ctx, "Мама")
	require.NoError(t, s.LinkContact(ctx, stWith.ID, u.ID, "Мама"))

	students, err := s.GetUnlinkedStudents(ctx)
	require.NoError(t, err)
	require.Len(t, students, 1)
	assert.Equal(t, stWithout.ID, students[0].ID)

	// После отвязки контакта ученик снова должен появиться в списке.
	require.NoError(t, s.UnlinkContact(ctx, stWith.ID, u.ID))
	students, err = s.GetUnlinkedStudents(ctx)
	require.NoError(t, err)
	assert.Len(t, students, 2)
}

func TestStudentIntervals(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	st, _ := s.CreateStudent(ctx, "Петя")

	require.NoError(t, s.SetStudentIntervals(ctx, st.ID, "1h,15m"))
	got, err := s.getStudent(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, "1h,15m", got.ReminderIntervals)

	// Сброс на глобальные = пустая строка.
	require.NoError(t, s.SetStudentIntervals(ctx, st.ID, ""))
	got, err = s.getStudent(ctx, st.ID)
	require.NoError(t, err)
	assert.Empty(t, got.ReminderIntervals)

	// Несуществующий ученик.
	err = s.SetStudentIntervals(ctx, 9999, "1h")
	assert.True(t, errors.Is(err, ErrNotFound))
}

func TestUpdateStudentName(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	st, err := s.CreateStudent(ctx, "Петя")
	require.NoError(t, err)

	require.NoError(t, s.UpdateStudentName(ctx, st.ID, "  Пётр  "))
	got, err := s.getStudent(ctx, st.ID)
	require.NoError(t, err)
	assert.Equal(t, "Пётр", got.DisplayName, "пробелы должны обрезаться, как и в CreateStudent")

	// Несуществующий ученик — ErrNotFound.
	err = s.UpdateStudentName(ctx, 9999, "Кто-то")
	assert.True(t, errors.Is(err, ErrNotFound))

	// Пустое имя отклоняется, как и в CreateStudent.
	assert.Error(t, s.UpdateStudentName(ctx, st.ID, "   "))
}

func TestSettings(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	require.NoError(t, s.SetSetting(ctx, "key", "v1"))
	v, err := s.GetSetting(ctx, "key")
	require.NoError(t, err)
	assert.Equal(t, "v1", v)

	// Перезапись.
	require.NoError(t, s.SetSetting(ctx, "key", "v2"))
	v, _ = s.GetSetting(ctx, "key")
	assert.Equal(t, "v2", v)

	_, err = s.GetSetting(ctx, "missing")
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestGetStudentsByContact — обратная связь к GetStudentContacts: находим
// учеников по родителю, а не наоборот. Нужно для кнопки "Мои ученики" в
// Telegram-меню родителя.
func TestGetStudentsByContact(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	mama, _ := s.CreateUser(ctx, "Мама")
	papa, _ := s.CreateUser(ctx, "Папа")
	petya, _ := s.CreateStudent(ctx, "Петя")
	masha, _ := s.CreateStudent(ctx, "Маша")

	// У мамы — оба ребёнка, у папы — только Петя.
	require.NoError(t, s.LinkContact(ctx, petya.ID, mama.ID, "Мама"))
	require.NoError(t, s.LinkContact(ctx, masha.ID, mama.ID, "Мама"))
	require.NoError(t, s.LinkContact(ctx, petya.ID, papa.ID, "Папа"))

	got, err := s.GetStudentsByContact(ctx, mama.ID)
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, "Маша", got[0].DisplayName) // ORDER BY display_name
	assert.Equal(t, "Петя", got[1].DisplayName)

	got, err = s.GetStudentsByContact(ctx, papa.ID)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Петя", got[0].DisplayName)

	// Пользователь без единого привязанного ученика — пустой список, не ошибка.
	stranger, _ := s.CreateUser(ctx, "Посторонний")
	got, err = s.GetStudentsByContact(ctx, stranger.ID)
	require.NoError(t, err)
	assert.Empty(t, got)
}
