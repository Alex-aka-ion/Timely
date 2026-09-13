package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/sqlite3"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/mattn/go-sqlite3"

	"github.com/booking-bot/booking-bot/internal/store/migrations"
)

// SQLiteStore — реализация Store на SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLite открывает базу по пути dbPath, включает обязательные PRAGMA
// и применяет все непримененные миграции.
//
// PRAGMA foreign_keys=ON — включить обязательно, по умолчанию выключено.
// PRAGMA journal_mode=WAL — защита от потери данных при аварии.
func NewSQLite(dbPath string) (*SQLiteStore, error) {
	// _foreign_keys=on — заставляет sqlite3 драйвер включать FK для каждого подключения.
	// _journal_mode=WAL — режим журнала.
	dsn := dbPath + "?_foreign_keys=on&_journal_mode=WAL&_busy_timeout=5000"
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("открытие БД: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping БД: %w", err)
	}

	// Дополнительная страховка: явно выставляем PRAGMA на текущем подключении.
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("PRAGMA foreign_keys: %w", err)
	}
	if _, err := db.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("PRAGMA journal_mode: %w", err)
	}

	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("миграции: %w", err)
	}

	return &SQLiteStore{db: db}, nil
}

// NewSQLiteInMemory — для тестов: открывает БД в памяти и применяет миграции.
func NewSQLiteInMemory() (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", ":memory:?_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := runMigrations(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

// Close закрывает соединение с БД.
func (s *SQLiteStore) Close() error { return s.db.Close() }

func runMigrations(db *sql.DB) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("источник миграций: %w", err)
	}
	driver, err := sqlite3.WithInstance(db, &sqlite3.Config{})
	if err != nil {
		return fmt.Errorf("драйвер миграций: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", src, "sqlite3", driver)
	if err != nil {
		return fmt.Errorf("инициализация migrate: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("применение миграций: %w", err)
	}
	return nil
}

// --- users + messenger_accounts ---------------------------------------------

// CreateUser создаёт нового пользователя.
func (s *SQLiteStore) CreateUser(ctx context.Context, fullName string) (User, error) {
	fullName = strings.TrimSpace(fullName)
	if fullName == "" {
		return User{}, errors.New("full_name пуст")
	}
	if len(fullName) > 100 {
		// Защита от инъекции через имя — ограничение длины.
		fullName = fullName[:100]
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO users (full_name) VALUES (?)`, fullName)
	if err != nil {
		return User{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return User{}, err
	}
	return s.getUser(ctx, id)
}

// UpdateUserName меняет ФИО существующего пользователя. В отличие от
// CreateUser, не создаёт новую запись — только обновляет full_name у уже
// зарегистрированного (userID приходит из GetUserByAccount, так что к
// моменту вызова пользователь заведомо существует, но ErrNotFound на
// RowsAffected()==0 всё равно возвращаем — на случай гонки/чужого ID).
func (s *SQLiteStore) UpdateUserName(ctx context.Context, userID int64, fullName string) error {
	fullName = strings.TrimSpace(fullName)
	if fullName == "" {
		return errors.New("full_name пуст")
	}
	if len(fullName) > 100 {
		fullName = fullName[:100]
	}
	res, err := s.db.ExecContext(ctx, `UPDATE users SET full_name = ? WHERE id = ?`, fullName, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) getUser(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx,
		`SELECT id, full_name, created_at FROM users WHERE id = ?`, id,
	).Scan(&u.ID, &u.FullName, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// GetUserByAccount находит пользователя по активному аккаунту мессенджера.
func (s *SQLiteStore) GetUserByAccount(ctx context.Context, messenger, externalID string) (User, error) {
	var u User
	err := s.db.QueryRowContext(ctx, `
		SELECT u.id, u.full_name, u.created_at
		FROM users u
		JOIN messenger_accounts ma ON ma.user_id = u.id
		WHERE ma.messenger = ? AND ma.external_id = ? AND ma.is_active = 1
	`, messenger, externalID).Scan(&u.ID, &u.FullName, &u.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

// SaveAccount создаёт новый аккаунт или активирует существующий.
func (s *SQLiteStore) SaveAccount(ctx context.Context, userID int64, messenger, externalID, username string) error {
	// UPSERT: при конфликте (messenger, external_id) обновляем user_id, username и активируем.
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messenger_accounts (user_id, messenger, external_id, username, is_active)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(messenger, external_id) DO UPDATE SET
			user_id = excluded.user_id,
			username = excluded.username,
			is_active = 1
	`, userID, messenger, externalID, username)
	return err
}

// GetActiveAccounts возвращает все активные аккаунты пользователя.
func (s *SQLiteStore) GetActiveAccounts(ctx context.Context, userID int64) ([]MessengerAccount, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, messenger, external_id, COALESCE(username, ''), is_active, created_at
		FROM messenger_accounts
		WHERE user_id = ? AND is_active = 1
		ORDER BY id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessengerAccount
	for rows.Next() {
		var a MessengerAccount
		var active int
		if err := rows.Scan(&a.ID, &a.UserID, &a.Messenger, &a.ExternalID, &a.Username, &active, &a.CreatedAt); err != nil {
			return nil, err
		}
		a.IsActive = active != 0
		out = append(out, a)
	}
	return out, rows.Err()
}

// DeactivateAccount помечает аккаунт неактивным.
func (s *SQLiteStore) DeactivateAccount(ctx context.Context, messenger, externalID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE messenger_accounts SET is_active = 0 WHERE messenger = ? AND external_id = ?`,
		messenger, externalID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetUnlinkedUsers возвращает пользователей не привязанных ни к одному ученику.
func (s *SQLiteStore) GetUnlinkedUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.full_name, u.created_at
		FROM users u
		WHERE NOT EXISTS (SELECT 1 FROM student_contacts sc WHERE sc.user_id = u.id)
		ORDER BY u.created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.FullName, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// --- students ----------------------------------------------------------------

func (s *SQLiteStore) CreateStudent(ctx context.Context, name string) (Student, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Student{}, errors.New("display_name пуст")
	}
	if len(name) > 100 {
		name = name[:100]
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO students (display_name) VALUES (?)`, name)
	if err != nil {
		return Student{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Student{}, err
	}
	return s.getStudent(ctx, id)
}

func (s *SQLiteStore) getStudent(ctx context.Context, id int64) (Student, error) {
	var st Student
	var notes, intervals sql.NullString
	err := s.db.QueryRowContext(ctx,
		`SELECT id, display_name, notes, reminder_intervals, created_at FROM students WHERE id = ?`, id,
	).Scan(&st.ID, &st.DisplayName, &notes, &intervals, &st.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Student{}, ErrNotFound
	}
	if err != nil {
		return Student{}, err
	}
	st.Notes = notes.String
	st.ReminderIntervals = intervals.String
	return st, nil
}

func (s *SQLiteStore) GetStudents(ctx context.Context) ([]Student, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, display_name, COALESCE(notes,''), COALESCE(reminder_intervals,''), created_at
		 FROM students ORDER BY display_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Student
	for rows.Next() {
		var st Student
		if err := rows.Scan(&st.ID, &st.DisplayName, &st.Notes, &st.ReminderIntervals, &st.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// GetUnlinkedStudents возвращает учеников без единого привязанного контакта
// (student_contacts) — см. комментарий у интерфейса в store.go.
func (s *SQLiteStore) GetUnlinkedStudents(ctx context.Context) ([]Student, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, display_name, COALESCE(notes,''), COALESCE(reminder_intervals,''), created_at
		FROM students st
		WHERE NOT EXISTS (SELECT 1 FROM student_contacts sc WHERE sc.student_id = st.id)
		ORDER BY display_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Student
	for rows.Next() {
		var st Student
		if err := rows.Scan(&st.ID, &st.DisplayName, &st.Notes, &st.ReminderIntervals, &st.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetAllUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, full_name, created_at
		FROM users
		ORDER BY full_name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.FullName, &u.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetStudentForEvent(ctx context.Context, masterEventID string) (Student, error) {
	var st Student
	var notes, intervals sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT s.id, s.display_name, s.notes, s.reminder_intervals, s.created_at
		FROM students s
		JOIN event_students es ON es.student_id = s.id
		WHERE es.master_event_id = ?
	`, masterEventID).Scan(&st.ID, &st.DisplayName, &notes, &intervals, &st.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Student{}, ErrNotFound
	}
	if err != nil {
		return Student{}, err
	}
	st.Notes = notes.String
	st.ReminderIntervals = intervals.String
	return st, nil
}

func (s *SQLiteStore) GetStudentEvents(ctx context.Context, studentID int64) ([]EventLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT master_event_id, student_id, added_at
		FROM event_students
		WHERE student_id = ?
		ORDER BY added_at
	`, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []EventLink
	for rows.Next() {
		var el EventLink
		if err := rows.Scan(&el.MasterEventID, &el.StudentID, &el.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, el)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetStudentContacts(ctx context.Context, studentID int64) ([]Contact, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT u.id, u.full_name, COALESCE(sc.label, ''), sc.added_at
		FROM student_contacts sc
		JOIN users u ON u.id = sc.user_id
		WHERE sc.student_id = ?
		ORDER BY sc.added_at
	`, studentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.UserID, &c.FullName, &c.Label, &c.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) GetStudentsByContact(ctx context.Context, userID int64) ([]Student, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT s.id, s.display_name, COALESCE(s.notes,''), COALESCE(s.reminder_intervals,''), s.created_at
		FROM students s
		JOIN student_contacts sc ON sc.student_id = s.id
		WHERE sc.user_id = ?
		ORDER BY s.display_name
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Student
	for rows.Next() {
		var st Student
		if err := rows.Scan(&st.ID, &st.DisplayName, &st.Notes, &st.ReminderIntervals, &st.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// UpdateStudentName переименовывает ученика — та же валидация (trim/пустая
// строка/лимит 100 символов), что и в CreateStudent при создании.
func (s *SQLiteStore) UpdateStudentName(ctx context.Context, studentID int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("display_name пуст")
	}
	if len(name) > 100 {
		name = name[:100]
	}
	res, err := s.db.ExecContext(ctx, `UPDATE students SET display_name = ? WHERE id = ?`, name, studentID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) SetStudentIntervals(ctx context.Context, studentID int64, intervals string) error {
	intervals = strings.TrimSpace(intervals)
	var arg any
	if intervals == "" {
		arg = nil // вернуть на глобальные
	} else {
		arg = intervals
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE students SET reminder_intervals = ? WHERE id = ?`, arg, studentID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- contacts (симметрично) -------------------------------------------------

func (s *SQLiteStore) LinkContact(ctx context.Context, studentID, userID int64, label string) error {
	label = strings.TrimSpace(label)
	if len(label) > 100 {
		label = label[:100]
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO student_contacts (student_id, user_id, label) VALUES (?, ?, ?)
		ON CONFLICT(student_id, user_id) DO UPDATE SET label = excluded.label
	`, studentID, userID, label)
	return err
}

func (s *SQLiteStore) UnlinkContact(ctx context.Context, studentID, userID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM student_contacts WHERE student_id = ? AND user_id = ?`,
		studentID, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- events (симметрично) ---------------------------------------------------

func (s *SQLiteStore) LinkEvent(ctx context.Context, masterEventID string, studentID int64) error {
	if strings.TrimSpace(masterEventID) == "" {
		return errors.New("master_event_id пуст")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO event_students (master_event_id, student_id) VALUES (?, ?)
		ON CONFLICT(master_event_id) DO UPDATE SET student_id = excluded.student_id
	`, masterEventID, studentID)
	return err
}

func (s *SQLiteStore) UnlinkEvent(ctx context.Context, masterEventID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM event_students WHERE master_event_id = ?`, masterEventID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- reminders --------------------------------------------------------------

func (s *SQLiteStore) ReminderSent(ctx context.Context, instanceID string, userID int64, reminderType string) (bool, error) {
	var dummy int
	err := s.db.QueryRowContext(ctx, `
		SELECT 1 FROM sent_reminders
		WHERE instance_event_id = ? AND user_id = ? AND reminder_type = ?
	`, instanceID, userID, reminderType).Scan(&dummy)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *SQLiteStore) MarkReminderSent(ctx context.Context, instanceID string, userID int64, reminderType string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sent_reminders (instance_event_id, user_id, reminder_type, sent_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT DO NOTHING
	`, instanceID, userID, reminderType, time.Now().UTC())
	return err
}

func (s *SQLiteStore) ClearRemindersForInstance(ctx context.Context, instanceEventID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM sent_reminders WHERE instance_event_id = ?`, instanceEventID)
	return err
}

// --- event state (для уведомлений об изменениях) ----------------------------

func (s *SQLiteStore) GetEventState(ctx context.Context, instanceEventID string) (EventState, error) {
	var st EventState
	var notified int
	err := s.db.QueryRowContext(ctx, `
		SELECT instance_event_id, start_time, notified_cancelled
		FROM event_state WHERE instance_event_id = ?
	`, instanceEventID).Scan(&st.InstanceEventID, &st.Start, &notified)
	if errors.Is(err, sql.ErrNoRows) {
		return EventState{}, ErrNotFound
	}
	if err != nil {
		return EventState{}, err
	}
	st.NotifiedCancelled = notified != 0
	return st, nil
}

// SaveEventState — upsert: сбрасывает notified_cancelled в 0, так что если
// "отменённое" событие вдруг снова стало активным (Google это позволяет —
// восстановление из корзины Calendar в течение некоторого времени), новая
// отмена в будущем снова будет замечена и разослана.
func (s *SQLiteStore) SaveEventState(ctx context.Context, instanceEventID string, start time.Time) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO event_state (instance_event_id, start_time, notified_cancelled, updated_at)
		VALUES (?, ?, 0, ?)
		ON CONFLICT(instance_event_id) DO UPDATE SET
			start_time = excluded.start_time,
			notified_cancelled = 0,
			updated_at = excluded.updated_at
	`, instanceEventID, start.UTC(), time.Now().UTC())
	return err
}

func (s *SQLiteStore) MarkEventCancelled(ctx context.Context, instanceEventID string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE event_state SET notified_cancelled = 1, updated_at = ? WHERE instance_event_id = ?`,
		time.Now().UTC(), instanceEventID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- settings ---------------------------------------------------------------

func (s *SQLiteStore) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

func (s *SQLiteStore) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}
