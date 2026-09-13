package bot

import (
	"context"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// menuMsg — минимальное сообщение для передачи в обработчики кнопок меню:
// им нужен только msg.From.
func menuMsg(userID int64, text string) *tgbotapi.Message {
	return &tgbotapi.Message{From: &tgbotapi.User{ID: userID}, Text: text}
}

// TestMenuAction_TeacherButtons — каждая кнопка меню преподавателя должна
// распознаваться menuAction и не паниковать при вызове. Конкретное поведение
// (что именно отправляется) уже покрыто тестами самих команд — здесь важна
// только маршрутизация "текст кнопки → обработчик".
func TestMenuAction_TeacherButtons(t *testing.T) {
	h := makeHandler(t)
	const teacher = int64(999)

	buttons := []string{btnStudents, btnStudentsNew, btnUnlinked, btnEvents, btnSettings}
	for _, label := range buttons {
		action, ok := h.menuAction(teacher, label)
		require.Truef(t, ok, "кнопка %q должна распознаваться как действие меню", label)
		require.NotNil(t, action)
		action(context.Background(), menuMsg(teacher, label))
	}
}

// TestMenuAction_UnknownTextIsNotMenu — свободный текст (например, вводимое
// имя ученика) не должен приниматься за нажатие кнопки, а кнопки одной роли
// не должны срабатывать у другой: иначе родитель мог бы, отправив нужный
// текст руками, вызвать команду преподавателя.
func TestMenuAction_UnknownTextIsNotMenu(t *testing.T) {
	h := makeHandler(t)

	_, ok := h.menuAction(999, "Просто текст, не кнопка")
	assert.False(t, ok)

	_, ok = h.menuAction(999, btnMyStudents)
	assert.False(t, ok, "кнопка родителя не должна работать у преподавателя")

	_, ok = h.menuAction(1, btnStudents)
	assert.False(t, ok, "кнопка преподавателя не должна работать у родителя")
}

func TestMenuAction_ParentButton(t *testing.T) {
	h := makeHandler(t)
	const parent = int64(1)
	action, ok := h.menuAction(parent, btnMyStudents)
	require.True(t, ok)
	action(context.Background(), menuMsg(parent, btnMyStudents))
}

// TestMenuAction_ClearsDialogState — переход по кнопке меню отменяет
// незавершённый диалог, даже если он был на середине.
func TestMenuAction_ClearsDialogState(t *testing.T) {
	h := makeHandler(t)
	const teacher = int64(999)
	h.dialog.Set(teacher, StateAwaitingStudentName, map[string]any{})

	action, ok := h.menuAction(teacher, btnEvents)
	require.True(t, ok)
	action(context.Background(), menuMsg(teacher, btnEvents))

	assert.Equal(t, StateIdle, h.dialog.Get(teacher).State,
		"кнопка меню должна была сбросить незавершённый диалог")
}

// TestMenuAction_ButtonTextDuringDialogIsNotTreatedAsInput воспроизводит
// сценарий, ради которого проверка menuAction стоит в handleUpdate ДО
// маршрутизации по состоянию диалога: если бы текст кнопки "долетал" как
// обычный ввод в handleTeacherMessage, при StateAwaitingStudentName он
// создал бы ученика с именем-эмодзи вместо перехода в раздел меню.
func TestMenuAction_ButtonTextDuringDialogIsNotTreatedAsInput(t *testing.T) {
	h := makeHandler(t)
	const teacher = int64(999)
	h.dialog.Set(teacher, StateAwaitingStudentName, map[string]any{})

	upd := tgbotapi.Update{Message: &tgbotapi.Message{
		From: &tgbotapi.User{ID: teacher},
		Text: btnEvents,
	}}
	h.handleUpdate(context.Background(), upd)

	students, err := h.store.GetStudents(context.Background())
	require.NoError(t, err)
	assert.Empty(t, students, "нажатие кнопки меню не должно было создать ученика")
}

// TestCancelCommand_ViaHandleUpdate — /cancel раньше не работал вообще:
// handleUpdate перехватывал любое IsCommand()==true сообщение в своём switch
// по командам и делал return, так и не доходя до старой (мёртвой) проверки
// в handleTeacherMessage. Теперь команда обрабатывается прямо в handleUpdate
// (см. case "cancel" в bot.go).
func TestCancelCommand_ViaHandleUpdate(t *testing.T) {
	h := makeHandler(t)
	const teacher = int64(999)
	h.dialog.Set(teacher, StateAwaitingGlobalIntervals, nil)

	upd := tgbotapi.Update{Message: &tgbotapi.Message{
		From: &tgbotapi.User{ID: teacher},
		Text: "/cancel",
		Entities: []tgbotapi.MessageEntity{
			{Type: "bot_command", Offset: 0, Length: len("/cancel")},
		},
	}}
	require.True(t, upd.Message.IsCommand(), "сообщение должно распознаваться как команда — иначе тест ничего не проверяет")
	require.Equal(t, "cancel", upd.Message.Command())

	h.handleUpdate(context.Background(), upd)

	assert.Equal(t, StateIdle, h.dialog.Get(teacher).State)
}
