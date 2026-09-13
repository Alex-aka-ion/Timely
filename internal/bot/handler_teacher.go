package bot

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/booking-bot/booking-bot/internal/calendar"
	"github.com/booking-bot/booking-bot/internal/config"
	"github.com/booking-bot/booking-bot/internal/logger"
	"github.com/booking-bot/booking-bot/internal/store"
)

// upcomingHorizon — сколько вперёд показывать события в команде /events.
const upcomingHorizon = 14 * 24 * time.Hour

// pendingEventKey — ключ в Dialog.Data: master_event_id события, которое мы
// привязываем к ученику.
const pendingEventKey = "pending_event_id"

// pendingStudentKey — ID ученика, к которому привязываем нового родителя.
const pendingStudentKey = "pending_student_id"

// pendingNewParentKey — ID нового родителя для команды cbNewStudent.
const pendingNewParentKey = "pending_user_id"

// --- /students --------------------------------------------------------------

func (h *Handler) handleStudents(ctx context.Context, msg *tgbotapi.Message) {
	if !h.requireTeacher(msg.From.ID) {
		return
	}
	students, err := h.store.GetStudents(ctx)
	if err != nil {
		logger.FromContext(ctx).Error("GetStudents", "error", err)
		h.send(msg.From.ID, "Ошибка получения списка учеников.")
		return
	}
	if len(students) == 0 {
		h.send(msg.From.ID, "Учеников пока нет. Создайте через /students_new.")
		return
	}
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(students))
	for _, s := range students {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				s.DisplayName,
				fmt.Sprintf("%s:%d", cbStudentMenu, s.ID),
			),
		))
	}
	out := tgbotapi.NewMessage(msg.From.ID, "Ученики:")
	out.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := h.api.Send(out); err != nil {
		logger.FromContext(ctx).Error("send /students", "error", err)
	}
}

// --- /students_new ----------------------------------------------------------

func (h *Handler) handleStudentsNew(ctx context.Context, msg *tgbotapi.Message) {
	if !h.requireTeacher(msg.From.ID) {
		return
	}
	// Запрашиваем имя ученика без привязки к существующему пользователю.
	h.dialog.Set(msg.From.ID, StateAwaitingStudentName, map[string]any{})
	h.send(msg.From.ID, "Введите имя нового ученика:")
}

// --- /unlinked --------------------------------------------------------------

func (h *Handler) handleUnlinked(ctx context.Context, msg *tgbotapi.Message) {
	if !h.requireTeacher(msg.From.ID) {
		return
	}
	users, err := h.store.GetUnlinkedUsers(ctx)
	if err != nil {
		logger.FromContext(ctx).Error("GetUnlinkedUsers", "error", err)
		h.send(msg.From.ID, "Ошибка.")
		return
	}
	if len(users) == 0 {
		h.send(msg.From.ID, "У всех родителей есть ученики.")
		return
	}
	var sb strings.Builder
	sb.WriteString("Не привязанные пользователи:\n")
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(users))
	for _, u := range users {
		fmt.Fprintf(&sb, "• %s\n", u.FullName)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"Привязать "+u.FullName,
				fmt.Sprintf("%s:%d", cbExistStudent, u.ID),
			),
		))
	}
	out := tgbotapi.NewMessage(msg.From.ID, sb.String())
	out.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := h.api.Send(out); err != nil {
		logger.FromContext(ctx).Error("send /unlinked", "error", err)
	}
}

// --- /unlinked_students -------------------------------------------------------

