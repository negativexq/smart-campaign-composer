package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// maxPermitAge is how long a permit stays good. A claim that takes longer than this
// has lost its turn, so its message goes back in the queue. Claims normally take a
// few milliseconds.
const maxPermitAge = 200 * time.Millisecond

// paceShare is how much of a rate limit is used. It is what leaves room for calls to
// bunch up on their way to the provider. See DECISIONS.md for what it was tested against.
const paceShare = 0.8

// permitBurstFrom is the lowest limit a burst of two fits inside: below it, two sends at
// once on top of the pace could pass the limit within a second.
const permitBurstFrom = 10

// PaceInterval is the time between sends that keeps to a limit per second.
func PaceInterval(limit int) time.Duration {
	return time.Duration(float64(time.Second) / (float64(limit) * paceShare))
}

// PermitBurst is how many sends may go at once under a limit per second.
func PermitBurst(limit int) int {
	if limit < permitBurstFrom {
		return 1
	}
	return 2
}

// takePermit is the generic cell rate algorithm with one send per call. The key holds
// the theoretical time of the next send. A turn is spent once it is given and is never
// returned, so a command run twice costs a turn and can never create one. The answer's
// wait is in microseconds.
var takePermit = redis.NewScript(`
local interval, burst = tonumber(ARGV[1]), tonumber(ARGV[2])
local paused = redis.call('PTTL', KEYS[2])
if paused > 0 then
  return {0, paused * 1000, 1}
end
local time = redis.call('TIME')
local now = time[1] * 1000000 + time[2]
local due = math.max(tonumber(redis.call('GET', KEYS[1]) or now), now)
local allowed = due - (burst - 1) * interval
if now < allowed then
  return {0, allowed - now, 0}
end
redis.call('SET', KEYS[1], string.format('%.0f', due + interval), 'PX', 5000)
return {1, 0, 0}
`)

// RedisPermit gives single, non-returnable turns shared across every worker on a channel.
// A turn with nothing to send is lost, which costs throughput and cannot break the limit.
type RedisPermit struct {
	RedisLimiter
	interval time.Duration
	burst    int
}

// NewPermitForLimit keeps every channel within a limit per second.
func NewPermitForLimit(client *redis.Client, limit int, prefix string) *RedisPermit {
	return NewRedisPermit(client, PaceInterval(limit), PermitBurst(limit), prefix)
}

// NewRedisPermit allows one send per interval on each channel, and up to burst at once
// after a rest. Its pauses are the same as a RedisLimiter's.
func NewRedisPermit(client *redis.Client, interval time.Duration, burst int, prefix string) *RedisPermit {
	return &RedisPermit{RedisLimiter{client: client, prefix: prefix}, interval, max(burst, 1)}
}

// Reserve grants at most one send, whatever is wanted. An error means no permission.
func (p *RedisPermit) Reserve(ctx context.Context, channel string, _ int) (Permits, error) {
	// Age counts from before the question, so a slow answer cannot make a permit look new.
	// time.Now carries this process's monotonic clock, so Redis's clock plays no part.
	asked := time.Now()
	answer, err := takePermit.Run(ctx, p.client, []string{p.prefix + ":permit:" + channel, p.pauseKey(channel)}, p.interval.Microseconds(), p.burst).Int64Slice()
	if err != nil {
		return Permits{}, fmt.Errorf("rate limiter: %w", err)
	}
	return Permits{Granted: int(answer[0]), Retry: time.Duration(answer[1]) * time.Microsecond, Paused: answer[2] == 1, taken: asked}, nil
}

// Settle returns nothing and touches no shared state. It only refuses a permit that has
// grown too old, which works without Redis.
func (p *RedisPermit) Settle(_ context.Context, _ string, permits Permits, used int) (int, error) {
	if time.Since(permits.taken) > maxPermitAge {
		return 0, nil
	}
	return used, nil
}
