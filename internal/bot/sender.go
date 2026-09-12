package bot

import (
	"context"
	"fmt"
	"strconv"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/booking-bot/booking-bot/internal/notify"
)

// MessengerName — значение для messenger_accounts.messenger.
const MessengerName = "telegram"

// TelegramSender реализует notify.Sender поверх Telegram Bot API.
type TelegramSender struct {
	api telegramAPI
}

// NewTelegramSender оборачивает существующий *tgbotapi.BotAPI в Sender.
func NewTelegramSender(api telegramAPI) *TelegramSender {
	return &TelegramSender{api: api}
}

// статическая проверка реализации интерфейса.
var _ notify.Sender = (*TelegramSender)(nil)

// Messenger возвращает имя мессенджера.
func (s *TelegramSender) Messenger() string { return MessengerName }

// Send отправляет текст в чат с указанным external_id (chat_id Telegram).
func (s *TelegramSender) Send(_ context.Context, externalID, text string) error {
	chatID, err := strconv.ParseInt(externalID, 10, 64)
	if err != nil {
		return fmt.Errorf("неверный external_id %q: %w", externalID, err)
	}
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := s.api.Send(msg); err != nil {
		return fmt.Errorf("telegram send: %w", err)
	}
	return nil
}
