package ratelimit

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/time/rate"
)

// Limiter is the rate limiting abstraction. It returns (allowed, retryAfter, error).
type Limiter interface {
	Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error)
}

// RedisLimiter implements a sliding-window rate limiter backed by Redis using a Lua script.
type RedisLimiter struct {
	client *redis.Client
}

// NewRedisLimiter creates a Redis-backed limiter.
func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{client: client}
}

// slidingWindowScript atomically removes stale entries, counts, and adds the new entry.
var slidingWindowScript = redis.NewScript(`
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local member = ARGV[4]

local cutoff = now - window
redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)
local count = redis.call('ZCARD', key)
if count < limit then
    redis.call('ZADD', key, now, member)
    redis.call('PEXPIRE', key, window)
    return {1, limit - count - 1, 0}
end
local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
local oldest_ms = tonumber(oldest[2])
return {0, 0, oldest_ms}
`)

func (r *RedisLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	now := time.Now()
	nowMS := now.UnixMilli()
	windowMS := window.Milliseconds()
	member := fmt.Sprintf("%d", nowMS)

	res, err := slidingWindowScript.Run(ctx, r.client,
		[]string{key},
		nowMS, windowMS, limit, member,
	).Int64Slice()
	if err != nil {
		return false, 0, 0, err
	}

	allowed := res[0] == 1
	if !allowed {
		// res[2] is the oldest entry's timestamp in ms; it expires at oldest+window
		oldestMS := res[2]
		retryAfterMS := (oldestMS + windowMS) - nowMS
		if retryAfterMS < 0 {
			retryAfterMS = 0
		}
		return false, 0, time.Duration(retryAfterMS) * time.Millisecond, nil
	}
	return true, int(res[1]), 0, nil
}

// LocalLimiter is an in-process token bucket limiter per key (no Redis dependency).
type LocalLimiter struct {
	mu       sync.Mutex
	limiters map[string]*rate.Limiter
}

// NewLocalLimiter creates an in-memory limiter.
func NewLocalLimiter() *LocalLimiter {
	return &LocalLimiter{limiters: make(map[string]*rate.Limiter)}
}

func (l *LocalLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	l.mu.Lock()
	lim, ok := l.limiters[key]
	if !ok {
		r := rate.Every(window / time.Duration(limit))
		lim = rate.NewLimiter(r, limit)
		l.limiters[key] = lim
	}
	l.mu.Unlock()

	if lim.Allow() {
		// token bucket doesn't track exact remaining count; approximate from burst
		remaining := int(lim.Tokens())
		return true, remaining, 0, nil
	}
	reservation := lim.Reserve()
	delay := reservation.Delay()
	reservation.Cancel()
	return false, 0, delay, nil
}

// FallbackLimiter tries Redis first; on error falls back to the local limiter.
type FallbackLimiter struct {
	redis *RedisLimiter
	local *LocalLimiter
}

// NewFallbackLimiter creates a limiter with automatic Redis→local fallback.
func NewFallbackLimiter(redis *RedisLimiter, local *LocalLimiter) *FallbackLimiter {
	return &FallbackLimiter{redis: redis, local: local}
}

func (f *FallbackLimiter) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, int, time.Duration, error) {
	allowed, remaining, retry, err := f.redis.Allow(ctx, key, limit, window)
	if err != nil {
		// Redis unavailable — fall back to local limiter
		return f.local.Allow(ctx, key, limit, window)
	}
	return allowed, remaining, retry, nil
}
