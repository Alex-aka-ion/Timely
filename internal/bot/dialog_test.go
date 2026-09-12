package bot

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDialog_InitialIdle(t *testing.T) {
	d := NewDialog()
	assert.Equal(t, StateIdle, d.Get(42).State)
}

func TestDialog_SetGet(t *testing.T) {
	d := NewDialog()
	d.Set(42, StateAwaitingName, map[string]any{"foo": "bar"})

	e := d.Get(42)
	assert.Equal(t, StateAwaitingName, e.State)
	assert.Equal(t, "bar", e.Data["foo"])
}

func TestDialog_SetState_PreservesData(t *testing.T) {
	d := NewDialog()
	d.Set(42, StateAwaitingStudentName, map[string]any{"user_id": int64(7)})
	d.SetState(42, StateAwaitingIntervals)

	e := d.Get(42)
	assert.Equal(t, StateAwaitingIntervals, e.State)
	assert.EqualValues(t, 7, e.Data["user_id"])
}

func TestDialog_Clear(t *testing.T) {
	d := NewDialog()
	d.Set(42, StateAwaitingName, nil)
	d.ClearState(42)
	assert.Equal(t, StateIdle, d.Get(42).State)
}

func TestDialog_ConcurrentAccess(t *testing.T) {
	d := NewDialog()
	const N = 100
	var wg sync.WaitGroup
	wg.Add(N * 2)
	for i := 0; i < N; i++ {
		go func(id int64) {
			defer wg.Done()
			d.Set(id, StateAwaitingName, nil)
		}(int64(i))
		go func(id int64) {
			defer wg.Done()
			_ = d.Get(id)
		}(int64(i))
	}
	wg.Wait()
}
