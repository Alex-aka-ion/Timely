package bot

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

// telegramAPI — узкий интерфейс над теми методами *tgbotapi.BotAPI, которые
// реально используют Handler, TelegramSender и TelegramAdminUI.
//
// Зачем он нужен: *tgbotapi.BotAPI — конкретный тип из сторонней библиотеки,
// его нельзя подменить фейком в юнит-тестах напрямую. Через этот интерфейс
// код пакета зависит не от реализации, а от контракта — тот же приём, что
// уже применён в проекте для Store, notify.Sender, calendar.Client и
// admin.UI (см. план проекта, раздел "Ключевые интерфейсы"). В тестах вместо
// реального клиента подставляется fakeTelegramAPI (см. *_test.go).
//
// Благодаря структурной типизации Go — *tgbotapi.BotAPI реализует
// telegramAPI автоматически, без единой лишней строчки кода: достаточно,
// что у него уже есть методы с такими же сигнатурами. В отличие от PHP,
// здесь не нужно явно писать "implements SomeInterface" — компилятор сам
// проверяет совместимость там, где значение присваивается переменной этого
// типа интерфейса.
type telegramAPI interface {
	Send(c tgbotapi.Chattable) (tgbotapi.Message, error)
	Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error)
	GetUpdatesChan(config tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel
	StopReceivingUpdates()
}

// Статическая проверка на этапе компиляции: если библиотека когда-нибудь
// поменяет сигнатуры этих методов, сборка сломается прямо здесь — с понятной
// ошибкой компиляции, а не необъяснимой паникой в проде.
var _ telegramAPI = (*tgbotapi.BotAPI)(nil)
