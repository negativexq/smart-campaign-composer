package delivery

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const maxPause = 30 * time.Second

// slidingWindow admits a send only if fewer than the limit happened in the last
// second, by Redis's clock, and none while the channel is paused. A fixed window or token bucket could let through up to
// twice the limit around a boundary; this holds in every one-second span.
var slidingWindow = redis.NewScript(`
local key, limit = KEYS[1], tonumber(ARGV[1])
local paused = redis.call('PTTL', KEYS[2])
if paused > 0 then
  return paused
end
local time = redis.call('TIME')
local now = time[1] * 1000000 + time[2]
redis.call('ZREMRANGEBYSCORE', key, 0, now - 1000000)
if redis.call('ZCARD', key) < limit then
  redis.call('ZADD', key, now, ARGV[2])
  redis.call('PEXPIRE', key, 2000)
  return 0
end
local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
return math.max(1, math.ceil((tonumber(oldest[2]) + 1000000 - now) / 1000))
`)

var extendPause = redis.NewScript(`
if redis.call('PTTL', KEYS[1]) < tonumber(ARGV[1]) then
  redis.call('SET', KEYS[1], '1', 'PX', ARGV[1])
end
`)

// RedisLimiter shares one per-channel rate across every worker.
type RedisLimiter struct {
	client *redis.Client
	limit  int
	prefix string
}

// NewRedisLimiter allows limit sends per second on each channel.
func NewRedisLimiter(client *redis.Client, limit int, prefix string) *RedisLimiter {
	return &RedisLimiter{client, limit, prefix}
}

// Wait blocks until a send is allowed and says how long that took. An error means
// there is no permission to send, including when Redis cannot be reached.
func (l *RedisLimiter) Wait(ctx context.Context, channel string) (time.Duration, error) {
	start := time.Now()
	key := l.prefix + ":rate:" + channel
	for {
		member := rand.Text()
		waitMillis, err := slidingWindow.Run(ctx, l.client, []string{key, l.pauseKey(channel)}, l.limit, member).Int64()
		if err != nil {
			if ctx.Err() != nil {
				return time.Since(start), ctx.Err()
			}
			return time.Since(start), fmt.Errorf("rate limiter: %w", err)
		}
		if waitMillis == 0 {
			return time.Since(start), nil
		}
		select {
		case <-ctx.Done():
			return time.Since(start), ctx.Err()
		case <-time.After(time.Duration(waitMillis) * time.Millisecond):
		}
	}
}

// Pause holds a channel for every worker, as after a 429. It never shortens a pause
// already running, and a ceiling keeps one answer from stopping all campaigns.
func (l *RedisLimiter) Pause(ctx context.Context, channel string, length time.Duration) error {
	length = min(length, maxPause)
	if length <= 0 {
		return nil
	}
	if err := extendPause.Run(ctx, l.client, []string{l.pauseKey(channel)}, length.Milliseconds()).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("pause channel: %w", err)
	}
	return nil
}

func (l *RedisLimiter) pauseKey(channel string) string {
	return l.prefix + ":pause:" + channel
}
