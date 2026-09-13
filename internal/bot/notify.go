package bot

import (
	"context"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/booking-bot/booking-bot/internal/admin"
	"github.com/booking-bot/booking-bot/internal/logger"
)

// TelegramAdminUI — реализация admin.UI поверх Telegram.
// Отправляет уведомления преподавателю (и, если задан, разработчику — см.
// recipientIDs) с inline-кнопками для быстрых действий.
type TelegramAdminUI struct {
	api          telegramAPI
	recipientIDs []int64
}

// NewTelegramAdminUI создаёт UI, рассылающий уведомления всем перечисленным
// telegram_id сразу — обычно преподавателю и, опционально, разработчику
// (Config.DevTelegramID), который подключается параллельно с теми же
// правами, чтобы видеть происходящее и при необходимости сам нажимать
// кнопки в уведомлениях (isTeacher в bot.go пускает его в те же
// callback-обработчики). ID ⩽ 0 пропускаются — благодаря этому
// DevTelegramID можно передавать напрямую из Config, не проверяя заранее,
// задан ли он.
func NewTelegramAdminUI(api telegramAPI, recipientIDs ...int64) *TelegramAdminUI {
	ids := make([]int64, 0, len(recipientIDs))
	for _, id := range recipientIDs {
		if id > 0 {
			ids = append(ids, id)
		}
	}
	return &TelegramAdminUI{api: api, recipientIDs: ids}
}

// статическая проверка интерфейса
var _ admin.UI = (*TelegramAdminUI)(nil)

// send рассылает одно сообщение всем получателям. Best-effort: ошибка
// отправки одному получателю не должна мешать остальным получить своё —
// поэтому не прерываемся на первой ошибке, а после рассылки всем
// возвращаем последнюю (если она была).
func (u *TelegramAdminUI) send(text string, kb *tgbotapi.InlineKeyboardMarkup) error {
	var lastErr error
	for _, id := range u.recipientIDs {
		msg := tgbotapi.NewMessage(id, text)
		if kb != nil {
			msg.ReplyMarkup = *kb
		}
		if _, err := u.api.Send(msg); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// NotifyNewUser сообщает о новом родителе.
// Кнопки: [Новый ученик] [К существующему].
//
// fullName приходит от пользователя — не логируем его, но отправлять
// получателям необходимо: они должны знать кто зарегистрировался.
func (u *TelegramAdminUI) NotifyNewUser(ctx context.Context, userID int64, fullName string) error {
	log := logger.FromContext(ctx)
	text := fmt.Sprintf("Новый родитель зарегистрировался: %s\nЧто делаем?", fullName)
	kb := kbNewUserChoice(userID)
	if err := u.send(text, &kb); err != nil {
		log.Error("notify new user", "user_id", userID, "error", err)
		return err
	}
	log.Info("notified teacher about new user", "user_id", userID)
	return nil
}

// NotifyStopRequest сообщает, что родитель отправил /stop и перестал
// получать сообщения бота. Кнопки: [Удалить из контактов] [Оставить] —
// решение убрать родителя из контактов учеников остаётся за преподавателем
// (или разработчиком с теми же правами), сам /stop только отключает
// доставку сообщений этому конкретному аккаунту.
func (u *TelegramAdminUI) NotifyStopRequest(ctx context.Context, userID int64, fullName string) error {
	log := logger.FromContext(ctx)
	text := fmt.Sprintf(
		"Родитель %s отправил /stop и больше не получает сообщения бота (включая напоминания).\n"+
			"Удалить его из контактов учеников?", fullName)
	kb := kbStopRequest(userID)
	if err := u.send(text, &kb); err != nil {
		log.Error("notify stop request", "user_id", userID, "error", err)
		return err
	}
	log.Info("notified teacher about stop request", "user_id", userID)
	return nil
}

// NotifyError сообщает о бизнес-ошибке.
// Использует только summary в логе — detail может содержать чувствительные данные.
func (u *TelegramAdminUI) NotifyError(ctx context.Context, summary, detail string) error {
	log := logger.FromContext(ctx)
	text := fmt.Sprintf("⚠️ %s\n\n%s", summary, detail)
	if err := u.send(text, nil); err != nil {
		log.Error("notify teacher about error", "summary", summary, "error", err)
		return err
	}
	return nil
}
