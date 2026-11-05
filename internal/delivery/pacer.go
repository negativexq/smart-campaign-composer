package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// pacerTolerance is how far ahead of its turn a send may be granted. It has to outlast
// a worker's gap between claims, or a channel could never reach its pace.
const pacerTolerance = 40 * time.Millisecond

// paceReserve is the generic cell rate algorithm. The key holds the time the next send
// is due. Sends are granted only as far ahead of that as the tolerance allows, so they
// leave evenly and a rest never builds up a burst.
var paceReserve = redis.NewScript(`
local interval, tolerance, want = tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3])
local paused = redis.call('PTTL', KEYS[2])
if paused > 0 then
  return {0, paused, 1, 0}
end
local time = redis.call('TIME')
local now = time[1] * 1000000 + time[2]
local due = math.max(tonumber(redis.call('GET', KEYS[1]) or now), now)
local room = math.floor((now + tolerance - due) / interval)
if room <= 0 then
  return {0, math.max(1, math.ceil((due - tolerance + interval - now) / 1000)), 0, now}
end
local granted = math.min(room, want)
redis.call('SET', KEYS[1], string.format('%.0f', due + granted * interval), 'PX', 5000)
return {granted, 0, 0, now}
`)

// paceSettle gives back unused turns. A reservation that waited longer than the
// tolerance has missed its turns, so what it sends takes new ones, as far as they fit.
var paceSettle = redis.NewScript(`
local interval, tolerance, granted, used, issued = tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3]), tonumber(ARGV[4]), tonumber(ARGV[5])
local time = redis.call('TIME')
local now = time[1] * 1000000 + time[2]
local due = tonumber(redis.call('GET', KEYS[1]) or now)
local confirmed = used
if now - issued <= tolerance then
  due = math.max(due - (granted - used) * interval, now)
else
  due = math.max(due, now)
  confirmed = math.min(used, math.max(math.floor((now + tolerance - due) / interval), 0))
  due = due + confirmed * interval
end
redis.call('SET', KEYS[1], string.format('%.0f', due), 'PX', 5000)
return confirmed
`)

// RedisPacer shares one evenly spaced pace across every worker on each channel.
type RedisPacer struct {
	RedisLimiter
	interval time.Duration
}

// NewRedisPacer allows one send per interval on each channel. Its pauses are the same
// as a RedisLimiter's.
func NewRedisPacer(client *redis.Client, interval time.Duration, prefix string) *RedisPacer {
	return &RedisPacer{RedisLimiter{client: client, prefix: prefix}, interval}
}

// Reserve grants the sends whose turn has come. An error means no permission to send.
func (p *RedisPacer) Reserve(ctx context.Context, channel string, want int) (Permits, error) {
	answer, err := paceReserve.Run(ctx, p.client, []string{p.paceKey(channel), p.pauseKey(channel)}, p.interval.Microseconds(), p.tolerance().Microseconds(), want).Int64Slice()
	if err != nil {
		return Permits{}, fmt.Errorf("rate limiter: %w", err)
	}
	return Permits{Granted: int(answer[0]), Retry: time.Duration(answer[1]) * time.Millisecond, Paused: answer[2] == 1, issued: answer[3]}, nil
}

// Settle gives back unused turns and answers how many of those used may go out.
func (p *RedisPacer) Settle(ctx context.Context, channel string, permits Permits, used int) (int, error) {
	confirmed, err := paceSettle.Run(ctx, p.client, []string{p.paceKey(channel)}, p.interval.Microseconds(), p.tolerance().Microseconds(), permits.Granted, used, permits.issued).Int()
	if err != nil {
		return 0, fmt.Errorf("settle permits: %w", err)
	}
	return confirmed, nil
}

// tolerance is never under one interval, or a slow pace could never grant a send.
func (p *RedisPacer) tolerance() time.Duration {
	return max(pacerTolerance, p.interval)
}

func (p *RedisPacer) paceKey(channel string) string {
	return p.prefix + ":pace:" + channel
}
