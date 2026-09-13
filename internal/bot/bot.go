package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/booking-bot/booking-bot/internal/admin"
	"github.com/booking-bot/booking-bot/internal/calendar"
	"github.com/booking-bot/booking-bot/internal/config"
	"github.com/booking-bot/booking-bot/internal/logger"
	"github.com/booking-bot/booking-bot/internal/notify"
	"github.com/booking-bot/booking-bot/internal/store"
)

// Handler — главный обработчик апдейтов Telegram.
//
// Содержит все зависимости (store, calendar, dispatcher, FSM, лимитер).
// Управление событиями делится по handler_parent / handler_teacher.
type Handler struct {
	api         telegramAPI
	botUsername string
	cfg         *config.Config
	store       store.Store
	dialog      *Dialog
	rl          *RateLimiter
	dispatch    *notify.Dispatcher
	adminUI     admin.UI
	calClient   calendar.Client
	eventTokens *eventTokens
}

// HandlerDeps — все зависимости Handler.
type HandlerDeps struct {
	API         telegramAPI
	BotUsername string
	Cfg         *config.Config
	Store       store.Store
	Dispatch    *notify.Dispatcher
	AdminUI     admin.UI
	CalClient   calendar.Client
}

// NewHandler создаёт Handler с типичными значениями rate-limiter (5/мин).
func NewHandler(d HandlerDeps) *Handler {
	return &Handler{
		api:         d.API,
		botUsername: d.BotUsername,
		cfg:         d.Cfg,
		store:       d.Store,
		dialog:      NewDialog(),
		rl:          NewRateLimiter(5, time.Minute),
		dispatch:    d.Dispatch,
		adminUI:     d.AdminUI,
		calClient:   d.CalClient,
		eventTokens: newEventTokens(),
	}
}

// Run запускает long-polling до отмены контекста.
func (h *Handler) Run(ctx context.Context) error {
	log := logger.FromContext(ctx)
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 30
	updates := h.api.GetUpdatesChan(u)
	defer h.api.StopReceivingUpdates()

	log.Info("bot started", "username", h.botUsername)

	for {
		select {
		case <-ctx.Done():
			log.Info("bot stopping")
			return nil
		case upd, ok := <-updates:
			if !ok {
				return nil
			}
			h.handleUpdate(ctx, upd)
		}
	}
}

// handleUpdate — диспетчер апдейтов: маршрутизирует на нужный хэндлер.
func (h *Handler) handleUpdate(ctx context.Context, upd tgbotapi.Update) {
	log := logger.FromContext(ctx)

	switch {
	case upd.CallbackQuery != nil:
		h.handleCallback(ctx, upd.CallbackQuery)
		return
	case upd.Message == nil:
		return
	}

	msg := upd.Message
	from := msg.From
	if from == nil {
		return
	}

	// Логируем без имён и username — только технические идентификаторы.
	log = log.With("user_id", from.ID, "messenger", MessengerName)
	ctx = logger.WithContext(ctx, log)

	// Команды
	if msg.IsCommand() {
		switch msg.Command() {
		case "start":
			h.handleStart(ctx, msg)
		case "students":
			h.handleStudents(ctx, msg)
		case "students_new":
			h.handleStudentsNew(ctx, msg)
		case "unlinked":
			h.handleUnlinked(ctx, msg)
		case "events":
			h.handleEvents(ctx, msg)
		case "settings":
			h.handleSettings(ctx, msg)
		case "cancel":
			// ВАЖНО: это единственное реальное место для /cancel. Любое
			// сообщение вида "/слово" — это msg.IsCommand()==true, поэтому
			// если тут не завести case, оно уйдёт в default и потеряется,
			// так и не дойдя до handleTeacherMessage (там раньше была
			// проверка на тот же текст, которая из-за этого никогда не
			// срабатывала, — убрана).
			h.dialog.ClearState(from.ID)
			h.send(from.ID, "Действие отменено.")
		default:
			// Неизвестная команда — игнорируем.
		}
		return
	}

	// Кнопки постоянного меню (ReplyKeyboardMarkup, см. keyboard.go) — это не
	// команды Telegram, а обычные текстовые сообщения с текстом кнопки.
	// Проверяем их здесь, ДО маршрутизации по состоянию диалога: иначе,
	// например, нажатие "📅 События" во время ввода имени ученика
	// было бы воспринято как это самое имя.
	if action, ok := h.menuAction(from.ID, msg.Text); ok {
		action(ctx, msg)
		return
	}

	// Текстовые сообщения — обрабатываются по состоянию диалога.
	if h.isTeacher(from.ID) {
		h.handleTeacherMessage(ctx, msg)
		return
	}
	h.handleParentMessage(ctx, msg)
}

// isTeacher возвращает true если пользователь — преподаватель.
// Роль определяется по telegram_id из конфига.
func (h *Handler) isTeacher(userID int64) bool {
	return userID == h.cfg.TeacherTelegramID
}

