package delivery

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	maxPause = 30 * time.Second
	// grantWindow is longer than the second the limit is about. A permission reaches
	// the provider some milliseconds later, by a different amount each time, so calls
	// granted exactly a second apart can land within one. Measured at the provider, a
	// plain second let through 113 calls for a limit of 100. The margin costs about
	// 9% of the rate: 91 a second at a limit of 100.
	grantWindow = 1100 * time.Millisecond
)

// slidingWindow grants up to the wanted number of sends, as many as keep the count
// in the last grantWindow within the limit, by Redis's clock, and none while the
// channel is paused. A fixed window or token bucket could let through up to twice
// the limit around a boundary; this holds in every span of that length. It answers
// with what was granted, how many milliseconds to wait if nothing was, and whether
// that is because of a pause.
var slidingWindow = redis.NewScript(`
local key, limit, window, want = KEYS[1], tonumber(ARGV[1]), tonumber(ARGV[3]), tonumber(ARGV[4])
local paused = redis.call('PTTL', KEYS[2])
if paused > 0 then
  return {0, paused, 1}
end
local time = redis.call('TIME')
local now = time[1] * 1000000 + time[2]
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
local room = limit - redis.call('ZCARD', key)
if room <= 0 then
  local oldest = redis.call('ZRANGE', key, 0, 0, 'WITHSCORES')
  return {0, math.max(1, math.ceil((tonumber(oldest[2]) + window - now) / 1000)), 0}
end
local granted = math.min(room, want)
for i = 1, granted do
  redis.call('ZADD', key, now, ARGV[2] .. ':' .. i)
end
redis.call('PEXPIRE', key, 2000)
return {granted, 0, 0}
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

// Reserve grants up to want sends without waiting. An error means there is no
// permission to send, including when Redis cannot be reached.
func (l *RedisLimiter) Reserve(ctx context.Context, channel string, want int) (Permits, error) {
	answer, err := slidingWindow.Run(ctx, l.client, []string{l.rateKey(channel), l.pauseKey(channel)}, l.limit, rand.Text(), grantWindow.Microseconds(), want).Int64Slice()
	if err != nil {
		return Permits{}, fmt.Errorf("rate limiter: %w", err)
	}
	return Permits{Granted: int(answer[0]), Retry: time.Duration(answer[1]) * time.Millisecond, Paused: answer[2] == 1}, nil
}

// Release gives back the newest permits. They need not be this caller's own: the
// count is what the limit is about, and they were all granted moments ago.
func (l *RedisLimiter) Release(ctx context.Context, channel string, count int) error {
	if err := l.client.ZPopMax(ctx, l.rateKey(channel), int64(count)).Err(); err != nil {
		return fmt.Errorf("release permits: %w", err)
	}
	return nil
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

func (l *RedisLimiter) rateKey(channel string) string {
	return l.prefix + ":rate:" + channel
}

func (l *RedisLimiter) pauseKey(channel string) string {
	return l.prefix + ":pause:" + channel
}
