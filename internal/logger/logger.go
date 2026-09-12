// Package logger предоставляет structured-логгер на базе log/slog.
//
// В dev-режиме (LOG_FORMAT=text) используется tint для цветного вывода.
// В prod-режиме (LOG_FORMAT=json) — стандартный JSONHandler.
//
// Логгер прокидывается через context.Context, чтобы не тащить его явным
// параметром в каждую функцию: WithContext / FromContext.
package logger

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lmittmann/tint"
)

type ctxKey struct{}

// New создаёт slog.Logger по уровню и формату.
//
//	level:  debug | info | warn | error
//	format: json (prod) | text (dev, цветной вывод)
func New(level, format string) *slog.Logger {
	return NewWithWriter(os.Stdout, level, format)
}

// NewWithWriter — то же что New, но пишет в произвольный io.Writer.
// Удобно для тестов.
func NewWithWriter(w io.Writer, level, format string) *slog.Logger {
	lvl := parseLevel(level)
	var handler slog.Handler
	switch strings.ToLower(format) {
	case "text":
		handler = tint.NewHandler(w, &tint.Options{
			Level:      lvl,
			TimeFormat: time.Kitchen,
		})
	default: // json по умолчанию
		handler = slog.NewJSONHandler(w, &slog.HandlerOptions{Level: lvl})
	}
	return slog.New(handler)
}

// WithContext кладёт логгер в контекст.
// Позволяет передавать обогащённый логгер вниз по стеку.
func WithContext(ctx context.Context, log *slog.Logger) context.Context {
	if log == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, log)
}

// FromContext достаёт логгер из контекста или возвращает дефолтный.
// Никогда не возвращает nil.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if v, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && v != nil {
		return v
	}
	return slog.Default()
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(s) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
