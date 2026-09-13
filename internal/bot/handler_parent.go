package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/booking-bot/booking-bot/internal/logger"
	"github.com/booking-bot/booking-bot/internal/store"
)

// handleStart — точка входа /start для родителей.
//
// Сценарий:
//  1. Rate limiter проверяет допустимость запроса.
//  2. Не зарегистрирован ли уже этот аккаунт?
//     - Да → отвечаем "вы уже зарегистрированы".
//  3. FSM → StateAwaitingName, бот просит ввести имя.
//  4. Родитель вводит имя → CreateUser + SaveAccount → уведомление преподавателю.
//  5. FSM → Idle.
func (h *Handler) handleStart(ctx context.Context, msg *tgbotapi.Message) {
	log := logger.FromContext(ctx)
	from := msg.From

	// Преподаватель регистрацию как родитель не проходит. Без этой проверки
	// /start поставил бы ему StateAwaitingName, а его следующий текстовый
	// ответ ушёл бы в handleTeacherMessage (маршрутизация по isTeacher в
	// handleUpdate) — там такого состояния нет, и он завис бы без ответа.
	if h.isTeacher(from.ID) {
		h.send(from.ID, "Вы вошли как преподаватель — регистрация родителя не нужна.")
		return
	}

	if !h.rl.Allow(from.ID) {
		log.Warn("rate limit", "user_id", from.ID)
		return
	}

	// Проверим, не зарегистрирован ли уже.
	if u, err := h.store.GetUserByAccount(ctx, MessengerName, externalID(from.ID)); err == nil {
		log.Info("user already registered", "user_id", u.ID)
		h.send(from.ID, "Вы уже зарегистрированы. Преподаватель свяжется с вами.")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		log.Error("проверка регистрации", "error", err)
		h.send(from.ID, "Произошла ошибка. Попробуйте позже.")
		return
	}

	h.dialog.Set(from.ID, StateAwaitingName, nil)
	h.send(from.ID, "Здравствуйте! Введите ваше имя и фамилию для регистрации.")
}

// handleParentMessage обрабатывает текстовое сообщение от потенциального родителя.
// Вызывается из главного цикла когда state != Idle и пользователь не преподаватель.
func (h *Handler) handleParentMessage(ctx context.Context, msg *tgbotapi.Message) {
	log := logger.FromContext(ctx)
	from := msg.From
	state := h.dialog.Get(from.ID)

	if !h.rl.Allow(from.ID) {
		return
	}

	switch state.State {
	case StateAwaitingName:
		name := strings.TrimSpace(msg.Text)
		if name == "" {
			h.send(from.ID, "Имя не может быть пустым. Введите имя и фамилию.")
			return
		}
		if len(name) > 100 {
			name = name[:100]
		}
		user, err := h.store.CreateUser(ctx, name)
		if err != nil {
			log.Error("CreateUser", "error", err)
			h.send(from.ID, "Не удалось сохранить. Попробуйте позже.")
			return
		}
		username := from.UserName
		if err := h.store.SaveAccount(ctx, user.ID, MessengerName, externalID(from.ID), username); err != nil {
			log.Error("SaveAccount", "user_id", user.ID, "error", err)
			h.send(from.ID, "Не удалось сохранить аккаунт. Попробуйте позже.")
			return
		}
		log.Info("user registered", "user_id", user.ID, "messenger", MessengerName)
		h.dialog.ClearState(from.ID)
		h.send(from.ID, "Спасибо! Преподаватель скоро свяжется с вами для подтверждения.")
		// Уведомляем преподавателя.
		if err := h.adminUI.NotifyNewUser(ctx, user.ID, name); err != nil {
			log.Error("уведомление преподавателя", "error", err)
		}

	default:
		// В любом другом состоянии для родителя — просто игнорируем,
		// чтобы бот не превращался в чат.
	}
}

// handleStop — команда /stop: родитель отказывается получать сообщения бота.
//
// Мы НЕ удаляем сразу связи с учениками — это затрагивает бизнес-данные
// (перестанут приходить напоминания конкретному человеку), и решение
// оставляем преподавателю. Вместо этого:
//  1. Немедленно деактивируем telegram-аккаунт — notify.Dispatcher берёт
//     только активные аккаунты (store.GetActiveAccounts), так что доставка
//     сообщений (включая напоминания планировщика) прекращается сразу же,
//     ещё до решения преподавателя.
//  2. Уведомляем преподавателя с кнопками [Удалить из контактов] [Оставить] —
//     см. admin.UI.NotifyStopRequest и cbStopRemove в handler_teacher.go.
//
// Если родитель передумает — /start реактивирует тот же аккаунт (см.
// комментарий у store.SaveAccount).
func (h *Handler) handleStop(ctx context.Context, msg *tgbotapi.Message) {
	log := logger.FromContext(ctx)
	from := msg.From

	if h.isTeacher(from.ID) {
		h.send(from.ID, "Команда /stop предназначена для родителей.")
		return
	}
	if !h.rl.Allow(from.ID) {
		return
	}

	user, err := h.store.GetUserByAccount(ctx, MessengerName, externalID(from.ID))
	if errors.Is(err, store.ErrNotFound) {
		h.send(from.ID, "Вы ещё не зарегистрированы.")
		return
	}
	if err != nil {
		log.Error("GetUserByAccount", "error", err)
		h.send(from.ID, "Произошла ошибка. Попробуйте позже.")
		return
	}

	if err := h.store.DeactivateAccount(ctx, MessengerName, externalID(from.ID)); err != nil {
		log.Error("DeactivateAccount", "user_id", user.ID, "error", err)
		h.send(from.ID, "Не удалось выполнить. Попробуйте позже.")
		return
	}

	h.send(from.ID,
		"Вы больше не будете получать сообщения от бота. "+
			"Если захотите возобновить — отправьте /start.")

	if err := h.adminUI.NotifyStopRequest(ctx, user.ID, user.FullName); err != nil {
		log.Error("уведомление преподавателя о /stop", "user_id", user.ID, "error", err)
	}
}

// --- "Мои ученики" (кнопка меню родителя) ------------------------------------

// handleMyStudents показывает родителю список привязанных к нему учеников.
func (h *Handler) handleMyStudents(ctx context.Context, msg *tgbotapi.Message) {
	log := logger.FromContext(ctx)
	from := msg.From

	if !h.rl.Allow(from.ID) {
		return
	}

	user, err := h.store.GetUserByAccount(ctx, MessengerName, externalID(from.ID))
	if errors.Is(err, store.ErrNotFound) {
		h.send(from.ID, "Вы ещё не зарегистрированы. Отправьте /start.")
		return
	}
	if err != nil {
		log.Error("GetUserByAccount", "error", err)
		h.send(from.ID, "Произошла ошибка. Попробуйте позже.")
		return
	}

	students, err := h.store.GetStudentsByContact(ctx, user.ID)
	if err != nil {
		log.Error("GetStudentsByContact", "error", err)
		h.send(from.ID, "Произошла ошибка. Попробуйте позже.")
		return
	}
	if len(students) == 0 {
		h.send(from.ID, "Пока не привязано ни одного ученика — преподаватель добавит вас позже.")
		return
	}

	var sb strings.Builder
	sb.WriteString("Ваши ученики:\n")
	for _, s := range students {
		fmt.Fprintf(&sb, "\u2022 %s\n", s.DisplayName)
	}
	h.send(from.ID, sb.String())
}
