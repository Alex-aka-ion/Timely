package bot

import (
	"context"
	"errors"
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
