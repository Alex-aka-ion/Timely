// Package bot реализует Telegram-бота: регистрация родителей и UI преподавателя.
//
// dialog.go — конечный автомат состояний диалога с пользователем.
// Состояние хранится в памяти; при перезапуске бота все диалоги
// сбрасываются в Idle (и пользователь должен начать заново — это допустимо).
package bot

import "sync"

// State — текущее состояние диалога с пользователем.
type State int

const (
	StateIdle State = iota

	// Родитель в процессе регистрации, ждём имя.
	StateAwaitingName

	// Преподаватель в процессе создания нового ученика — ждём имя ученика.
	// Контекст: к какому новому пользователю прикрепляем — хранится в Data.
	StateAwaitingStudentName

	// Преподаватель в процессе изменения интервалов напоминаний для ученика.
	// Data содержит ID ученика.
	StateAwaitingIntervals

	// Преподаватель в процессе изменения глобальных настроек напоминаний.
	StateAwaitingGlobalIntervals

	// Родитель в процессе изменения собственного ФИО (/rename или кнопка
	// меню "Изменить имя"). В отличие от StateAwaitingName (первичная
	// регистрация — создаёт нового User) здесь пользователь уже существует,
	// меняем только имя (store.UpdateUserName).
	StateAwaitingNewName
)

// Entry — состояние одного пользователя.
type Entry struct {
	State State
	Data  map[string]any // произвольный контекст: например, pending_user_id
}

// Dialog — потокобезопасное хранилище состояний диалогов.
type Dialog struct {
	mu     sync.Mutex
	states map[int64]Entry
}

// NewDialog создаёт пустое хранилище состояний.
func NewDialog() *Dialog {
	return &Dialog{states: make(map[int64]Entry)}
}

// Get возвращает текущее состояние пользователя или StateIdle если нет записи.
func (d *Dialog) Get(userID int64) Entry {
	d.mu.Lock()
	defer d.mu.Unlock()
	e, ok := d.states[userID]
	if !ok {
		return Entry{State: StateIdle}
	}
	return e
}

// Set устанавливает состояние и контекст.
func (d *Dialog) Set(userID int64, state State, data map[string]any) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if data == nil {
		data = make(map[string]any)
	}
	d.states[userID] = Entry{State: state, Data: data}
}

// SetState меняет только состояние, сохраняя данные.
func (d *Dialog) SetState(userID int64, state State) {
	d.mu.Lock()
	defer d.mu.Unlock()
	e := d.states[userID]
	e.State = state
	if e.Data == nil {
		e.Data = make(map[string]any)
	}
	d.states[userID] = e
}

// ClearState возвращает пользователя в StateIdle и очищает данные.
func (d *Dialog) ClearState(userID int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.states, userID)
}
