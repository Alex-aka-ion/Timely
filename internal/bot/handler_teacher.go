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

// pendingEventKey — ключ в Dialog.Data: токен события (eventtoken.go),
// которое привяжем к ученику сразу после его создания (см.
// cbNewStudentForEvent). Именно токен, а не сам master_event_id — событие
// уже показывалось через /events и токен для него выпущен, отдельного
// способа идентифицировать то же событие заводить не нужно.
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

// eventLabelOrUnlink возвращает подпись для кнопки конкретного привязанного
// события в карточке ученика. Использует GetEvent, а не UpcomingMasters:
// последний ограничен 14-дневным горизонтом, поэтому "не нашли событие в
// списке" не отличить от "событие вне горизонта" — GetEvent ищет по ID
// напрямую и, если календарь отвечает ErrEventNotFound, событие
// действительно удалено (или отменено целиком), а не просто далеко по
// времени.
//
// В этом случае отвязываем событие сразу, автоматически: показывать
// кнопку "Отвязать" для события, которого больше нет, бессмысленно, а
// молча оставлять висящую привязку — ровно то поведение, из-за которого
// эта функция появилась (баг-репорт: удалённое из календаря событие
// оставалось в списке ученика бессрочно). ok=false, если событие было
// отвязано — вызывающий код не должен показывать под него кнопку.
func (h *Handler) eventLabelOrUnlink(ctx context.Context, masterEventID string) (label string, ok bool) {
	log := logger.FromContext(ctx)
	if h.calClient == nil {
		return eventLabelFallback(masterEventID), true
	}
	ev, err := h.calClient.GetEvent(ctx, h.cfg.GoogleCalendarID, masterEventID)
	if errors.Is(err, calendar.ErrEventNotFound) {
		if uerr := h.store.UnlinkEvent(ctx, masterEventID); uerr != nil && !errors.Is(uerr, store.ErrNotFound) {
			log.Error("UnlinkEvent (автоочистка удалённого события)", "master_event_id", masterEventID, "error", uerr)
		}
		return "", false
	}
	if err != nil {
		// Календарь недоступен/ошибка сети — не трогаем привязку на основании
		// временного сбоя, показываем как есть.
		log.Error("GetEvent", "master_event_id", masterEventID, "error", err)
		return eventLabelFallback(masterEventID), true
	}
	return formatEvent(ev.Start, ev.Summary), true
}

