package delivery

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	maxPause = 30 * time.Second
	// probeInterval is how often one call tests a channel whose credentials were refused.
	probeInterval = 30 * time.Second
	// recoverHold is how long a channel waits once Redis lost its window: as long as
	// a call with no answer counts, the send timeout and its margin, and the second
	// after it.
	recoverHold = defaultSendTimeout + answerMargin + grantWindow
	// grantWindow is the second the limit is about. A call is counted from when it
	// goes out until its answer comes back, and for this long after the answer. The
	// provider received it before answering, so however long it took on the way, a
	// call granted later cannot reach the provider within a second of it.
	grantWindow = time.Second
	// inFlightRetry is how soon to ask again when the window is full of calls still
	// waiting for an answer: one of them answering is what frees it.
	inFlightRetry = 20 * time.Millisecond
	// maxHold caps how long one call can count as in flight, so a stuck worker cannot
	// hold a channel for long.
	maxHold = 2 * time.Minute
)

// landed applies answers that came back since this worker last asked: each moves one
// of its reservation's calls still in flight to when its answer arrived, by Redis's
// clock less how long ago the worker saw it, which is never earlier than the truth.
// The answers ride on calls the worker makes anyway, so they cost no round trip.
const landed = `
local function landed(key, now, first)
  for j = first, #ARGV, 3 do
    local id, granted, at = ARGV[j], tonumber(ARGV[j + 1]), now - tonumber(ARGV[j + 2])
    for i = 1, granted do
      local member = id .. ':' .. i
      local score = redis.call('ZSCORE', key, member)
      if score and tonumber(score) > now then
        redis.call('ZADD', key, math.min(at, now), member)
        break
      end
    end
  end
end
`

// slidingWindow grants up to the wanted number of sends, as many as keep the count
// in the last grantWindow within the limit, by Redis's clock, and none while the
// channel is paused. A fixed window or token bucket could let through up to twice
// the limit around a boundary; this holds in every span of that length. Calls still
// in flight are stored in the future, so they count until their answer is applied.
// It answers with what was granted, how many milliseconds to wait if nothing was, and
// whether that is because of a pause. Each permit is stored under its reservation, so
// it can be settled later without touching anyone else's.
//
// While the provider refuses the channel's credentials, it grants one call every
// probe interval and nothing else. Moving the next probe's time forward here, in the
// same step that grants it, is what makes it one probe across every worker; a worker
// that dies with it only delays the next one. The fourth value is the lock's
// generation when the permit is that probe.
//
// The window lives only in Redis. When Redis loses it, by a restart without
// persistence, a flush or an eviction, the calls of the last seconds are forgotten
// and the next second could be given out twice. A key with no expiry marks the
// channel's window as whole; when it is missing, the channel grants nothing until
// every call that could have been out has stopped counting. A first start waits too,
// since it cannot tell the two apart.
var slidingWindow = redis.NewScript(landed + `
local key, limit, window, want, ttl = KEYS[1], tonumber(ARGV[1]), tonumber(ARGV[3]), tonumber(ARGV[4]), tonumber(ARGV[5])
local time = redis.call('TIME')
local now = time[1] * 1000000 + time[2]
landed(key, now, 9)
local intact = redis.call('GET', KEYS[4])
if not intact then
  intact = now + tonumber(ARGV[8])
  redis.call('SET', KEYS[4], intact)
end
if now < tonumber(intact) then
  return {0, math.max(1, math.ceil((tonumber(intact) - now) / 1000)), 0, 0}
end
local paused = redis.call('PTTL', KEYS[2])
if paused > 0 then
  return {0, paused, 1, 0}
end
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
local room = limit - redis.call('ZCARD', key)
if room <= 0 then
  local oldest = redis.call('ZRANGEBYSCORE', key, '-inf', now, 'WITHSCORES', 'LIMIT', 0, 1)
  if #oldest == 0 then
    return {0, tonumber(ARGV[6]), 0, 0}
  end
  return {0, math.max(1, math.ceil((tonumber(oldest[2]) + window - now) / 1000)), 0, 0}
end
local probe = 0
if redis.call('HGET', KEYS[3], 'locked') == '1' then
  local at = tonumber(redis.call('HGET', KEYS[3], 'probe_at') or '0')
  if now < at then
    return {0, math.max(1, math.ceil((at - now) / 1000)), 1, 0}
  end
  redis.call('HSET', KEYS[3], 'probe_at', now + tonumber(ARGV[7]))
  probe = tonumber(redis.call('HGET', KEYS[3], 'gen'))
  want = 1
end
local granted = math.min(room, want)
for i = 1, granted do
  redis.call('ZADD', key, now, ARGV[2] .. ':' .. i)
end
redis.call('PEXPIRE', key, ttl)
return {granted, 0, 0, probe}
`)

