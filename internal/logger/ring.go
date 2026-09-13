package logger

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
)

// Ring — потокобезопасный кольцевой буфер последних N отформатированных
// строк лога. Нужен команде /log в боте (см. bot/handler_teacher.go) —
// посмотреть, что бот только что делал, не заходя на сервер за
// `docker compose logs`/`journalctl`.
//
// Не архив: живёт только в памяти процесса и сбрасывается при
// перезапуске/передеплое — для истории есть журнал самого Docker/systemd,
// здесь важна только оперативность.
type Ring struct {
	mu    sync.Mutex
	buf   []string
	start int // индекс самой старой записи
	size  int
}

// NewRing создаёт кольцевой буфер на capacity записей.
func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = 1
	}
	return &Ring{buf: make([]string, capacity)}
}

func (r *Ring) add(line string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx := (r.start + r.size) % len(r.buf)
	r.buf[idx] = line
	if r.size < len(r.buf) {
		r.size++
	} else {
		r.start = (r.start + 1) % len(r.buf)
	}
}

// Lines возвращает записи от самой старой к самой новой.
func (r *Ring) Lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, r.size)
	for i := 0; i < r.size; i++ {
		out[i] = r.buf[(r.start+i)%len(r.buf)]
	}
	return out
}

// ringHandler оборачивает произвольный slog.Handler: каждая запись,
// прошедшая фильтр уровня, дублируется в Ring в читаемом виде, а сама
// запись без изменений уходит дальше — в обычный JSON/text handler.
// Обычный вывод (в stdout, откуда его забирает Docker/systemd) от этого
// никак не меняется.
type ringHandler struct {
	next slog.Handler
	ring *Ring
}

// WrapWithRing оборачивает handler логгера копированием каждой записи в
// ring. Отдельная функция, а не параметр New/NewWithWriter — чтобы не
// трогать их сигнатуру и существующие тесты (logger_test.go создаёт
// логгер напрямую без ring).
func WrapWithRing(next slog.Handler, ring *Ring) slog.Handler {
	return &ringHandler{next: next, ring: ring}
}

func (h *ringHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *ringHandler) Handle(ctx context.Context, r slog.Record) error {
	h.ring.add(formatRecord(r))
	return h.next.Handle(ctx, r)
}

func (h *ringHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ringHandler{next: h.next.WithAttrs(attrs), ring: h.ring}
}

func (h *ringHandler) WithGroup(name string) slog.Handler {
	return &ringHandler{next: h.next.WithGroup(name), ring: h.ring}
}

// formatRecord — компактная однострочная запись для /log в Telegram:
// время, уровень, сообщение, атрибуты через пробел. Не JSON — телеграм-
// сообщение и так режется лимитом в 4096 символов, тратить их на кавычки
// и скобки незачем.
func formatRecord(r slog.Record) string {
	var sb strings.Builder
	sb.WriteString(r.Time.Local().Format("15:04:05"))
	sb.WriteByte(' ')
	sb.WriteString(r.Level.String())
	sb.WriteByte(' ')
	sb.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&sb, " %s=%v", a.Key, a.Value.Any())
		return true
	})
	return sb.String()
}