// handleUnlinkedStudents — обратная выборка к /unlinked: там показывались
// зарегистрированные РОДИТЕЛИ без ученика, здесь — УЧЕНИКИ без единого
// привязанного родителя (store.GetUnlinkedStudents). Кнопка каждого пункта
// открывает обычную карточку ученика (cbStudentMenu) — оттуда преподаватель
// уже может управлять контактами/интервалами/именем как для любого другого
// ученика.
func (h *Handler) handleUnlinkedStudents(ctx context.Context, msg *tgbotapi.Message) {
	if !h.requireTeacher(msg.From.ID) {
		return
	}
	students, err := h.store.GetUnlinkedStudents(ctx)
	if err != nil {
		logger.FromContext(ctx).Error("GetUnlinkedStudents", "error", err)
		h.send(msg.From.ID, "Ошибка.")
		return
	}
	if len(students) == 0 {
		h.send(msg.From.ID, "У всех учеников есть хотя бы один привязанный контакт.")
		return
	}
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(students))
	for _, st := range students {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				st.DisplayName,
				fmt.Sprintf("%s:%d", cbStudentMenu, st.ID),
			),
		))
	}
	out := tgbotapi.NewMessage(msg.From.ID, "Ученики без привязанного родителя:")
	out.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := h.api.Send(out); err != nil {
		logger.FromContext(ctx).Error("send /unlinked_students", "error", err)
	}
}

// --- /events ----------------------------------------------------------------

func (h *Handler) handleEvents(ctx context.Context, msg *tgbotapi.Message) {
	if !h.requireTeacher(msg.From.ID) {
		return
	}
	if h.calClient == nil {
		h.send(msg.From.ID, "Календарь не подключён.")
		return
	}
	events, err := h.calClient.UpcomingMasters(ctx, h.cfg.GoogleCalendarID, upcomingHorizon)
	if err != nil {
		logger.FromContext(ctx).Error("UpcomingMasters", "error", err)
		h.send(msg.From.ID, "Не удалось получить события из календаря.")
		return
	}
	// Фильтруем те, где уже есть привязка.
	var unlinked []eventListItem
	for _, e := range events {
		if _, err := h.store.GetStudentForEvent(ctx, e.ID); !errors.Is(err, store.ErrNotFound) {
			continue
		}
		unlinked = append(unlinked, eventListItem{ID: e.ID, Label: formatEvent(e.Start, e.Summary)})
	}
	if len(unlinked) == 0 {
		h.send(msg.From.ID, "Все ближайшие события уже привязаны.")
		return
	}
	// В кнопку кладём не сам ID события, а короткий токен (см. eventtoken.go) —
	// event ID может быть длиннее 64-байтного лимита callback_data Telegram.
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(unlinked))
	for _, e := range unlinked {
		token := h.eventTokens.tokenFor(e.ID)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(e.Label, cbPickEvent+":"+token),
		))
	}
	out := tgbotapi.NewMessage(msg.From.ID, "Выберите событие:")
	out.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)
	if _, err := h.api.Send(out); err != nil {
		logger.FromContext(ctx).Error("send /events", "error", err)
	}
}

type eventListItem struct {
	ID    string
	Label string
}

func formatEvent(start time.Time, summary string) string {
	return fmt.Sprintf("%s — %s", start.Local().Format("Mon 02.01 15:04"), summary)
}

// --- /settings --------------------------------------------------------------

func (h *Handler) handleSettings(ctx context.Context, msg *tgbotapi.Message) {
	if !h.requireTeacher(msg.From.ID) {
		return
	}
	val, err := h.store.GetSetting(ctx, "reminder_intervals")
	if err != nil {
		logger.FromContext(ctx).Error("GetSetting", "error", err)
		h.send(msg.From.ID, "Ошибка.")
		return
	}
	h.dialog.Set(msg.From.ID, StateAwaitingGlobalIntervals, nil)
	h.send(msg.From.ID, fmt.Sprintf(
		"Текущие глобальные интервалы напоминаний: %s\n\n"+
			"Введите новые через запятую (например: 24h,2h,30m), или /cancel для отмены.",
		val))
}

// --- сообщения преподавателя в режиме диалога ------------------------------

