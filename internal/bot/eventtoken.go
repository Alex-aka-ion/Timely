package bot

import (
	"strconv"
	"sync"
)

// eventTokens отображает короткий токен на настоящий master_event_id
// события Google Calendar и обратно.
//
// Зачем это вообще нужно: Telegram ограничивает callback_data кнопки
// 64 байтами. ID событий, которые Google Calendar создаёт сам, обычно
// укладываются в лимит (~26 символов), но событие, импортированное в
// календарь из другого сервиса (Outlook, приглашение, ics-подписка и
// т.п.), может иметь заметно более длинный ID. Кнопка с таким ID внутри
// становится невалидной, а Telegram при этом отклоняет ВЕСЬ набор кнопок
// целиком — даже нормальные события по соседству переставали показываться
// в /events. Поэтому в callback_data кладём не сам ID, а короткий токен, а
// настоящий ID достаём из этой мапы уже при обработке нажатия.
//
// Токены не удаляются — их число равно количеству когда-либо показанных
// в интерфейсе событий (единицы-десятки за всё время жизни процесса), так
// что не проблема для масштаба этого бота. Как и Dialog (см. dialog.go),
// при перезапуске бота мапа пересоздаётся пустой — это ожидаемо.
type eventTokens struct {
	mu   sync.Mutex
	toID map[string]string // token -> event ID
	toTk map[string]string // event ID -> token
	next uint64
}

func newEventTokens() *eventTokens {
	return &eventTokens{toID: map[string]string{}, toTk: map[string]string{}}
}

// tokenFor возвращает короткий токен для eventID: существующий при
// повторном обращении с тем же ID, новый — при первом.
func (t *eventTokens) tokenFor(eventID string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tok, ok := t.toTk[eventID]; ok {
		return tok
	}
	t.next++
	// base36 (0-9a-z) компактнее десятичного и не содержит ':' — токен
	// безопасно класть как отдельный сегмент в "prefix:token[:...]".
	tok := strconv.FormatUint(t.next, 36)
	t.toTk[eventID] = tok
	t.toID[tok] = eventID
	return tok
}

// resolve возвращает настоящий event ID по токену, если он ещё известен
// (например, не был потерян перезапуском бота).
func (t *eventTokens) resolve(token string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	id, ok := t.toID[token]
	return id, ok
}
