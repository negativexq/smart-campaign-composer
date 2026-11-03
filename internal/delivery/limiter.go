package delivery

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// slidingWindow admits a send only if fewer than the limit happened in the last
// second, by Redis's clock. A fixed window or token bucket could let through up to
// twice the limit around a boundary; this holds in every one-second span.
var slidingWindow = redis.NewScript(`
local key, limit = KEYS[1], tonumber(ARGV[1])
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
		waitMillis, err := slidingWindow.Run(ctx, l.client, []string{key}, l.limit, member).Int64()
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