func (h *Handler) handleTeacherMessage(ctx context.Context, msg *tgbotapi.Message) {
	log := logger.FromContext(ctx)
	from := msg.From

	// /cancel обрабатывается в bot.go:handleUpdate (case "cancel"), а не
	// здесь: это команда (IsCommand()==true), и handleUpdate возвращается
	// сразу после своего switch по командам, так и не дойдя досюда — раньше
	// тут была проверка на тот же текст, но она была мёртвым кодом.
	state := h.dialog.Get(from.ID)
	switch state.State {
	case StateAwaitingStudentName:
		name := strings.TrimSpace(msg.Text)
		if name == "" {
			h.send(from.ID, "Имя пустое. Введите ещё раз или /cancel.")
			return
		}
		st, err := h.store.CreateStudent(ctx, name)
		if err != nil {
			log.Error("CreateStudent", "error", err)
			h.send(from.ID, "Не удалось создать ученика.")
			return
		}
		// Если в Data есть pending_user_id — это сценарий "новый родитель → новый ученик".
		if uid, ok := state.Data[pendingNewParentKey].(int64); ok {
			label := h.lookupParentName(ctx, uid)
			if err := h.store.LinkContact(ctx, st.ID, uid, label); err != nil {
				log.Error("LinkContact", "error", err)
				h.send(from.ID, "Ученик создан, но не удалось привязать родителя.")
				h.dialog.ClearState(from.ID)
				return
			}
			h.send(from.ID, fmt.Sprintf("Ученик %q создан и родитель привязан.", st.DisplayName))
		} else {
			h.send(from.ID, fmt.Sprintf("Ученик %q создан.", st.DisplayName))
		}
		h.dialog.ClearState(from.ID)

	case StateAwaitingStudentRename:
		name := strings.TrimSpace(msg.Text)
		if name == "" {
			h.send(from.ID, "Имя не может быть пустым. Введите ещё раз или /cancel.")
			return
		}
		studentID, _ := state.Data["student_id"].(int64)
		if err := h.store.UpdateStudentName(ctx, studentID, name); err != nil {
			log.Error("UpdateStudentName", "student_id", studentID, "error", err)
			h.send(from.ID, "Не удалось сохранить.")
			return
		}
		h.dialog.ClearState(from.ID)
		h.send(from.ID, "Имя ученика обновлено.")

	case StateAwaitingIntervals:
		text := strings.TrimSpace(msg.Text)
		if text != "" {
			if _, err := config.ParseIntervals(text); err != nil {
				h.send(from.ID, "Неверный формат. Пример: 24h,2h,30m. Попробуйте ещё раз или /cancel.")
				return
			}
		}
		studentID, _ := state.Data["student_id"].(int64)
		if err := h.store.SetStudentIntervals(ctx, studentID, text); err != nil {
			log.Error("SetStudentIntervals", "error", err)
			h.send(from.ID, "Не удалось сохранить.")
			return
		}
		if text == "" {
			h.send(from.ID, "Интервалы сброшены на глобальные.")
		} else {
			h.send(from.ID, "Интервалы сохранены.")
		}
		h.dialog.ClearState(from.ID)

	case StateAwaitingGlobalIntervals:
		text := strings.TrimSpace(msg.Text)
		if _, err := config.ParseIntervals(text); err != nil {
			h.send(from.ID, "Неверный формат. Пример: 24h,2h,30m.")
			return
		}
		if err := h.store.SetSetting(ctx, "reminder_intervals", text); err != nil {
			log.Error("SetSetting", "error", err)
			h.send(from.ID, "Не удалось сохранить.")
			return
		}
		h.send(from.ID, "Глобальные интервалы обновлены.")
		h.dialog.ClearState(from.ID)
	}
}

// lookupParentName возвращает имя пользователя по ID или пустую строку.
// Чтобы не дёргать Store отдельно, делаем простой fallback "Родитель".
func (h *Handler) lookupParentName(ctx context.Context, userID int64) string {
	users, err := h.store.GetUnlinkedUsers(ctx)
	if err != nil {
		return "Родитель"
	}
	for _, u := range users {
		if u.ID == userID {
			return u.FullName
		}
	}
	return "Родитель"
}

// --- callback router --------------------------------------------------------

