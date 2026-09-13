// Package admin определяет интерфейс UI для уведомления преподавателя
// о бизнес-событиях системы.
//
// Реализации:
//   - bot.TelegramAdminUI — текущая, через inline-кнопки в Telegram.
//   - в будущем: WebAdminUI, MiniAppAdminUI и т.п. — без изменения бизнес-логики.
package admin

import "context"

// UI — интерфейс уведомления преподавателя.
type UI interface {
	// NotifyNewUser сигнализирует о регистрации нового родителя.
	// Реализация должна показать кнопки [Новый ученик] / [К существующему].
	NotifyNewUser(ctx context.Context, userID int64, fullName string) error

	// NotifyStopRequest сигнализирует, что родитель отправил /stop (больше
	// не хочет получать сообщения бота). Реализация должна показать кнопки
	// подтверждения — окончательно убрать родителя из контактов учеников
	// решает преподаватель, а не сам родитель одним нажатием.
	NotifyStopRequest(ctx context.Context, userID int64, fullName string) error

	// NotifyError используется бизнес-логикой для эскалации проблем,
	// которые требуют ручного вмешательства преподавателя.
	NotifyError(ctx context.Context, summary, detail string) error
}

// Noop — заглушка, не делает ничего. Удобна в тестах и при отключённом UI.
type Noop struct{}

func (Noop) NotifyNewUser(context.Context, int64, string) error     { return nil }
func (Noop) NotifyStopRequest(context.Context, int64, string) error { return nil }
func (Noop) NotifyError(context.Context, string, string) error      { return nil }
