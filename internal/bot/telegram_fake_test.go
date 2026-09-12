package bot

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

// fakeTelegramAPI — тестовая заглушка telegramAPI. Ничего никуда не
// отправляет по сети, только запоминает вызовы, чтобы тест мог их проверить.
//
// Используется вместо nil в тестах хэндлеров: раньше makeHandler оставлял
// API нулевым в надежде, что до него "не дойдёт" — но requireTeacher всё
// равно отвечает на callback (answerCallback) даже для заблокированных
// пользователей, потому что так требует протокол Telegram (иначе кнопка у
// пользователя в клиенте виснет с крутящимся индикатором). Из-за этого вызов
// уходил в h.api.Request на nil-указателе и падал паникой.
type fakeTelegramAPI struct {
	sent     []tgbotapi.Chattable
	answered []tgbotapi.Chattable
}

func (f *fakeTelegramAPI) Send(c tgbotapi.Chattable) (tgbotapi.Message, error) {
	f.sent = append(f.sent, c)
	return tgbotapi.Message{}, nil
}

func (f *fakeTelegramAPI) Request(c tgbotapi.Chattable) (*tgbotapi.APIResponse, error) {
	f.answered = append(f.answered, c)
	return &tgbotapi.APIResponse{Ok: true}, nil
}

func (f *fakeTelegramAPI) GetUpdatesChan(tgbotapi.UpdateConfig) tgbotapi.UpdatesChannel {
	return make(tgbotapi.UpdatesChannel)
}

func (f *fakeTelegramAPI) StopReceivingUpdates() {}

var _ telegramAPI = (*fakeTelegramAPI)(nil)
