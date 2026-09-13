package logger

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRing_Empty(t *testing.T) {
	r := NewRing(3)
	assert.Empty(t, r.Lines())
}

// TestRing_Overflow — при переполнении должны остаться только самые
// свежие capacity записей, в хронологическом порядке.
func TestRing_Overflow(t *testing.T) {
	r := NewRing(3)
	for _, s := range []string{"a", "b", "c", "d", "e"} {
		r.add(s)
	}
	assert.Equal(t, []string{"c", "d", "e"}, r.Lines())
}

func TestRing_UnderCapacity(t *testing.T) {
	r := NewRing(5)
	r.add("a")
	r.add("b")
	assert.Equal(t, []string{"a", "b"}, r.Lines())
}

// TestWrapWithRing_CapturesAndPassesThrough — обёрнутый handler должен и
// продублировать запись в Ring, и пропустить её дальше без изменений
// (обычный вывод в JSON/text не должен пострадать).
func TestWrapWithRing_CapturesAndPassesThrough(t *testing.T) {
	var buf bytes.Buffer
	base := NewWithWriter(&buf, "info", "json")
	ring := NewRing(10)
	log := base.Handler()
	wrapped := WrapWithRing(log, ring)

	logWithRing := slog.New(wrapped)
	logWithRing.Info("hello", "user_id", 7)

	require.Contains(t, buf.String(), `"msg":"hello"`, "обычный вывод не должен измениться")

	lines := ring.Lines()
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "hello")
	assert.Contains(t, lines[0], "user_id=7")
	assert.Contains(t, lines[0], "INFO")
}

// TestWrapWithRing_FiltersByLevel — запись ниже настроенного уровня не
// должна ни уходить в обычный вывод, ни попадать в Ring (Enabled решает
// это до Handle).
func TestWrapWithRing_FiltersByLevel(t *testing.T) {
	var buf bytes.Buffer
	base := NewWithWriter(&buf, "warn", "json")
	ring := NewRing(10)
	logWithRing := slog.New(WrapWithRing(base.Handler(), ring))

	logWithRing.Info("skip-me")
	logWithRing.Warn("keep-me")

	assert.False(t, strings.Contains(buf.String(), "skip-me"))
	lines := ring.Lines()
	require.Len(t, lines, 1)
	assert.Contains(t, lines[0], "keep-me")
}
