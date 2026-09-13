package bot

import (
	"context"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTelegramAdminUI_NotifiesAllRecipients — преподаватель и разработчик
// (Config.DevTelegramID) должны получить одно и то же уведомление каждый в
// свой чат, а не только один из них.
func TestTelegramAdminUI_NotifiesAllRecipients(t *testing.T) {
	api := &fakeTelegramAPI{}
	u := NewTelegramAdminUI(api, 999, 777)

	require.NoError(t, u.NotifyNewUser(context.Background(), 1, "Вася"))

	require.Len(t, api.sent, 2)
	var chatIDs []int64
	for _, c := range api.sent {
		msg, ok := c.(tgbotapi.MessageConfig)
		require.True(t, ok)
		chatIDs = append(chatIDs, msg.ChatID)
		assert.Contains(t, msg.Text, "Вася")
	}
	assert.ElementsMatch(t, []int64{999, 777}, chatIDs)
}

// TestTelegramAdminUI_IgnoresUnsetDevID — ID ⩽ 0 (Config.DevTelegramID,
// когда переменная окружения не задана) не должен породить сообщение в
// несуществующий чат 0.
func TestTelegramAdminUI_IgnoresUnsetDevID(t *testing.T) {
	api := &fakeTelegramAPI{}
	u := NewTelegramAdminUI(api, 999, 0)

	require.NoError(t, u.NotifyError(context.Background(), "проблема", "детали"))

	require.Len(t, api.sent, 1)
	msg := api.sent[0].(tgbotapi.MessageConfig)
	assert.Equal(t, int64(999), msg.ChatID)
}