// requireTeacher молча игнорирует не-преподавательские запросы.
// Используется в начале каждого teacher-хэндлера.
func (h *Handler) requireTeacher(userID int64) bool {
	return h.isTeacher(userID)
}

// send отправляет простое текстовое сообщение пользователю.
// Ошибки логируются и проглатываются (нет смысла их прокидывать наверх).
//
// К каждому такому сообщению прикрепляем постоянное меню (ReplyKeyboardMarkup):
// Telegram показывает такую клавиатуру внизу экрана и держит её там, пока не
// придёт другая, — то есть достаточно один раз прислать её с любым сообщением,
// и она останется у пользователя даже под сообщениями, где reply_markup не
// задан (например, инлайн-кнопки в /events — независимый слой поверх
// конкретного сообщения, а не замена нижнего меню).
func (h *Handler) send(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = h.menuKeyboard(chatID)
	if _, err := h.api.Send(msg); err != nil {
		// Логируем без слога-контекста (тут его нет) — простой stderr.
		fmt.Println("telegram send error:", err)
	}
}

// menuKeyboard возвращает постоянную клавиатуру для роли пользователя этого
// чата. Для приватных чатов chat_id == user_id (см. externalID ниже), так что
// isTeacher можно применить прямо к chatID.
func (h *Handler) menuKeyboard(chatID int64) tgbotapi.ReplyKeyboardMarkup {
	if h.isTeacher(chatID) {
		return menuKeyboardTeacher()
	}
	return menuKeyboardParent()
}

// menuAction сопоставляет текст сообщения кнопке постоянного меню (если это
// она) и возвращает обработчик для неё. Кнопки меню — алиасы существующих
// команд: например, "👥 Ученики" делает то же самое, что и /students,
// просто её не нужно печатать.
//
// Возвращаем func(...), а не сразу вызываем нужный handleXxx — так вызывающий
// код (handleUpdate) сам решает, когда его позвать, единообразно с остальными
// ветками диспетчеризации. В PHP ближайший аналог — вернуть callable/Closure
// вместо результата его вызова.
func (h *Handler) menuAction(userID int64, text string) (func(context.Context, *tgbotapi.Message), bool) {
	if h.isTeacher(userID) {
		switch text {
		case btnStudents:
			return h.runMenuAction(userID, h.handleStudents), true
		case btnStudentsNew:
			return h.runMenuAction(userID, h.handleStudentsNew), true
		case btnUnlinked:
			return h.runMenuAction(userID, h.handleUnlinked), true
		case btnEvents:
			return h.runMenuAction(userID, h.handleEvents), true
		case btnSettings:
			return h.runMenuAction(userID, h.handleSettings), true
		}
		return nil, false
	}
	if text == btnMyStudents {
		return h.runMenuAction(userID, h.handleMyStudents), true
	}
	return nil, false
}

// runMenuAction оборачивает обработчик кнопки меню: переход в другой раздел
// меню всегда отменяет незавершённый диалог (ввод имени ученика, интервалов
// и т.п.) — так же, как это делает команда /cancel.
func (h *Handler) runMenuAction(userID int64, fn func(context.Context, *tgbotapi.Message)) func(context.Context, *tgbotapi.Message) {
	return func(ctx context.Context, msg *tgbotapi.Message) {
		h.dialog.ClearState(userID)
		fn(ctx, msg)
	}
}

// answerCallback отвечает на callback-кнопку (Telegram требует "ответить" на callback).
func (h *Handler) answerCallback(id, text string) {
	cb := tgbotapi.NewCallback(id, text)
	if _, err := h.api.Request(cb); err != nil {
		fmt.Println("answer callback:", err)
	}
}

// editText обновляет текст сообщения с inline-клавиатурой.
func (h *Handler) editText(chatID int64, msgID int, text string, kb *tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	if kb != nil {
		edit.ReplyMarkup = kb
	}
	if _, err := h.api.Send(edit); err != nil {
		fmt.Println("edit message:", err)
	}
}

// externalID — Telegram chat_id всегда совпадает с user_id для приватных чатов.
func externalID(id int64) string { return strconv.FormatInt(id, 10) }

// parseCallback разбирает callback_data вида "<префикс>:<id>[:<id2>]".
// Возвращает префикс и до 2-х численных аргументов.
func parseCallback(data string) (prefix string, args []int64, ok bool) {
	parts := strings.Split(data, ":")
	if len(parts) == 0 {
		return "", nil, false
	}
	prefix = parts[0]
	args = make([]int64, 0, len(parts)-1)
	for _, p := range parts[1:] {
		// Префиксы вроде master_event_id могут содержать строку, но мы
		// обрабатываем такие случаи в специализированных хэндлерах.
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return prefix, nil, false
		}
		args = append(args, n)
	}
	return prefix, args, true
}
