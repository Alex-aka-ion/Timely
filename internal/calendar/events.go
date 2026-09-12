package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	calapi "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// scope: calendar.events — только события, не полный доступ к календарю.
const scope = calapi.CalendarEventsScope

// GoogleClient — реальная реализация Client поверх Google Calendar API.
type GoogleClient struct {
	svc *calapi.Service
}

var _ Client = (*GoogleClient)(nil)

// NewGoogleClient читает credentials.json и token.json из путей и
// создаёт авторизованного клиента. Если token.json не существует или
// устарел — вернёт ошибку. Используйте RunAuthFlow для первичной авторизации.
func NewGoogleClient(ctx context.Context, credentialsPath, tokenPath string) (*GoogleClient, error) {
	cfg, err := loadOAuthConfig(credentialsPath)
	if err != nil {
		return nil, err
	}
	tok, err := loadToken(tokenPath)
	if err != nil {
		return nil, fmt.Errorf("token: %w (запустите %s --auth)", err, os.Args[0])
	}
	httpClient := cfg.Client(ctx, tok)
	svc, err := calapi.NewService(ctx, option.WithHTTPClient(httpClient))
	if err != nil {
		return nil, fmt.Errorf("calendar service: %w", err)
	}
	return &GoogleClient{svc: svc}, nil
}

// NewGoogleClientFromHTTP — для тестов с httptest.Server: подаёт готовый http.Client.
func NewGoogleClientFromHTTP(ctx context.Context, hc *http.Client, baseURL string) (*GoogleClient, error) {
	opts := []option.ClientOption{option.WithHTTPClient(hc), option.WithoutAuthentication()}
	if baseURL != "" {
		opts = append(opts, option.WithEndpoint(baseURL))
	}
	svc, err := calapi.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &GoogleClient{svc: svc}, nil
}

// UpcomingMasters возвращает все мастер-события за период [now, now+horizon].
// Не разворачивает повторяющиеся события — отдаёт только master.
func (c *GoogleClient) UpcomingMasters(ctx context.Context, calendarID string, horizon time.Duration) ([]Event, error) {
	now := time.Now().UTC()
	end := now.Add(horizon)
	// Не используем OrderBy("startTime") — он несовместим с SingleEvents=false.
	resp, err := c.svc.Events.List(calendarID).
		Context(ctx).
		ShowDeleted(false).
		SingleEvents(false). // нужны master-events
		TimeMin(now.Format(time.RFC3339)).
		TimeMax(end.Format(time.RFC3339)).
		MaxResults(250).
		Do()
	if err != nil {
		return nil, fmt.Errorf("events.list: %w", err)
	}
	out := make([]Event, 0, len(resp.Items))
	for _, it := range resp.Items {
		if it.Status == StatusCancelled {
			continue
		}
		start, end, ok := parseTimes(it)
		if !ok {
			continue
		}
		out = append(out, Event{
			ID:      it.Id,
			Summary: it.Summary,
			Start:   start,
			End:     end,
		})
	}
	return out, nil
}

// UpcomingInstances разворачивает повторяющиеся события в окно [from, to].
func (c *GoogleClient) UpcomingInstances(ctx context.Context, calendarID string, from, to time.Time) ([]Instance, error) {
	resp, err := c.svc.Events.List(calendarID).
		Context(ctx).
		ShowDeleted(true). // нужны cancelled чтобы их пропускать в Status
		SingleEvents(true).
		TimeMin(from.Format(time.RFC3339)).
		TimeMax(to.Format(time.RFC3339)).
		MaxResults(250).
		OrderBy("startTime").
		Do()
	if err != nil {
		return nil, fmt.Errorf("events.list (instances): %w", err)
	}
	out := make([]Instance, 0, len(resp.Items))
	for _, it := range resp.Items {
		start, end, ok := parseTimes(it)
		if !ok {
			continue
		}
		master := it.RecurringEventId
		if master == "" {
			master = it.Id
		}
		out = append(out, Instance{
			ID:       it.Id,
			MasterID: master,
			Summary:  it.Summary,
			Start:    start,
			End:      end,
			Status:   it.Status,
		})
	}
	return out, nil
}

// UpdateSummary обновляет название события (например, добавляет имя ученика).
func (c *GoogleClient) UpdateSummary(ctx context.Context, calendarID, eventID, summary string) error {
	patch := &calapi.Event{Summary: summary}
	_, err := c.svc.Events.Patch(calendarID, eventID, patch).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("events.patch: %w", err)
	}
	return nil
}

// parseTimes извлекает время начала/конца события (для timed events).
// All-day events (только Date) пропускаем — их не нужно напоминать.
func parseTimes(e *calapi.Event) (time.Time, time.Time, bool) {
	if e.Start == nil || e.End == nil || e.Start.DateTime == "" || e.End.DateTime == "" {
		return time.Time{}, time.Time{}, false
	}
	s, err := time.Parse(time.RFC3339, e.Start.DateTime)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	en, err := time.Parse(time.RFC3339, e.End.DateTime)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return s, en, true
}

// --- OAuth helpers ----------------------------------------------------------

func loadOAuthConfig(path string) (*oauth2.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("чтение credentials: %w", err)
	}
	cfg, err := google.ConfigFromJSON(b, scope)
	if err != nil {
		return nil, fmt.Errorf("парсинг credentials: %w", err)
	}
	return cfg, nil
}

func loadToken(path string) (*oauth2.Token, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var t oauth2.Token
	if err := json.NewDecoder(f).Decode(&t); err != nil {
		return nil, err
	}
	if t.AccessToken == "" && t.RefreshToken == "" {
		return nil, errors.New("пустой token.json")
	}
	return &t, nil
}

// SaveToken сохраняет токен в файл с правами 0600.
func SaveToken(path string, t *oauth2.Token) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(t)
}

// RunAuthFlow проводит первичную OAuth-авторизацию.
// Печатает URL, ждёт ввод authorization code из stdin, обменивает на token.
//
// Использование: ./booking-bot --auth
func RunAuthFlow(ctx context.Context, credentialsPath, tokenPath string) error {
	cfg, err := loadOAuthConfig(credentialsPath)
	if err != nil {
		return err
	}
	authURL := cfg.AuthCodeURL("state-token", oauth2.AccessTypeOffline)
	fmt.Printf("Перейдите по ссылке и разрешите доступ:\n\n%s\n\n", authURL)
	fmt.Print("Введите код авторизации: ")
	var code string
	if _, err := fmt.Scan(&code); err != nil {
		return fmt.Errorf("чтение кода: %w", err)
	}
	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("обмен кода: %w", err)
	}
	if err := SaveToken(tokenPath, tok); err != nil {
		return fmt.Errorf("сохранение токена: %w", err)
	}
	fmt.Println("Токен сохранён в", tokenPath)
	return nil
}
