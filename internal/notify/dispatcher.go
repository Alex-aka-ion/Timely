// Package notify абстрагирует отправку сообщений в произвольный мессенджер.
//
// Sender — интерфейс конкретного мессенджера (Telegram, WhatsApp, Viber).
// Dispatcher — оркестратор: получает пользователя, находит все его активные
// аккаунты, отправляет в каждый через соответствующий Sender.
//
// Бизнес-логика (планировщик, бот) работает только с Dispatcher и Sender —
// никаких прямых зависимостей от Telegram API.
package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/booking-bot/booking-bot/internal/logger"
	"github.com/booking-bot/booking-bot/internal/store"
)

// Sender отправляет сообщения в один конкретный мессенджер.
type Sender interface {
	// Messenger возвращает имя мессенджера (например "telegram").
	// Должно совпадать со значением, сохранённым в messenger_accounts.messenger.
	Messenger() string

	// Send отправляет текстовое сообщение по external_id мессенджера.
	Send(ctx context.Context, externalID, text string) error
}

// Dispatcher маршрутизирует сообщения между Sender'ами.
type Dispatcher struct {
	store   store.Store
	senders map[string]Sender
}

// NewDispatcher создаёт диспетчер с зарегистрированными Sender'ами.
// Sender'ы индексируются по их Messenger() имени.
func NewDispatcher(s store.Store, senders ...Sender) *Dispatcher {
	d := &Dispatcher{store: s, senders: make(map[string]Sender, len(senders))}
	for _, sd := range senders {
		if sd == nil {
			continue
		}
		d.senders[sd.Messenger()] = sd
	}
	return d
}

// Register добавляет Sender в диспетчер. Удобно для динамической регистрации.
func (d *Dispatcher) Register(s Sender) {
	if s == nil {
		return
	}
	d.senders[s.Messenger()] = s
}

// SendToUser доставляет сообщение через все активные аккаунты пользователя.
//
// Возвращает ошибку только если ВСЕ Sender'ы упали или пользователь
// не имеет активных аккаунтов. Если хоть один доставил — успех.
//
// Логирует предупреждение если для мессенджера нет зарегистрированного Sender.
func (d *Dispatcher) SendToUser(ctx context.Context, userID int64, text string) error {
	log := logger.FromContext(ctx)

	accounts, err := d.store.GetActiveAccounts(ctx, userID)
	if err != nil {
		return fmt.Errorf("получить аккаунты пользователя: %w", err)
	}
	if len(accounts) == 0 {
		return fmt.Errorf("у пользователя %d нет активных аккаунтов", userID)
	}

	var (
		successes int
		errs      []error
	)
	for _, acc := range accounts {
		sd, ok := d.senders[acc.Messenger]
		if !ok {
			// Для мессенджера нет зарегистрированного Sender — предупреждение,
			// но не критическая ошибка: возможно, поддержка добавится позже.
			log.Warn("нет Sender для мессенджера",
				"user_id", userID, "messenger", acc.Messenger)
			continue
		}
		if err := sd.Send(ctx, acc.ExternalID, text); err != nil {
			log.Error("ошибка отправки",
				"user_id", userID, "messenger", acc.Messenger, "error", err)
			errs = append(errs, fmt.Errorf("%s: %w", acc.Messenger, err))
			continue
		}
		successes++
	}

	if successes > 0 {
		return nil
	}
	if len(errs) == 0 {
		return fmt.Errorf("у пользователя %d нет аккаунтов в поддерживаемых мессенджерах", userID)
	}
	return errors.Join(errs...)
}