// handleCallback маршрутизирует inline-кнопки.
// Все callback'и теперь — действия преподавателя; родителю мы не показываем кнопок.
func (h *Handler) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	log := logger.FromContext(ctx)
	from := cb.From
	if !h.requireTeacher(from.ID) {
		h.answerCallback(cb.ID, "")
		return
	}
	if !h.rl.Allow(from.ID) {
		h.answerCallback(cb.ID, "Слишком много запросов")
		return
	}

	data := cb.Data
	parts := strings.SplitN(data, ":", 2)
	prefix := parts[0]
	rest := ""
	if len(parts) > 1 {
		rest = parts[1]
	}

	switch prefix {
	case cbCancel:
		h.dialog.ClearState(from.ID)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Отменено.", nil)
		h.answerCallback(cb.ID, "")

	case cbNewStudent:
		uid, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "Неверные данные")
			return
		}
		h.dialog.Set(from.ID, StateAwaitingStudentName, map[string]any{
			pendingNewParentKey: uid,
		})
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			"Введите имя нового ученика:", nil)
		h.answerCallback(cb.ID, "")

	case cbExistStudent:
		uid, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "Неверные данные")
			return
		}
		// Показываем список учеников.
		students, err := h.store.GetStudents(ctx)
		if err != nil {
			log.Error("GetStudents", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		if len(students) == 0 {
			h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
				"Учеников нет. Используйте «Новый ученик».", nil)
			h.answerCallback(cb.ID, "")
			return
		}
		choices := make([]studentChoice, 0, len(students))
		for _, s := range students {
			choices = append(choices, studentChoice{ID: s.ID, Name: s.DisplayName})
		}
		kb := kbStudents(cbLinkContact, uid, choices)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Выберите ученика:", &kb)
		h.answerCallback(cb.ID, "")

	case cbLinkContact:
		// формат: cbLinkContact:user_id:student_id
		args := strings.Split(rest, ":")
		if len(args) != 2 {
			h.answerCallback(cb.ID, "Неверные данные")
			return
		}
		uid, _ := strconv.ParseInt(args[0], 10, 64)
		sid, _ := strconv.ParseInt(args[1], 10, 64)
		label := h.lookupParentName(ctx, uid)
		if err := h.store.LinkContact(ctx, sid, uid, label); err != nil {
			log.Error("LinkContact", "error", err)
			h.answerCallback(cb.ID, "Не удалось привязать")
			return
		}
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Контакт привязан.", nil)
		h.answerCallback(cb.ID, "Готово")

	case cbStudentMenu:
		sid, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "")
			return
		}
		text, kb, err := h.studentDetails(ctx, sid)
		if err != nil {
			log.Error("studentDetails", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, text, &kb)
		h.answerCallback(cb.ID, "")

	case cbStudentContact:
		sid, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "")
			return
		}
		contacts, err := h.store.GetStudentContacts(ctx, sid)
		if err != nil {
			log.Error("GetStudentContacts", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		if len(contacts) == 0 {
			h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Контактов нет.", nil)
			h.answerCallback(cb.ID, "")
			return
		}
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(contacts))
		for _, c := range contacts {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					"Отвязать "+c.FullName,
					fmt.Sprintf("%s:%d:%d", cbUnlinkContact, sid, c.UserID),
				),
			))
		}
		kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Контакты ученика:", &kb)
		h.answerCallback(cb.ID, "")

	case cbUnlinkContact:
		// Подтверждение перед удалением.
		args := strings.Split(rest, ":")
		if len(args) != 2 {
			h.answerCallback(cb.ID, "")
			return
		}
		confirmCB := cbUnlinkContactC + ":" + rest
		kb := kbConfirm(confirmCB, cbCancel)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			"Точно отвязать контакт?", &kb)
		h.answerCallback(cb.ID, "")

	case cbUnlinkContactC:
		args := strings.Split(rest, ":")
		if len(args) != 2 {
			h.answerCallback(cb.ID, "")
			return
		}
		sid, _ := strconv.ParseInt(args[0], 10, 64)
		uid, _ := strconv.ParseInt(args[1], 10, 64)
		if err := h.store.UnlinkContact(ctx, sid, uid); err != nil && !errors.Is(err, store.ErrNotFound) {
			log.Error("UnlinkContact", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Контакт отвязан.", nil)
		h.answerCallback(cb.ID, "Готово")

	case cbUnlinkEvent:
		// Подтверждение.
		eid := rest
		confirmCB := cbUnlinkEventC + ":" + eid
		kb := kbConfirm(confirmCB, cbCancel)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			"Точно отвязать событие? Напоминания прекратятся.", &kb)
		h.answerCallback(cb.ID, "")

	case cbUnlinkEventC:
		eid := rest
		if err := h.store.UnlinkEvent(ctx, eid); err != nil && !errors.Is(err, store.ErrNotFound) {
			log.Error("UnlinkEvent", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Событие отвязано.", nil)
		h.answerCallback(cb.ID, "Готово")

	case cbPickEvent:
		// Пользователь выбрал событие — показываем учеников.
		// rest — короткий токен из eventtoken.go, не сам event ID.
		token := rest
		if _, ok := h.eventTokens.resolve(token); !ok {
			// Бот перезапускали после того, как список /events был показан —
			// токены не персистентные (см. eventtoken.go).
			h.answerCallback(cb.ID, "Список устарел, вызовите /events заново")
			return
		}
		students, err := h.store.GetStudents(ctx)
		if err != nil {
			log.Error("GetStudents", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		if len(students) == 0 {
			h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
				"Сначала создайте ученика.", nil)
			h.answerCallback(cb.ID, "")
			return
		}
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(students))
		for _, s := range students {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					s.DisplayName,
					// token, а не сам event ID — та же причина, что в /events.
					fmt.Sprintf("%s:%s:%d", cbPickStudent, token, s.ID),
				),
			))
		}
		kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			"Выберите ученика для события:", &kb)
		h.answerCallback(cb.ID, "")

	case cbPickStudent:
		// формат: cbPickStudent:token:student_id
		args := strings.Split(rest, ":")
		if len(args) != 2 {
			h.answerCallback(cb.ID, "Неверные данные")
			return
		}
		sid, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "Неверные данные")
			return
		}
		eid, ok := h.eventTokens.resolve(args[0])
		if !ok {
			h.answerCallback(cb.ID, "Список устарел, вызовите /events заново")
			return
		}
		if err := h.store.LinkEvent(ctx, eid, sid); err != nil {
			log.Error("LinkEvent", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Событие привязано.", nil)
		h.answerCallback(cb.ID, "Готово")
		// Привязка уже сохранена — уведомление родителей best-effort и не
		// должно ничего откатывать при ошибке (см. саму функцию).
		h.notifyContactsAboutNewEvent(ctx, sid, eid)

	case cbStopRemove:
		// Преподаватель подтвердил удаление родителя из контактов после
		// его /stop (см. handleStop в handler_parent.go и
		// admin.UI.NotifyStopRequest). Отвязываем от ВСЕХ учеников сразу —
		// в отличие от cbUnlinkContact, здесь это не точечное действие по
		// одному ученику, а полная очистка по инициативе самого родителя.
		uid, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "Неверные данные")
			return
		}
		students, err := h.store.GetStudentsByContact(ctx, uid)
		if err != nil {
			log.Error("GetStudentsByContact", "user_id", uid, "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		for _, s := range students {
			if err := h.store.UnlinkContact(ctx, s.ID, uid); err != nil && !errors.Is(err, store.ErrNotFound) {
				log.Error("UnlinkContact", "student_id", s.ID, "user_id", uid, "error", err)
			}
		}
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Родитель удалён из контактов.", nil)
		h.answerCallback(cb.ID, "Готово")

	case cbSetIntervals:
		sid, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "")
			return
		}
		h.dialog.Set(from.ID, StateAwaitingIntervals, map[string]any{"student_id": sid})
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			"Введите интервалы через запятую (24h,2h) или пустую строку для возврата к глобальным. /cancel для отмены.",
			nil)
		h.answerCallback(cb.ID, "")

	case cbRenameStudent:
		sid, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "")
			return
		}
		h.dialog.Set(from.ID, StateAwaitingStudentRename, map[string]any{"student_id": sid})
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			"Введите новое имя ученика, или /cancel для отмены.", nil)
		h.answerCallback(cb.ID, "")

	default:
		h.answerCallback(cb.ID, "")
	}
}

