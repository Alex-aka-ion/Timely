package bot

import (
	"context"
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sentTexts(h *Handler) []string {
	var out []string
	for _, c := range h.api.(*fakeTelegramAPI).sent {
		if m, ok := c.(tgbotapi.MessageConfig); ok {
			out = append(out, m.Text)
		}
	}
	return out
}

func TestHandleNotifications_Empty(t *testing.T) {
	h := makeHandler(t)
	h.handleNotifications(context.Background(), &tgbotapi.Message{From: &tgbotapi.User{ID: 999}})
	assert.Contains(t, lastSentText(t, h), "пока не отправлялось")
}

func TestHandleNotifications_ShowsRealNamesAndStatus(t *testing.T) {
	h := makeHandler(t)
	ctx := context.Background()
	u, err := h.store.CreateUser(ctx, "Анна Иванова")
	require.NoError(t, err)
	require.NoError(t, h.store.LogNotification(ctx, u.ID, "Напоминание: занятие у ученика Петя\nСобытие: Логопед\nЧерез 24 ч", true))
	require.NoError(t, h.store.LogNotification(ctx, u.ID, "Напоминание", false))

	h.handleNotifications(ctx, &tgbotapi.Message{From: &tgbotapi.User{ID: 999}})

	all := strings.Join(sentTexts(h), "\n")
	assert.Contains(t, all, "Анна Иванова", "реальное имя родителя")
	assert.Contains(t, all, "\n    Напоминание: занятие у ученика Петя\n    Событие: Логопед\n    Через 24 ч",
		"название события на отдельной строке, не склеено с именем ученика")
	assert.Contains(t, all, "не доставлено")
	assert.Less(t, strings.Index(all, "Напоминание"), strings.Index(all, "занятие у ученика Петя"), "новые сверху")
}

func TestHandleNotifications_RequiresTeacher(t *testing.T) {
	h := makeHandler(t)
	h.handleNotifications(context.Background(), &tgbotapi.Message{From: &tgbotapi.User{ID: 1}})
	assert.Empty(t, h.api.(*fakeTelegramAPI).sent)
}

func TestChunkLines(t *testing.T) {
	lines := []string{strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)}
	chunks := chunkLines(lines, 90)
	require.Len(t, chunks, 2)
	assert.Equal(t, lines[0]+"\n\n"+lines[1], chunks[0])
	assert.Equal(t, lines[2], chunks[1])
	assert.Empty(t, chunkLines(nil, 90))
}