// settlePermits gives back the unused permits and marks the used ones in flight until
// hold from now, the longest a call can take before its answer is applied.
var settlePermits = redis.NewScript(landed + `
local key, limit, id, window, granted, used, hold, ttl = KEYS[1], tonumber(ARGV[1]), ARGV[2], tonumber(ARGV[3]), tonumber(ARGV[4]), tonumber(ARGV[5]), tonumber(ARGV[6]), tonumber(ARGV[7])
local time = redis.call('TIME')
local now = time[1] * 1000000 + time[2]
landed(key, now, 8)
redis.call('ZREMRANGEBYSCORE', key, 0, now - window)
for i = used + 1, granted do
  redis.call('ZREM', key, id .. ':' .. i)
end
local room = limit - redis.call('ZCARD', key)
local confirmed = 0
for i = 1, used do
  local member = id .. ':' .. i
  if redis.call('ZSCORE', key, member) then
    redis.call('ZADD', key, now + hold, member)
    confirmed = confirmed + 1
  elseif room > 0 then
    redis.call('ZADD', key, now + hold, member)
    room = room - 1
    confirmed = confirmed + 1
  end
end
redis.call('PEXPIRE', key, ttl)
return confirmed
`)

// lockCredentials holds a channel whose credentials the provider refused, starting a
// new generation only when it was open, so refusals that keep arriving neither move
// the next probe back nor make the probe in flight unable to open the lock it tests.
var lockCredentials = redis.NewScript(`
local time = redis.call('TIME')
local now = time[1] * 1000000 + time[2]
if redis.call('HGET', KEYS[1], 'locked') == '1' then
  return {0, tonumber(redis.call('HGET', KEYS[1], 'gen'))}
end
local gen = redis.call('HINCRBY', KEYS[1], 'gen', 1)
redis.call('HSET', KEYS[1], 'locked', '1', 'probe_at', now + tonumber(ARGV[1]))
return {1, gen}
`)

// unlockCredentials opens the channel only for a probe of the lock still in place: a
// late answer from an earlier lock's probe cannot open a newer one.
var unlockCredentials = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'locked') == '1' and redis.call('HGET', KEYS[1], 'gen') == ARGV[1] then
  redis.call('HSET', KEYS[1], 'locked', '0')
  return 1
