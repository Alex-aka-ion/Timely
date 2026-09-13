package bot

import (
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Префиксы callback_data для маршрутизации inline-кнопок.
// Формат: "<префикс>:<id>" или "<префикс>:<id>:<sub>".
const (
	cbNewStudent     = "new_student"    // создать ученика для нового родителя; данные: user_id
	cbExistStudent   = "exist_student"  // показать список учеников для нового родителя; данные: user_id
	cbLinkContact    = "link_contact"   // привязать существующего ученика к родителю; данные: user_id:student_id
	cbUnlinkContact  = "unlink_contact" // отвязать; данные: student_id:user_id
	cbUnlinkContactC = "unlink_cont_c"  // подтверждение отвязки контакта; данные: student_id:user_id
	cbUnlinkEvent    = "unlink_event"   // отвязать событие от ученика; данные: master_event_id
	cbUnlinkEventC   = "unlink_event_c" // подтверждение; данные: master_event_id
	cbStudentMenu    = "student"        // показать меню ученика; данные: student_id
	cbStudentContact = "student_cont"   // меню "контакты"; данные: student_id
	cbPickEvent      = "pick_event"     // выбран event для привязки; данные: master_event_id
	cbPickStudent    = "pick_student"   // выбран ученик для события; данные: master_event_id:student_id
	cbSetIntervals   = "set_intervals"  // изменить интервалы ученика; данные: student_id
	cbRenameStudent  = "rename_student" // переименовать ученика; данные: student_id
	cbStopRemove     = "stop_remove"    // удалить родителя из всех контактов после /stop; данные: user_id
	cbCancel         = "cancel"         // отменить текущее действие
)

// kbNewUserChoice — кнопки "Новый ученик / К существующему" для нового родителя.
func kbNewUserChoice(userID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"Новый ученик",
				fmt.Sprintf("%s:%d", cbNewStudent, userID),
			),
			tgbotapi.NewInlineKeyboardButtonData(
				"К существующему",
				fmt.Sprintf("%s:%d", cbExistStudent, userID),
			),
		),
	)
}

// kbStudents строит клавиатуру со списком учеников для выбора родителя.
type studentChoice struct {
	ID   int64
	Name string
}

func kbStudents(prefix string, userID int64, students []studentChoice) tgbotapi.InlineKeyboardMarkup {
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(students)+1)
	for _, s := range students {
		data := fmt.Sprintf("%s:%d:%d", prefix, userID, s.ID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(s.Name, data),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("Отмена", cbCancel),
	))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// kbStudentMenu — действия по конкретному ученику.
func kbStudentMenu(studentID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"Управлять контактами",
				fmt.Sprintf("%s:%d", cbStudentContact, studentID),
			),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"Изменить интервалы",
				fmt.Sprintf("%s:%d", cbSetIntervals, studentID),
			),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"Переименовать",
				fmt.Sprintf("%s:%d", cbRenameStudent, studentID),
			),
		),
	)
}

// kbStopRequest — кнопки для решения преподавателя после /stop родителя:
// либо насовсем убрать его из контактов всех учеников, либо оставить как
// есть (аккаунт всё равно уже деактивирован — сообщения приходить не будут,
// пока родитель сам не пришлёт /start).
func kbStopRequest(userID int64) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"Удалить из контактов",
				fmt.Sprintf("%s:%d", cbStopRemove, userID),
			),
			tgbotapi.NewInlineKeyboardButtonData("Оставить", cbCancel),
		),
	)
}

// kbConfirm — пара кнопок Да/Нет с подтверждающим callback.
func kbConfirm(confirmCB, cancelCB string) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("Да, отвязать", confirmCB),
			tgbotapi.NewInlineKeyboardButtonData("Отмена", cancelCB),
		),
	)
}

// --- постоянное меню (ReplyKeyboardMarkup) -----------------------------------
//
// В отличие от инлайн-кнопок выше (callback_data, отдельный API-метод
// answerCallback, кнопки живут под конкретным сообщением), это кнопки
// "быстрого ввода": нажатие на любую из них с точки зрения Telegram API
// неотличимо от того, что пользователь напечатал этот текст сам и отправил.
// Поэтому у них нет callback'ов — это текстовые алиасы команд, которые
// разбираются в bot.go (см. menuAction).

const (
	btnStudents         = "👥 Ученики"
	btnStudentsNew      = "➕ Новый ученик"
	btnUnlinked         = "🔗 Не привязаны"
	btnUnlinkedStudents = "🧒 Ученики без родителя"
	btnEvents           = "📅 События"
	btnSettings         = "⚙️ Настройки"
	btnMyStudents       = "👶 Мои ученики"
	btnRename           = "✏️ Изменить имя"
)

// menuKeyboardTeacher — постоянное меню преподавателя: по кнопке на каждую
// существующую команду (/students, /students_new, /unlinked, /events,
// /settings) — сами команды при этом продолжают работать как раньше.
func menuKeyboardTeacher() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnStudents),
			tgbotapi.NewKeyboardButton(btnStudentsNew),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnUnlinked),
			tgbotapi.NewKeyboardButton(btnUnlinkedStudents),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnEvents),
			tgbotapi.NewKeyboardButton(btnSettings),
		),
	)
	// ResizeKeyboard — компактная высота кнопок вместо "во весь экран"
	// (поведение Telegram-клиента по умолчанию, если флаг не выставить).
	kb.ResizeKeyboard = true
	return kb
}

// menuKeyboardParent — меню родителя: пока только "Мои ученики", но уже
// отдельной функцией — когда кнопок станет больше, менять нужно будет
// только здесь.
func menuKeyboardParent() tgbotapi.ReplyKeyboardMarkup {
	kb := tgbotapi.NewReplyKeyboard(
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnMyStudents),
		),
		tgbotapi.NewKeyboardButtonRow(
			tgbotapi.NewKeyboardButton(btnRename),
		),
	)
	kb.ResizeKeyboard = true
	return kb
}
