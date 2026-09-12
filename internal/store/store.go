// Package store определяет интерфейс хранилища и доменные типы.
//
// Бизнес-логика работает только с интерфейсом Store. Реализация SQLite
// в sqlite.go; в тестах используется либо мок, либо :memory:.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound возвращается когда запрашиваемая запись не существует.
var ErrNotFound = errors.New("store: запись не найдена")

// User — абстрактный пользователь системы (родитель или преподаватель,
// хотя роль преподавателя определяется по telegram_id из конфига).
type User struct {
	ID        int64
	FullName  string
	CreatedAt time.Time
}

// MessengerAccount — аккаунт пользователя в конкретном мессенджере.
type MessengerAccount struct {
	ID         int64
	UserID     int64
	Messenger  string
	ExternalID string
	Username   string
	IsActive   bool
	CreatedAt  time.Time
}

// Student — ученик. Создаётся только преподавателем.
type Student struct {
	ID                int64
	DisplayName       string
	Notes             string
	ReminderIntervals string // NULL/пусто = использовать глобальные
	CreatedAt         time.Time
}

// Contact — родитель ученика, как видится из связи student_contacts.
type Contact struct {
	UserID   int64
	FullName string
	Label    string
	AddedAt  time.Time
}

// EventLink — привязка мастер-события Calendar к ученику.
type EventLink struct {
	MasterEventID string
	StudentID     int64
	AddedAt       time.Time
}

// Store — единая точка доступа к хранилищу.
//
// Все операции принимают context для возможности отмены / таймаутов.
// У каждой привязки есть симметричная отвязка.
type Store interface {
	// users + messenger_accounts ------------------------------------------

	// CreateUser создаёт нового пользователя и возвращает его с заполненным ID.
	CreateUser(ctx context.Context, fullName string) (User, error)

	// GetUserByAccount находит пользователя по аккаунту мессенджера.
	// Возвращает ErrNotFound если аккаунт не зарегистрирован.
	GetUserByAccount(ctx context.Context, messenger, externalID string) (User, error)

	// SaveAccount создаёт новый аккаунт или активирует существующий
	// (если пользователь когда-то деактивировал и теперь регистрируется снова).
	SaveAccount(ctx context.Context, userID int64, messenger, externalID, username string) error

	// GetActiveAccounts возвращает все активные аккаунты пользователя
	// (по всем мессенджерам).
	GetActiveAccounts(ctx context.Context, userID int64) ([]MessengerAccount, error)

	// DeactivateAccount помечает аккаунт неактивным (мы не удаляем — для аудита).
	DeactivateAccount(ctx context.Context, messenger, externalID string) error

	// GetUnlinkedUsers возвращает зарегистрированных пользователей,
	// которые ещё не привязаны ни к одному ученику.
	GetUnlinkedUsers(ctx context.Context) ([]User, error)

	// students -------------------------------------------------------------

	CreateStudent(ctx context.Context, name string) (Student, error)
	GetStudents(ctx context.Context) ([]Student, error)

	// GetStudentForEvent возвращает ученика по master_event_id.
	// ErrNotFound если событие не привязано.
	GetStudentForEvent(ctx context.Context, masterEventID string) (Student, error)

	// GetStudentContacts возвращает всех родителей ученика.
	GetStudentContacts(ctx context.Context, studentID int64) ([]Contact, error)

	// SetStudentIntervals устанавливает индивидуальные интервалы напоминаний.
	// Пустая строка = вернуть на глобальные настройки.
	SetStudentIntervals(ctx context.Context, studentID int64, intervals string) error

	// contacts (симметрично) ----------------------------------------------

	LinkContact(ctx context.Context, studentID, userID int64, label string) error
	UnlinkContact(ctx context.Context, studentID, userID int64) error

	// events (симметрично) ------------------------------------------------

	LinkEvent(ctx context.Context, masterEventID string, studentID int64) error
	UnlinkEvent(ctx context.Context, masterEventID string) error

	// reminders (дедупликация) --------------------------------------------

	// ReminderSent возвращает true если напоминание уже отправлялось.
	ReminderSent(ctx context.Context, instanceID string, userID int64, reminderType string) (bool, error)
	MarkReminderSent(ctx context.Context, instanceID string, userID int64, reminderType string) error

	// settings -------------------------------------------------------------

	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error

	// Lifecycle ------------------------------------------------------------

	// Close закрывает соединение с БД.
	Close() error
}