end
return 0
`)

// releaseProbe lets the next request probe at once, for a probe that found nothing to
// send, so the lock does not wait a whole interval for nothing.
var releaseProbe = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'locked') == '1' and redis.call('HGET', KEYS[1], 'gen') == ARGV[1] then
  local time = redis.call('TIME')
  redis.call('HSET', KEYS[1], 'probe_at', time[1] * 1000000 + time[2])
end
return 0
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
	// probeInterval is how often a channel with refused credentials is tested.
	probeInterval time.Duration
	recoverHold   time.Duration

	mu sync.Mutex
	// answered holds calls answered since the channel's last trip to Redis.
	answered map[string][]answer
}

// answer is one call that came back, from the reservation that sent it.
type answer struct {
	id      string
	granted int
	at      time.Time
}

// NewRedisLimiter allows limit sends per second on each channel.
func NewRedisLimiter(client *redis.Client, limit int, prefix string) *RedisLimiter {
	return &RedisLimiter{client: client, limit: limit, prefix: prefix, probeInterval: probeInterval, recoverHold: recoverHold, answered: map[string][]answer{}}
}

// RecoverAfter changes how long a channel waits after Redis lost its window.
func (l *RedisLimiter) RecoverAfter(hold time.Duration) *RedisLimiter {
	l.recoverHold = hold
	return l
}

// ProbeEvery changes how often a channel with refused credentials is tested.
func (l *RedisLimiter) ProbeEvery(interval time.Duration) *RedisLimiter {
	l.probeInterval = interval
	return l
}

// Reserve grants up to want sends without waiting. An error means there is no
// permission to send, including when Redis cannot be reached.
func (l *RedisLimiter) Reserve(ctx context.Context, channel string, want int) (Permits, error) {
	id := rand.Text()
	answers, args := l.takeAnswered(channel)
	reply, err := slidingWindow.Run(ctx, l.client, []string{l.rateKey(channel), l.pauseKey(channel), l.credentialsKey(channel), l.intactKey(channel)},
		append([]any{l.limit, id, grantWindow.Microseconds(), want, keyLife(maxHold).Milliseconds(), inFlightRetry.Milliseconds(), l.probeInterval.Microseconds(), l.recoverHold.Microseconds()}, args...)...).Int64Slice()
	if err != nil {
		l.giveBackAnswered(channel, answers)
		return Permits{}, fmt.Errorf("rate limiter: %w", err)
	}
	return Permits{Granted: int(reply[0]), Retry: time.Duration(reply[1]) * time.Millisecond, Paused: reply[2] == 1, Probe: reply[3], id: id}, nil
}

// Settle gives back this reservation's unused permits and counts the used ones as in
// flight for up to hold, until Answered says each came back. Only its own permits are
// touched, so a worker that stalled past the window cannot erase what another has
// sent since. The answer is how many of the used ones may go out, fewer only if that
// stall let the window fill up.
func (l *RedisLimiter) Settle(ctx context.Context, channel string, permits Permits, used int, hold time.Duration) (int, error) {
	hold = min(hold, maxHold)
	answers, args := l.takeAnswered(channel)
	confirmed, err := settlePermits.Run(ctx, l.client, []string{l.rateKey(channel)},
		append([]any{l.limit, permits.id, grantWindow.Microseconds(), permits.Granted, used, hold.Microseconds(), keyLife(hold).Milliseconds()}, args...)...).Int()
	if err != nil {
		l.giveBackAnswered(channel, answers)
		return 0, fmt.Errorf("settle permits: %w", err)
	}
	return confirmed, nil
}

// Answered notes that one call of permits came back at. It is sent with this
// channel's next Reserve or Settle, so it costs no trip to Redis of its own.
func (l *RedisLimiter) Answered(channel string, permits Permits, at time.Time) {
	if permits.id == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.answered[channel] = append(l.answered[channel], answer{permits.id, permits.Granted, at})
}

func (l *RedisLimiter) takeAnswered(channel string) ([]answer, []any) {
	l.mu.Lock()
	answers := l.answered[channel]
	delete(l.answered, channel)
	l.mu.Unlock()
	args := make([]any, 0, 3*len(answers))
	for _, a := range answers {
		args = append(args, a.id, a.granted, time.Since(a.at).Microseconds())
	}
	return answers, args
}

// giveBackAnswered keeps answers a failed trip did not deliver for the next one. Until
// then their calls still count as in flight, which only holds the rate back.
func (l *RedisLimiter) giveBackAnswered(channel string, answers []answer) {
	if len(answers) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.answered[channel] = append(answers, l.answered[channel]...)
}

// keyLife keeps a channel's key while any call in it can still count.
func keyLife(hold time.Duration) time.Duration {
	return hold + grantWindow + time.Second
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

// LockCredentials holds the channel for every worker after the provider refused its
// credentials, and says whether this call is what locked it.
func (l *RedisLimiter) LockCredentials(ctx context.Context, channel string) (bool, error) {
	reply, err := lockCredentials.Run(ctx, l.client, []string{l.credentialsKey(channel)}, l.probeInterval.Microseconds()).Int64Slice()
	if err != nil {
		return false, fmt.Errorf("lock channel: %w", err)
	}
	return reply[0] == 1, nil
}

// UnlockCredentials opens the channel after its probe got through, if the lock that
// probe tested is still the one in place. It says whether it opened it.
func (l *RedisLimiter) UnlockCredentials(ctx context.Context, channel string, generation int64) (bool, error) {
	opened, err := unlockCredentials.Run(ctx, l.client, []string{l.credentialsKey(channel)}, generation).Int()
	if err != nil {
		return false, fmt.Errorf("unlock channel: %w", err)
	}
	return opened == 1, nil
}

// ReleaseProbe hands back a probe that sent nothing.
func (l *RedisLimiter) ReleaseProbe(ctx context.Context, channel string, generation int64) error {
	if err := releaseProbe.Run(ctx, l.client, []string{l.credentialsKey(channel)}, generation).Err(); err != nil && !errors.Is(err, redis.Nil) {
		return fmt.Errorf("release probe: %w", err)
	}
	return nil
}

// CredentialsLocked says whether the channel is held because its credentials were refused.
func (l *RedisLimiter) CredentialsLocked(ctx context.Context, channel string) (bool, error) {
	locked, err := l.client.HGet(ctx, l.credentialsKey(channel), "locked").Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read channel lock: %w", err)
	}
	return locked == "1", nil
}

func (l *RedisLimiter) rateKey(channel string) string {
	return l.prefix + ":rate:" + channel
}

func (l *RedisLimiter) pauseKey(channel string) string {
	return l.prefix + ":pause:" + channel
}

// credentialsKey holds the channel's lock after refused credentials. It has no expiry:
// only a probe that gets through opens it. If Redis loses it, the next refusal sets
// it again, and those calls cost their messages no attempt.
func (l *RedisLimiter) credentialsKey(channel string) string {
	return l.prefix + ":credentials:" + channel
}

func (l *RedisLimiter) intactKey(channel string) string {
	return l.prefix + ":intact:" + channel
}