// studentDetails возвращает текстовое описание ученика и клавиатуру действий.
func (h *Handler) studentDetails(ctx context.Context, studentID int64) (string, tgbotapi.InlineKeyboardMarkup, error) {
	students, err := h.store.GetStudents(ctx)
	if err != nil {
		return "", tgbotapi.InlineKeyboardMarkup{}, err
	}
	var found *store.Student
	for i, s := range students {
		if s.ID == studentID {
			found = &students[i]
			break
		}
	}
	if found == nil {
		return "Ученик не найден.", tgbotapi.InlineKeyboardMarkup{}, nil
	}
	contacts, err := h.store.GetStudentContacts(ctx, studentID)
	if err != nil {
		return "", tgbotapi.InlineKeyboardMarkup{}, err
	}
	intervals := found.ReminderIntervals
	if intervals == "" {
		intervals = "(глобальные)"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Ученик: %s\nИнтервалы: %s\nКонтакты:\n", found.DisplayName, intervals)
	if len(contacts) == 0 {
		sb.WriteString("  (нет)\n")
	}
	for _, c := range contacts {
		fmt.Fprintf(&sb, "  • %s (%s)\n", c.FullName, c.Label)
	}
	return sb.String(), kbStudentMenu(studentID), nil
}

// notifyContactsAboutNewEvent уведомляет всех родителей ученика о том, что
// занятию назначено конкретное время — событие Google Calendar только что
// привязали к ученику через cbPickStudent. Родитель до этого момента мог
// вообще не знать, когда состоится занятие.
//
// Best-effort: сама привязка (store.LinkEvent) уже сохранена к моменту
// вызова этой функции, поэтому любая её собственная ошибка — только в лог,
// без отката привязки и без сообщения об ошибке преподавателю (тот уже
// увидел "Событие привязано").
func (h *Handler) notifyContactsAboutNewEvent(ctx context.Context, studentID int64, masterEventID string) {
	log := logger.FromContext(ctx)

	if h.dispatch == nil {
		return
	}

	contacts, err := h.store.GetStudentContacts(ctx, studentID)
	if err != nil {
		log.Error("GetStudentContacts (уведомление о привязке события)", "student_id", studentID, "error", err)
		return
	}
	if len(contacts) == 0 {
		return
	}

	student, err := h.findStudent(ctx, studentID)
	if err != nil {
		log.Error("findStudent (уведомление о привязке события)", "student_id", studentID, "error", err)
		return
	}

	// LinkEvent получает на входе только master_event_id — ни времени, ни
	// названия. У calendar.Client нет метода "получить одно событие по ID"
	// (см. internal/calendar/client.go), поэтому достаём их тем же способом,
	// что и /events — свежим списком UpcomingMasters.
	if h.calClient == nil {
		return
	}
	events, err := h.calClient.UpcomingMasters(ctx, h.cfg.GoogleCalendarID, upcomingHorizon)
	if err != nil {
		log.Error("UpcomingMasters (уведомление о привязке события)", "error", err)
		return
	}
	var ev *calendar.Event
	for i := range events {
		if events[i].ID == masterEventID {
			ev = &events[i]
			break
		}
	}
	if ev == nil {
		// Событие могло исчезнуть из ближайших upcomingHorizon между показом
		// /events и нажатием кнопки — редкий случай гонки, не повод падать.
		log.Warn("событие не найдено для уведомления о привязке", "master_event_id", masterEventID)
		return
	}

	text := fmt.Sprintf("Занятие у %s назначено:\n%s", student.DisplayName, formatEvent(ev.Start, ev.Summary))
	for _, c := range contacts {
		if err := h.dispatch.SendToUser(ctx, c.UserID, text); err != nil {
			log.Error("SendToUser (уведомление о привязке события)", "user_id", c.UserID, "error", err)
			continue
		}
		log.Info("уведомление о новом времени занятия отправлено", "student_id", studentID, "user_id", c.UserID)
	}
}

// findStudent возвращает ученика по ID. У Store нет прямого GetStudent(id) —
// только список целиком (GetStudents), поэтому ищем в нём же, как и
// studentDetails выше.
func (h *Handler) findStudent(ctx context.Context, studentID int64) (store.Student, error) {
	students, err := h.store.GetStudents(ctx)
	if err != nil {
		return store.Student{}, err
	}
	for _, s := range students {
		if s.ID == studentID {
			return s, nil
		}
	}
	return store.Student{}, store.ErrNotFound
}
