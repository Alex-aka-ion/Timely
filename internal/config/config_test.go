package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseIntervals(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []time.Duration
		wantErr bool
	}{
		{"один интервал", "24h", []time.Duration{24 * time.Hour}, false},
		{"три интервала", "24h,2h,30m", []time.Duration{24 * time.Hour, 2 * time.Hour, 30 * time.Minute}, false},
		{"с пробелами", " 24h , 2h ", []time.Duration{24 * time.Hour, 2 * time.Hour}, false},
		{"пустая строка", "", nil, true},
		{"некорректный формат", "abc", nil, true},
		{"отрицательный", "-1h", nil, true},
		{"пустой элемент", "24h,,2h", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseIntervals(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConfigValidate(t *testing.T) {
	base := Config{
		BotToken:          "token",
		TeacherTelegramID: 123,
		DBPath:            "./data/booking.db",
		ReminderIntervals: []time.Duration{2 * time.Hour},
		SchedulerTick:     5 * time.Minute,
		LogLevel:          "info",
		LogFormat:         "json",
	}
	require.NoError(t, base.Validate())

	t.Run("пустой токен", func(t *testing.T) {
		c := base
		c.BotToken = ""
		assert.Error(t, c.Validate())
	})
	t.Run("teacher_id <= 0", func(t *testing.T) {
		c := base
		c.TeacherTelegramID = 0
		assert.Error(t, c.Validate())
	})
	t.Run("dev_id не задан — валиден", func(t *testing.T) {
		c := base
		c.DevTelegramID = 0
		assert.NoError(t, c.Validate())
	})
	t.Run("dev_id отрицательный", func(t *testing.T) {
		c := base
		c.DevTelegramID = -1
		assert.Error(t, c.Validate())
	})
	t.Run("неизвестный уровень логов", func(t *testing.T) {
		c := base
		c.LogLevel = "trace"
		assert.Error(t, c.Validate())
	})
	t.Run("неизвестный формат логов", func(t *testing.T) {
		c := base
		c.LogFormat = "xml"
		assert.Error(t, c.Validate())
	})
	t.Run("пустые интервалы", func(t *testing.T) {
		c := base
		c.ReminderIntervals = nil
		assert.Error(t, c.Validate())
	})
}

func TestMaxInterval(t *testing.T) {
	c := Config{ReminderIntervals: []time.Duration{2 * time.Hour, 24 * time.Hour, 30 * time.Minute}}
	assert.Equal(t, 24*time.Hour, c.MaxInterval())
}

func TestLoad_RequiresTeacherID(t *testing.T) {
	t.Setenv("BOT_TOKEN", "token")
	// TEACHER_TELEGRAM_ID не выставлен.
	_, err := Load()
	assert.Error(t, err)
}

func TestLoad_OK(t *testing.T) {
	t.Setenv("BOT_TOKEN", "token")
	t.Setenv("TEACHER_TELEGRAM_ID", "123")
	t.Setenv("REMINDER_INTERVALS", "24h,2h")
	t.Setenv("SCHEDULER_TICK", "5m")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_FORMAT", "json")
	c, err := Load()
	require.NoError(t, err)
	assert.Equal(t, int64(123), c.TeacherTelegramID)
	assert.Zero(t, c.DevTelegramID, "DEV_TELEGRAM_ID не задан в этом тесте")
	assert.Len(t, c.ReminderIntervals, 2)
	assert.Equal(t, 24*time.Hour, c.MaxInterval())
}

func TestLoad_DevTelegramIDOptional(t *testing.T) {
	t.Setenv("BOT_TOKEN", "token")
	t.Setenv("TEACHER_TELEGRAM_ID", "123")
	t.Setenv("DEV_TELEGRAM_ID", "456")
	t.Setenv("REMINDER_INTERVALS", "24h,2h")
	t.Setenv("SCHEDULER_TICK", "5m")
	t.Setenv("LOG_LEVEL", "info")
	t.Setenv("LOG_FORMAT", "json")
	c, err := Load()
	require.NoError(t, err)
	assert.Equal(t, int64(456), c.DevTelegramID)
}

func TestLoad_InvalidDevTelegramID(t *testing.T) {
	t.Setenv("BOT_TOKEN", "token")
	t.Setenv("TEACHER_TELEGRAM_ID", "123")
	t.Setenv("DEV_TELEGRAM_ID", "не число")
	_, err := Load()
	assert.Error(t, err)
}