// eventLabelFallback — подпись, когда узнать реальные время/название события
// не удалось (календарь не подключён или временно недоступен). Сокращённого
// ID достаточно, чтобы отличить одну кнопку от другой в списке.
func eventLabelFallback(masterEventID string) string {
	id := masterEventID
	if len(id) > 12 {
		id = id[:12] + "…"
	}
	return id
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
		} else if token, ok := state.Data[pendingEventKey].(string); ok {
			// Сценарий "выбор события в /events → новый ученик" (кнопка
			// "➕ Новый ученик" из cbNewStudentForEvent).
			eid, ok := h.eventTokens.resolve(token)
			if !ok {
				h.send(from.ID, fmt.Sprintf(
					"Ученик %q создан, но список событий устарел — привяжите его через /events заново.",
					st.DisplayName))
				h.dialog.ClearState(from.ID)
				return
			}
			if err := h.store.LinkEvent(ctx, eid, st.ID); err != nil {
				log.Error("LinkEvent", "error", err)
				h.send(from.ID, "Ученик создан, но не удалось привязать событие.")
				h.dialog.ClearState(from.ID)
				return
			}
			h.send(from.ID, fmt.Sprintf("Ученик %q создан и событие привязано.", st.DisplayName))
			h.dialog.ClearState(from.ID)
			// Привязка уже сохранена — уведомление родителей best-effort, как
			// и в cbPickStudent (см. саму функцию).
			h.notifyContactsAboutNewEvent(ctx, st.ID, eid)
			return
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
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(contacts)+1)
		for _, c := range contacts {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					"Отвязать "+c.FullName,
					fmt.Sprintf("%s:%d:%d", cbUnlinkContact, sid, c.UserID),
				),
			))
		}
		// Всегда доступна, даже когда контактов ещё нет — раньше это был
		// тупик ("Контактов нет." без единой кнопки): чтобы привязать
		// родителя, который уже зарегистрирован (например, водит к нам
		// второго ребёнка на другое время), не было вообще никакого пути в
		// интерфейсе, кроме как ждать, что он снова напишет боту /start.
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Добавить родителя", fmt.Sprintf("%s:%d", cbAddContact, sid)),
		))
		text := "Контакты ученика:"
		if len(contacts) == 0 {
			text = "Контактов нет."
		}
		kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, text, &kb)
		h.answerCallback(cb.ID, "")

	case cbAddContact:
		sid, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "")
			return
		}
		users, err := h.store.GetAllUsers(ctx)
		if err != nil {
			log.Error("GetAllUsers", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		existing, err := h.store.GetStudentContacts(ctx, sid)
		if err != nil {
			log.Error("GetStudentContacts", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		alreadyContact := make(map[int64]bool, len(existing))
		for _, c := range existing {
			alreadyContact[c.UserID] = true
		}
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(users))
		for _, u := range users {
			if alreadyContact[u.ID] {
				continue
			}
			// Пометка "какой ученик уже привязан" — весь смысл этого
			// экрана: отличить родителя, который уже водит к нам другого
			// ребёнка (и его для второго ребёнка нужно найти по имени
			// среди зарегистрированных, а не создавать заново), от
			// действительно нового контакта.
			note := "пока без учеников"
			if kids, err := h.store.GetStudentsByContact(ctx, u.ID); err == nil && len(kids) > 0 {
				names := make([]string, len(kids))
				for i, k := range kids {
					names[i] = k.DisplayName
				}
				note = "уже: " + strings.Join(names, ", ")
			}
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					fmt.Sprintf("%s (%s)", u.FullName, note),
					fmt.Sprintf("%s:%d:%d", cbLinkContact, u.ID, sid),
				),
			))
		}
		if len(rows) == 0 {
			h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Нет родителей, которых можно добавить.", nil)
			h.answerCallback(cb.ID, "")
			return
		}
		kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "Выберите родителя:", &kb)
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

	case cbStudentEvents:
		sid, err := strconv.ParseInt(rest, 10, 64)
		if err != nil {
			h.answerCallback(cb.ID, "")
			return
		}
		links, err := h.store.GetStudentEvents(ctx, sid)
		if err != nil {
			log.Error("GetStudentEvents", "error", err)
			h.answerCallback(cb.ID, "Ошибка")
			return
		}
		if len(links) == 0 {
			h.editText(cb.Message.Chat.ID, cb.Message.MessageID, "К ученику не привязано ни одного события.", nil)
			h.answerCallback(cb.ID, "")
			return
		}
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(links))
		var autoUnlinked int
		for _, l := range links {
			label, ok := h.eventLabelOrUnlink(ctx, l.MasterEventID)
			if !ok {
				autoUnlinked++
				continue
			}
			// token, а не сам event ID — та же причина, что в /events
			// (см. eventtoken.go): ID может быть длиннее 64-байтного лимита
			// callback_data.
			token := h.eventTokens.tokenFor(l.MasterEventID)
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					"Отвязать: "+label,
					cbUnlinkEvent+":"+token,
				),
			))
		}
		var prefix string
		if autoUnlinked > 0 {
			prefix = fmt.Sprintf("Удалённых из календаря событий отвязано автоматически: %d.\n\n", autoUnlinked)
		}
		if len(rows) == 0 {
			h.editText(cb.Message.Chat.ID, cb.Message.MessageID, prefix+"К ученику не привязано ни одного события.", nil)
			h.answerCallback(cb.ID, "")
			return
		}
		kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID, prefix+"События ученика:", &kb)
		h.answerCallback(cb.ID, "")

	case cbUnlinkEvent:
		// Подтверждение. rest — токен из eventtoken.go, не сам event ID.
		token := rest
		if _, ok := h.eventTokens.resolve(token); !ok {
			h.answerCallback(cb.ID, "Список устарел, откройте карточку ученика заново")
			return
		}
		confirmCB := cbUnlinkEventC + ":" + token
		kb := kbConfirm(confirmCB, cbCancel)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			"Точно отвязать событие? Напоминания прекратятся.", &kb)
		h.answerCallback(cb.ID, "")

	case cbUnlinkEventC:
		token := rest
		eid, ok := h.eventTokens.resolve(token)
		if !ok {
			h.answerCallback(cb.ID, "Список устарел, откройте карточку ученика заново")
			return
		}
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
		rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(students)+1)
		for _, s := range students {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					s.DisplayName,
					// token, а не сам event ID — та же причина, что в /events.
					fmt.Sprintf("%s:%s:%d", cbPickStudent, token, s.ID),
				),
			))
		}
		// Тем же токеном, что и cbPickStudent выше, — чтобы после создания
		// ученика привязать к нему именно это событие (см. pendingEventKey
		// и case StateAwaitingStudentName в handleTeacherMessage).
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("➕ Новый ученик", cbNewStudentForEvent+":"+token),
		))
		kb := tgbotapi.NewInlineKeyboardMarkup(rows...)
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			"Выберите ученика для события:", &kb)
		h.answerCallback(cb.ID, "")

	case cbNewStudentForEvent:
		token := rest
		if _, ok := h.eventTokens.resolve(token); !ok {
			h.answerCallback(cb.ID, "Список устарел, вызовите /events заново")
			return
		}
		h.dialog.Set(from.ID, StateAwaitingStudentName, map[string]any{
			pendingEventKey: token,
		})
		h.editText(cb.Message.Chat.ID, cb.Message.MessageID,
			"Введите имя нового ученика:", nil)
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

	text := fmt.Sprintf("Занятие у %s назначено:\n%s", student.DisplayName, ev.Start.Local().Format("Mon 02.01 в 15:04 MST"))
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
