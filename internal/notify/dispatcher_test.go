package notify

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/booking-bot/booking-bot/internal/store"
)

// fakeSender — простая реализация Sender для тестов.
type fakeSender struct {
	mu   sync.Mutex
	name string
	err  error
	sent []string // external_id куда отправляли
}

func (f *fakeSender) Messenger() string { return f.name }
func (f *fakeSender) Send(_ context.Context, externalID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, externalID)
	return nil
}

// fakeStore возвращает заранее заданный список аккаунтов.
type fakeStore struct {
	store.Store
	accounts []store.MessengerAccount
	err      error
}

func (f *fakeStore) GetActiveAccounts(_ context.Context, _ int64) ([]store.MessengerAccount, error) {
	return f.accounts, f.err
}

func TestDispatcher_AllSendersUsed(t *testing.T) {
	tg := &fakeSender{name: "telegram"}
	wa := &fakeSender{name: "whatsapp"}
	st := &fakeStore{accounts: []store.MessengerAccount{
		{ID: 1, UserID: 7, Messenger: "telegram", ExternalID: "tg-1", IsActive: true},
		{ID: 2, UserID: 7, Messenger: "whatsapp", ExternalID: "wa-1", IsActive: true},
	}}

	d := NewDispatcher(st, tg, wa)
	require.NoError(t, d.SendToUser(context.Background(), 7, "hi"))
	assert.Equal(t, []string{"tg-1"}, tg.sent)
	assert.Equal(t, []string{"wa-1"}, wa.sent)
}

func TestDispatcher_PartialFailure(t *testing.T) {
	tg := &fakeSender{name: "telegram", err: errors.New("network")}
	wa := &fakeSender{name: "whatsapp"}
	st := &fakeStore{accounts: []store.MessengerAccount{
		{Messenger: "telegram", ExternalID: "tg-1"},
		{Messenger: "whatsapp", ExternalID: "wa-1"},
	}}
	d := NewDispatcher(st, tg, wa)
	// Один доставил — успех.
	assert.NoError(t, d.SendToUser(context.Background(), 7, "hi"))
}

func TestDispatcher_AllFail(t *testing.T) {
	tg := &fakeSender{name: "telegram", err: errors.New("a")}
	wa := &fakeSender{name: "whatsapp", err: errors.New("b")}
	st := &fakeStore{accounts: []store.MessengerAccount{
		{Messenger: "telegram", ExternalID: "tg-1"},
		{Messenger: "whatsapp", ExternalID: "wa-1"},
	}}
	d := NewDispatcher(st, tg, wa)
	err := d.SendToUser(context.Background(), 7, "hi")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "telegram")
	assert.Contains(t, err.Error(), "whatsapp")
}

func TestDispatcher_NoAccounts(t *testing.T) {
	d := NewDispatcher(&fakeStore{accounts: nil})
	err := d.SendToUser(context.Background(), 7, "hi")
	assert.Error(t, err)
}

func TestDispatcher_MissingSender(t *testing.T) {
	st := &fakeStore{accounts: []store.MessengerAccount{
		{Messenger: "viber", ExternalID: "vb-1"},
	}}
	d := NewDispatcher(st) // нет Sender для viber
	err := d.SendToUser(context.Background(), 7, "hi")
	// Никто не доставил — ошибка.
	assert.Error(t, err)
}

func TestDispatcher_StoreError(t *testing.T) {
	d := NewDispatcher(&fakeStore{err: errors.New("db down")})
	err := d.SendToUser(context.Background(), 7, "hi")
	assert.Error(t, err)
}

func TestDispatcher_RegisterAfterCreation(t *testing.T) {
	d := NewDispatcher(&fakeStore{accounts: []store.MessengerAccount{
		{Messenger: "telegram", ExternalID: "tg-1"},
	}})
	tg := &fakeSender{name: "telegram"}
	d.Register(tg)
	require.NoError(t, d.SendToUser(context.Background(), 7, "hi"))
	assert.Len(t, tg.sent, 1)
}

func TestDispatcher_NilSenderIgnored(t *testing.T) {
	d := NewDispatcher(&fakeStore{}, nil)
	d.Register(nil)
	// Не должно паниковать.
	_ = d
}

// Sanity: контекст с дедлайном корректно прокидывается (не зависает).
func TestDispatcher_ContextRespected(t *testing.T) {
	tg := &fakeSender{name: "telegram"}
	st := &fakeStore{accounts: []store.MessengerAccount{
		{Messenger: "telegram", ExternalID: "tg-1"},
	}}
	d := NewDispatcher(st, tg)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, d.SendToUser(ctx, 7, "hi"))
}
