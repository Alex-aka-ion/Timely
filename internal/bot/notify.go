package bot

import (
	"context"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/booking-bot/booking-bot/internal/admin"
	"github.com/booking-bot/booking-bot/internal/logger"
)

// TelegramAdminUI — реализация admin.UI поверх Telegram.
// Отправляет уведомления преподавателю с inline-кнопками для быстрых действий.
type TelegramAdminUI struct {
	api       *tgbotapi.BotAPI
	teacherID int64
}

// NewTelegramAdminUI создаёт UI, привязанный к конкретному telegram_id преподавателя.
func NewTelegramAdminUI(api *tgbotapi.BotAPI, teacherID int64) *TelegramAdminUI {
	return &TelegramAdminUI{api: api, teacherID: teacherID}
}

// статическая проверка интерфейса
var _ admin.UI = (*TelegramAdminUI)(nil)

// NotifyNewUser сообщает преподавателю о новом родителе.
// Кнопки: [Новый ученик] [К существующему].
//
// fullName приходит от пользователя — не логируем его, но отправлять преподавателю
// необходимо: преподаватель должен знать кто зарегистрировался.
func (u *TelegramAdminUI) NotifyNewUser(ctx context.Context, userID int64, fullName string) error {
	log := logger.FromContext(ctx)
	text := fmt.Sprintf("Новый родитель зарегистрировался: %s\nЧто делаем?", fullName)
	msg := tgbotapi.NewMessage(u.teacherID, text)
	msg.ReplyMarkup = kbNewUserChoice(userID)
	if _, err := u.api.Send(msg); err != nil {
		log.Error("notify new user", "user_id", userID, "error", err)
		return err
	}
	log.Info("notified teacher about new user", "user_id", userID)
	return nil
}

// NotifyError сообщает преподавателю о бизнес-ошибке.
// Использует только summary в логе — detail может содержать чувствительные данные.
func (u *TelegramAdminUI) NotifyError(ctx context.Context, summary, detail string) error {
	log := logger.FromContext(ctx)
	text := fmt.Sprintf("⚠️ %s\n\n%s", summary, detail)
	msg := tgbotapi.NewMessage(u.teacherID, text)
	if _, err := u.api.Send(msg); err != nil {
		log.Error("notify teacher about error", "summary", summary, "error", err)
		return err
	}
	return nil
}
