package main

import (
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
)

const limiterPrefix = "delivery"

// chooseLimiter builds the limiter RATE_LIMITER names, single permits unless told
// otherwise. The other two are kept to compare them under the same load, and none may go
// faster than the limit. An unknown name is an error, because falling back to another
// limiter would hide a mistake in how a worker is run. It also answers the claim gap to
// use: only single permits need it gone, since each claim takes one message.
func chooseLimiter(env func(string) string, client *redis.Client, limit int, claimGap time.Duration) (delivery.Limiter, time.Duration, error) {
	switch name := env("RATE_LIMITER"); name {
	case "", "permit":
		return delivery.NewPermitForLimit(client, limit, limiterPrefix), 0, nil
	case "window":
		return delivery.NewRedisLimiter(client, limit, limiterPrefix), claimGap, nil
	case "pacing":
		interval := delivery.PaceInterval(limit)
		if text := env("PACING_INTERVAL_MS"); text != "" {
			milliseconds, err := strconv.ParseFloat(text, 64)
			if err != nil || milliseconds <= 0 {
				return nil, 0, fmt.Errorf("PACING_INTERVAL_MS must be a positive number")
			}
			interval = time.Duration(milliseconds * float64(time.Millisecond))
		}
		if interval < time.Second/time.Duration(limit) {
			return nil, 0, fmt.Errorf("PACING_INTERVAL_MS of %v is faster than RATE_LIMIT_PER_SECOND of %d", interval, limit)
		}
		return delivery.NewRedisPacer(client, interval, limiterPrefix), claimGap, nil
	default:
		return nil, 0, fmt.Errorf("RATE_LIMITER %q is not one of permit, window, pacing", name)
	}
}
