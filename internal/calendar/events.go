package calendar

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
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

// RunAuthFlow проводит первичную OAuth-авторизацию через loopback flow.
//
// Раньше здесь был OOB-flow (Google печатал код, пользователь копировал его
// руками в терминал) — Google отключил этот механизм в январе 2023 года.
// Актуальная замена для десктопных/CLI-приложений — loopback flow: поднимаем
// временный HTTP-сервер на 127.0.0.1 (порт выбирает ОС — Google принимает
// любой порт на loopback-адресе для клиентов типа "Desktop app", регистрировать
// его заранее в консоли не нужно), открываем ссылку авторизации, и после
// согласия пользователя Google сам делает редирект браузера на этот локальный
// сервер с кодом авторизации в query-параметре. Ручного копирования кода
// больше нет.
//
// Использование: ./booking-bot --auth
func RunAuthFlow(ctx context.Context, credentialsPath, tokenPath string) error {
	cfg, err := loadOAuthConfig(credentialsPath)
	if err != nil {
		return err
	}

	// port 0 — просим ОС выделить свободный порт сама, чтобы не зависеть
	// от того, что конкретный порт может быть занят другим процессом.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("запуск локального сервера для callback: %w", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	cfg.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d", port)

	// state — случайная строка, защита от подделки callback (CSRF): в конце
	// проверяем, что Google вернул именно то значение, которое мы отправили.
	state, err := randomState()
	if err != nil {
		return fmt.Errorf("генерация state: %w", err)
	}

	// authResult — то, что придёт из HTTP-хендлера ниже. Хендлер выполняется
	// в отдельной горутине (net/http сам её порождает на каждый запрос),
	// поэтому единственный безопасный способ передать результат обратно в
	// основную горутину — канал, а не обычная переменная.
	type authResult struct {
		code string
		err  error
	}
	resultCh := make(chan authResult, 1)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case q.Get("error") != "":
			fmt.Fprintln(w, "Доступ не предоставлен, можно закрыть эту вкладку.")
			resultCh <- authResult{err: fmt.Errorf("google вернул ошибку: %s", q.Get("error"))}
		case q.Get("state") != state:
			http.Error(w, "неверный state", http.StatusBadRequest)
			resultCh <- authResult{err: errors.New("неверный state в callback — попробуйте ещё раз")}
		case q.Get("code") == "":
			http.Error(w, "нет code в запросе", http.StatusBadRequest)
			resultCh <- authResult{err: errors.New("code отсутствует в callback")}
		default:
			fmt.Fprintln(w, "Готово! Можно закрыть эту вкладку и вернуться в терминал.")
			resultCh <- authResult{code: q.Get("code")}
		}
	})

	srv := &http.Server{Handler: mux}
	go func() {
		// ErrServerClosed — ожидаемая ошибка после srv.Shutdown ниже, не
		// настоящий сбой, поэтому её отдельно отфильтровываем.
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			fmt.Println("auth callback server:", err)
		}
	}()
	defer srv.Shutdown(context.Background())

	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline)
	fmt.Printf("Открой ссылку в браузере и разреши доступ:\n\n%s\n\n", authURL)
	fmt.Println("Жду подтверждения в браузере...")

	var code string
	select {
	case res := <-resultCh:
		if res.err != nil {
			return res.err
		}
		code = res.code
	case <-ctx.Done():
		return ctx.Err()
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

// randomState генерирует криптографически случайную строку для параметра
// state — защита OAuth callback от подделки запроса (CSRF).
func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
