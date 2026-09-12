// Package calendar абстрагирует Google Calendar API.
//
// Только интерфейс Client используется бизнес-логикой — реализация
// (events.go) скрыта за ним, чтобы планировщик и бот можно было тестировать
// с моком и httptest.
package calendar

import (
	"context"
	"time"
)

// Status события — нас интересуют только confirmed/cancelled.
const (
	StatusConfirmed = "confirmed"
	StatusCancelled = "cancelled"
)

// Event — одно мастер-событие из Google Calendar.
type Event struct {
	ID      string // master event id
	Summary string
	Start   time.Time
	End     time.Time
}

// Instance — конкретный экземпляр события (для повторяющихся events).
// MasterID совпадает с ID для одиночных событий.
type Instance struct {
	ID       string // unique instance id
	MasterID string
	Summary  string
	Start    time.Time
	End      time.Time
	Status   string
}

// Client — единая точка работы с Google Calendar.
//
// Реализации:
//   - GoogleClient — реальный API
//   - в тестах: httptest-мок
type Client interface {
	// UpcomingMasters возвращает мастер-события за период [now, now + horizon].
	// Используется командой /events для показа событий без привязанного ученика.
	UpcomingMasters(ctx context.Context, calendarID string, horizon time.Duration) ([]Event, error)

	// UpcomingInstances возвращает все instances событий в окне [from, to].
	// Используется планировщиком — он развёртывает повторяющиеся события.
	UpcomingInstances(ctx context.Context, calendarID string, from, to time.Time) ([]Instance, error)

	// UpdateSummary меняет название события (опционально, при привязке ученика).
	UpdateSummary(ctx context.Context, calendarID, eventID, summary string) error
}
