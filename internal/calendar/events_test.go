package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	calapi "google.golang.org/api/calendar/v3"
)

// fakeCalendarServer возвращает заготовленный список событий по пути /calendars/.../events.
func fakeCalendarServer(t *testing.T, items []*calapi.Event) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/calendars/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := &calapi.Events{Items: items}
		_ = json.NewEncoder(w).Encode(resp)
	})
	return httptest.NewServer(mux)
}

func TestUpcomingInstances_FiltersCancelled(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Minute)
	items := []*calapi.Event{
		{
			Id:      "ev1",
			Summary: "Active",
			Status:  StatusConfirmed,
			Start:   &calapi.EventDateTime{DateTime: now.Add(time.Hour).Format(time.RFC3339)},
			End:     &calapi.EventDateTime{DateTime: now.Add(2 * time.Hour).Format(time.RFC3339)},
		},
		{
			Id:      "ev2",
			Summary: "Cancelled",
			Status:  StatusCancelled,
			Start:   &calapi.EventDateTime{DateTime: now.Add(time.Hour).Format(time.RFC3339)},
			End:     &calapi.EventDateTime{DateTime: now.Add(2 * time.Hour).Format(time.RFC3339)},
		},
		{
			// All-day event, нужно пропустить.
			Id:     "ev3",
			Status: StatusConfirmed,
			Start:  &calapi.EventDateTime{Date: "2026-01-01"},
			End:    &calapi.EventDateTime{Date: "2026-01-02"},
		},
	}
	srv := fakeCalendarServer(t, items)
	defer srv.Close()

	c, err := NewGoogleClientFromHTTP(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)

	// UpcomingInstances пропускает all-day и оставляет cancelled (статусом).
	instances, err := c.UpcomingInstances(context.Background(), "primary", now, now.Add(24*time.Hour))
	require.NoError(t, err)

	// Бизнес-логика планировщика будет сама пропускать cancelled.
	require.Len(t, instances, 2)

	var active, cancelled int
	for _, in := range instances {
		switch in.Status {
		case StatusConfirmed:
			active++
		case StatusCancelled:
			cancelled++
		}
	}
	assert.Equal(t, 1, active)
	assert.Equal(t, 1, cancelled)
}

func TestUpcomingMasters_SkipsCancelled(t *testing.T) {
	now := time.Now().UTC()
	items := []*calapi.Event{
		{
			Id: "m1", Summary: "M1", Status: StatusConfirmed,
			Start: &calapi.EventDateTime{DateTime: now.Add(time.Hour).Format(time.RFC3339)},
			End:   &calapi.EventDateTime{DateTime: now.Add(2 * time.Hour).Format(time.RFC3339)},
		},
		{
			Id: "m2", Summary: "M2", Status: StatusCancelled,
			Start: &calapi.EventDateTime{DateTime: now.Add(time.Hour).Format(time.RFC3339)},
			End:   &calapi.EventDateTime{DateTime: now.Add(2 * time.Hour).Format(time.RFC3339)},
		},
	}
	srv := fakeCalendarServer(t, items)
	defer srv.Close()

	c, err := NewGoogleClientFromHTTP(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)

	events, err := c.UpcomingMasters(context.Background(), "primary", 14*24*time.Hour)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, "m1", events[0].ID)
}

// fakeSingleEventServer имитирует ответ Events.Get: либо конкретное
// событие с кодом 200, либо ошибку (404/410 — событие удалено).
func fakeSingleEventServer(t *testing.T, status int, event *calapi.Event) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/calendars/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if status != http.StatusOK {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]any{"code": status, "message": "not found"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(event)
	})
	return httptest.NewServer(mux)
}

func TestGetEvent_Found(t *testing.T) {
	srv := fakeSingleEventServer(t, http.StatusOK, &calapi.Event{
		Id: "ev1", Summary: "Занятие", Status: StatusConfirmed,
		Start: &calapi.EventDateTime{DateTime: "2026-01-01T10:00:00Z"},
		End:   &calapi.EventDateTime{DateTime: "2026-01-01T11:00:00Z"},
	})
	defer srv.Close()
	c, err := NewGoogleClientFromHTTP(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)

	ev, err := c.GetEvent(context.Background(), "primary", "ev1")
	require.NoError(t, err)
	assert.Equal(t, "Занятие", ev.Summary)
}

// TestGetEvent_Cancelled — событие отменено целиком (не один instance
// повторяющейся серии) — трактуем как ErrEventNotFound, как и настоящее
// удаление: с точки зрения карточки ученика разница неважна, в обоих
// случаях привязку пора снимать.
func TestGetEvent_Cancelled(t *testing.T) {
	srv := fakeSingleEventServer(t, http.StatusOK, &calapi.Event{
		Id: "ev1", Status: StatusCancelled,
	})
	defer srv.Close()
	c, err := NewGoogleClientFromHTTP(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)

	_, err = c.GetEvent(context.Background(), "primary", "ev1")
	assert.ErrorIs(t, err, ErrEventNotFound)
}

// TestGetEvent_DeletedReturns404 — событие удалено из календаря вручную:
// Google отвечает 404 (или 410 для старых tombstone-записей), в обоих
// случаях GetEvent должен вернуть ErrEventNotFound, а не голую ошибку API.
func TestGetEvent_DeletedReturns404(t *testing.T) {
	srv := fakeSingleEventServer(t, http.StatusNotFound, nil)
	defer srv.Close()
	c, err := NewGoogleClientFromHTTP(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)

	_, err = c.GetEvent(context.Background(), "primary", "ev1")
	assert.ErrorIs(t, err, ErrEventNotFound)
}

func TestGetEvent_DeletedReturns410(t *testing.T) {
	srv := fakeSingleEventServer(t, http.StatusGone, nil)
	defer srv.Close()
	c, err := NewGoogleClientFromHTTP(context.Background(), srv.Client(), srv.URL)
	require.NoError(t, err)

	_, err = c.GetEvent(context.Background(), "primary", "ev1")
	assert.ErrorIs(t, err, ErrEventNotFound)
}

func TestParseTimes_AllDay(t *testing.T) {
	e := &calapi.Event{
		Start: &calapi.EventDateTime{Date: "2026-01-01"},
		End:   &calapi.EventDateTime{Date: "2026-01-02"},
	}
	_, _, ok := parseTimes(e)
	assert.False(t, ok)
}

func TestParseTimes_Timed(t *testing.T) {
	e := &calapi.Event{
		Start: &calapi.EventDateTime{DateTime: "2026-01-01T10:00:00Z"},
		End:   &calapi.EventDateTime{DateTime: "2026-01-01T11:00:00Z"},
	}
	s, en, ok := parseTimes(e)
	require.True(t, ok)
	assert.Equal(t, time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), s)
	assert.Equal(t, time.Date(2026, 1, 1, 11, 0, 0, 0, time.UTC), en)
}
