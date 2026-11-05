//go:build load

package load_test

import (
	"bufio"
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/delivery"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

// TestPauseRace counts turns taken after a pause began. Redis logs every command in the
// order it ran them, so the count does not depend on any clock outside Redis. Checking
// the pause in the same script as taking a turn is what keeps this at zero: a run with
// go-redis/redis_rate, which needs a separate check, is in results/2026-11-05-1023.md.
func TestPauseRace(t *testing.T) {
	const (
		rounds = 300
		pause  = 40 * time.Millisecond
		// The time between pauses varies, so they do not fall into step with the turns.
		leastRun = 20 * time.Millisecond
		mostRun  = 90 * time.Millisecond
	)
	client := testsupport.NewRedis(t)
	for _, workers := range []int{1, 4} {
		t.Run(fmt.Sprintf("%d workers", workers), func(t *testing.T) {
			prefix := "race" + testsupport.Unique(t)
			watch := watchOrder(t, client.Options().Addr, prefix)
			limiter := delivery.NewPermitForLimit(client, limit, prefix)
			ctx, cancel := context.WithCancel(context.Background())
			var running sync.WaitGroup
			for range workers {
				running.Go(func() {
					for ctx.Err() == nil {
						permits, err := limiter.Reserve(ctx, "sms", 1)
						if err != nil {
							return
						}
						time.Sleep(max(permits.Retry, 200*time.Microsecond))
					}
				})
			}
			for range rounds {
				if err := limiter.Pause(context.Background(), "sms", pause); err != nil {
					t.Fatal(err)
				}
				time.Sleep(pause + leastRun + time.Duration(rand.Int64N(int64(mostRun-leastRun)))) //nolint:gosec // test timing
			}
			cancel()
			running.Wait()
			time.Sleep(200 * time.Millisecond)
			leaked, turns, pauses, longest := watch.result(pause)
			out := &result{Name: "Pause race, " + t.Name(), Question: "Can a turn be taken once a pause has begun?",
				Setup: fmt.Sprintf("%d workers asking for turns, %d pauses of %s, order read from Redis.", workers, rounds, pause), CampaignStatus: "COMPLETED",
				Channels: map[string]channelFigures{}, Violations: []string{},
				Notes: []string{fmt.Sprintf("Pauses seen %d, turns taken %d, turns taken during a pause %d, latest %.2f ms after it began.", pauses, turns, leaked, longest.Seconds()*1000)}}
			t.Log(out.Notes[0])
			if leaked > 0 {
				out.Violations = append(out.Violations, fmt.Sprintf("%d turns taken after a pause began", leaked))
			}
			record(t, out)
		})
	}
}

// orderLog keeps the turn and pause writes Redis ran, with Redis's own time.
type orderLog struct {
	mu     sync.Mutex
	events []orderEvent
	conn   net.Conn
}

type orderEvent struct {
	at    time.Duration
	pause bool
}

func watchOrder(t *testing.T, address, prefix string) *orderLog {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Write([]byte("MONITOR\r\n")); err != nil {
		t.Fatal(err)
	}
	log := &orderLog{conn: conn}
	go func() {
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 1<<16), 1<<20)
		for scanner.Scan() {
			match := monitorLine.FindStringSubmatch(scanner.Text())
			if match == nil || match[3] != "lua" {
				continue
			}
			args := monitorArg.FindAllStringSubmatch(match[4], -1)
			if len(args) < 2 || !strings.EqualFold(args[0][1], "set") || !strings.Contains(args[1][1], prefix) {
				continue
			}
			seconds, _ := strconv.ParseInt(match[1], 10, 64)
			micros, _ := strconv.ParseInt(match[2], 10, 64)
			event := orderEvent{at: time.Duration(seconds)*time.Second + time.Duration(micros)*time.Microsecond, pause: strings.Contains(args[1][1], ":pause:")}
			log.mu.Lock()
			log.events = append(log.events, event)
			log.mu.Unlock()
		}
	}()
	return log
}

func (l *orderLog) result(length time.Duration) (leaked, turns, pauses int, longest time.Duration) {
	l.conn.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	var began time.Duration
	for _, event := range l.events {
		if event.pause {
			pauses++
			began = event.at
			continue
		}
		turns++
		// Redis ends a key to the millisecond, so the last moments of a pause do not count.
		if pauses > 0 && event.at-began < length-2*time.Millisecond {
			leaked++
			longest = max(longest, event.at-began)
		}
	}
	return leaked, turns, pauses, longest
}
