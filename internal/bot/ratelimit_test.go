package bot

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRateLimiter_AllowsWithinLimit(t *testing.T) {
	r := NewRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		assert.True(t, r.Allow(1), "i=%d", i)
	}
	assert.False(t, r.Allow(1), "4-й запрос должен быть отклонён")
}

func TestRateLimiter_DifferentUsersIndependent(t *testing.T) {
	r := NewRateLimiter(1, time.Minute)
	assert.True(t, r.Allow(1))
	assert.False(t, r.Allow(1))
	assert.True(t, r.Allow(2))
}

func TestRateLimiter_WindowExpires(t *testing.T) {
	r := NewRateLimiter(2, 100*time.Millisecond)

	// Подменяем clock.
	now := time.Unix(0, 0)
	r.nowFunc = func() time.Time { return now }

	assert.True(t, r.Allow(1))
	assert.True(t, r.Allow(1))
	assert.False(t, r.Allow(1))

	// Сдвигаем время за окно — отметки устаревают.
	now = now.Add(200 * time.Millisecond)
	assert.True(t, r.Allow(1))
}
