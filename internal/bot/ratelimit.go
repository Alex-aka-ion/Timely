package bot

import (
	"sync"
	"time"
)

// RateLimiter — простой in-memory лимитер на скользящем окне.
// Не более N сообщений в Window на одного пользователя.
//
// Достаточно для старта; при росте нагрузки можно заменить на Redis-based
// без изменения интерфейса.
type RateLimiter struct {
	mu       sync.Mutex
	limit    int
	window   time.Duration
	buckets  map[int64][]time.Time
	nowFunc  func() time.Time // для тестов
	lastTrim time.Time
}

// NewRateLimiter — limit=5, window=1m по умолчанию.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		limit:   limit,
		window:  window,
		buckets: make(map[int64][]time.Time),
		nowFunc: time.Now,
	}
}

// Allow возвращает true если запрос можно обработать.
func (r *RateLimiter) Allow(userID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.nowFunc()
	cutoff := now.Add(-r.window)

	// Периодическая чистка пустых бакетов чтобы карта не росла бесконечно.
	if now.Sub(r.lastTrim) > r.window {
		for id, ts := range r.buckets {
			if len(ts) == 0 || ts[len(ts)-1].Before(cutoff) {
				delete(r.buckets, id)
			}
		}
		r.lastTrim = now
	}

	bucket := r.buckets[userID]
	// Удаляем устаревшие отметки.
	i := 0
	for i < len(bucket) && bucket[i].Before(cutoff) {
		i++
	}
	bucket = bucket[i:]

	if len(bucket) >= r.limit {
		r.buckets[userID] = bucket
		return false
	}
	bucket = append(bucket, now)
	r.buckets[userID] = bucket
	return true
}
