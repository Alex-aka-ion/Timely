package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWithWriter_JSONFormat(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf, "info", "json")
	log.Info("hello", "user_id", 42)

	var entry map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &entry))
	assert.Equal(t, "hello", entry["msg"])
	assert.EqualValues(t, 42, entry["user_id"])
}

func TestNewWithWriter_TextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf, "info", "text")
	log.Info("hello")
	// В тексте формат человекочитаемый — просто проверим что сообщение есть.
	assert.Contains(t, buf.String(), "hello")
}

func TestParseLevel(t *testing.T) {
	tests := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
		"junk":    slog.LevelInfo,
	}
	for in, want := range tests {
		assert.Equal(t, want, parseLevel(in), "input=%q", in)
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf, "warn", "json")
	log.Info("info-msg")  // должно быть отфильтровано
	log.Warn("warn-msg")  // должно пройти
	out := buf.String()
	assert.NotContains(t, out, "info-msg")
	assert.Contains(t, out, "warn-msg")
}

func TestWithContextFromContext(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf, "info", "json")
	ctx := WithContext(context.Background(), log)

	got := FromContext(ctx)
	assert.Same(t, log, got)
}

func TestFromContext_DefaultIfMissing(t *testing.T) {
	got := FromContext(context.Background())
	assert.NotNil(t, got)
}

func TestFromContext_NilContext(t *testing.T) {
	got := FromContext(nil) //nolint:staticcheck // намеренно nil
	assert.NotNil(t, got)
}

func TestWithContext_NilLogger_DoesNotCrash(t *testing.T) {
	ctx := WithContext(context.Background(), nil)
	got := FromContext(ctx)
	assert.NotNil(t, got)
}

func TestStructuredFields(t *testing.T) {
	var buf bytes.Buffer
	log := NewWithWriter(&buf, "info", "json")
	log.Info("event", "user_id", 7, "messenger", "telegram")
	line := strings.TrimSpace(buf.String())

	var entry map[string]any
	require.NoError(t, json.Unmarshal([]byte(line), &entry))
	assert.EqualValues(t, 7, entry["user_id"])
	assert.Equal(t, "telegram", entry["messenger"])
}
