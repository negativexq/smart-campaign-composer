//go:build load

package load_test

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
)

// cost is what a run asked of Postgres and Redis while the workers ran. The rig's own
// polling is included, which is a few statements a second.
type cost struct {
	Statements            int64   `json:"postgres_statements"`
	StatementMilliseconds float64 `json:"postgres_milliseconds"`
	ClaimCalls            int64   `json:"claim_calls"`
	ClaimMilliseconds     float64 `json:"claim_milliseconds"`
	ClaimRows             int64   `json:"claimed_rows"`
	RedisCommands         int64   `json:"redis_commands"`
	RedisScripts          int64   `json:"redis_script_calls"`
	Messages              int64   `json:"messages"`
	StatementsPerMessage  float64 `json:"statements_per_message"`
	RedisPerMessage       float64 `json:"redis_commands_per_message"`
}

func (r *run) resetCounters() {
	ctx := context.Background()
	if _, err := r.pool.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_stat_statements`); err != nil {
		r.t.Fatalf("statement counting (run `make test-env-up` to recreate postgres): %v", err)
	}
	if _, err := r.pool.Exec(ctx, `SELECT pg_stat_statements_reset()`); err != nil {
		r.t.Fatalf("reset statement counts: %v", err)
	}
	if err := r.redis.ConfigResetStat(ctx).Err(); err != nil {
		r.t.Fatalf("reset redis counts: %v", err)
	}
}

func (r *run) readCost() cost {
	ctx := context.Background()
	var out cost
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(sum(calls), 0)::bigint, COALESCE(sum(total_exec_time), 0)::float8,
		       COALESCE(sum(calls) FILTER (WHERE query ILIKE '%FOR UPDATE OF d SKIP LOCKED%'), 0)::bigint,
		       COALESCE(sum(total_exec_time) FILTER (WHERE query ILIKE '%FOR UPDATE OF d SKIP LOCKED%'), 0)::float8,
		       COALESCE(sum(rows) FILTER (WHERE query ILIKE '%FOR UPDATE OF d SKIP LOCKED%'), 0)::bigint
		FROM pg_stat_statements WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())`).
		Scan(&out.Statements, &out.StatementMilliseconds, &out.ClaimCalls, &out.ClaimMilliseconds, &out.ClaimRows); err != nil {
		r.t.Logf("read statement counts: %v", err)
	}
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM campaign_deliveries WHERE campaign_id = $1`, r.campaign).Scan(&out.Messages); err != nil {
		r.t.Logf("count messages: %v", err)
	}
	if info, err := r.redis.Info(ctx, "stats", "commandstats").Result(); err == nil {
		out.RedisCommands, out.RedisScripts = redisCounts(info)
	}
	if out.Messages > 0 {
		out.StatementsPerMessage = float64(out.Statements) / float64(out.Messages)
		out.RedisPerMessage = float64(out.RedisCommands) / float64(out.Messages)
	}
	return out
}

var scriptCalls = regexp.MustCompile(`cmdstat_(?:evalsha|eval):calls=(\d+)`)

func redisCounts(info string) (commands, scripts int64) {
	for line := range strings.Lines(info) {
		if value, found := strings.CutPrefix(strings.TrimSpace(line), "total_commands_processed:"); found {
			commands, _ = strconv.ParseInt(value, 10, 64)
		}
		if match := scriptCalls.FindStringSubmatch(line); match != nil {
			calls, _ := strconv.ParseInt(match[1], 10, 64)
			scripts += calls
		}
	}
	return commands, scripts
}

// permitLog reads the pacer's turns from Redis as they are given out, without changing
// the worker. MONITOR slows Redis a little, so only the timeline scenarios use it.
type permitLog struct {
	mu     sync.Mutex
	slots  []int64
	grants map[int]int
	conn   net.Conn
}

var (
	monitorLine = regexp.MustCompile(`^\+?(\d+)\.(\d+) \[\d+ (\S+)\] (.*)$`)
	monitorArg  = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
)

func (r *run) watchPermits() {
	r.t.Helper()
	conn, err := net.Dial("tcp", r.redis.Options().Addr)
	if err != nil {
		r.t.Fatalf("watch permits: %v", err)
	}
	r.t.Cleanup(func() { conn.Close() })
	if _, err := conn.Write([]byte("MONITOR\r\n")); err != nil {
		r.t.Fatalf("watch permits: %v", err)
	}
	r.permits = &permitLog{grants: map[int]int{}, conn: conn}
	go r.permits.read(conn)
}

func (l *permitLog) read(conn net.Conn) {
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 1<<16), 1<<20)
	var due, interval int64
	reserving := false
	for scanner.Scan() {
		match := monitorLine.FindStringSubmatch(scanner.Text())
		if match == nil {
			continue
		}
		args := monitorArg.FindAllStringSubmatch(match[4], -1)
		if len(args) < 3 {
			continue
		}
		switch strings.ToLower(args[0][1]) {
		case "evalsha":
			reserving = args[2][1] == "2" && len(args) > 5 && strings.Contains(args[3][1], ":pace:email")
			if reserving {
				interval, _ = strconv.ParseInt(args[5][1], 10, 64)
			}
		case "set":
			if match[3] != "lua" || !strings.Contains(args[1][1], ":pace:email") {
				continue
			}
			value, err := strconv.ParseInt(args[2][1], 10, 64)
			if err != nil {
				continue
			}
			if reserving && interval > 0 {
				seconds, _ := strconv.ParseInt(match[1], 10, 64)
				micros, _ := strconv.ParseInt(match[2], 10, 64)
				first := max(due, seconds*1_000_000+micros)
				granted := int((value - first + interval/2) / interval)
				l.mu.Lock()
				l.grants[granted]++
				for i := range granted {
					l.slots = append(l.slots, first+int64(i)*interval)
				}
				l.mu.Unlock()
			}
			due = value
			reserving = false
		}
	}
}

// report lines each send up with the turn it most likely used, in order. Redis runs on
// another clock, so only the spread around the typical difference means anything.
func (l *permitLog) report(p *provider, channel string) []string {
	l.conn.Close()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.slots) == 0 {
		return nil
	}
	p.mu.Lock()
	sent := make([]int64, 0, len(p.timed[channel]))
	for _, call := range p.timed[channel] {
		sent = append(sent, call.sent.UnixMicro())
	}
	p.mu.Unlock()
	slices.Sort(sent)
	slots := slices.Clone(l.slots)
	slices.Sort(slots)
	count := min(len(sent), len(slots))
	differences := make([]int64, count)
	for i := range count {
		differences[i] = sent[i] - slots[i]
	}
	slices.Sort(differences)
	at := func(share float64) float64 {
		return float64(differences[int(share*float64(count-1))]-differences[count/2]) / 1000
	}
	sizes := make([]string, 0, len(l.grants))
	for size, times := range l.grants {
		sizes = append(sizes, fmt.Sprintf("%d turns: %d times", size, times))
	}
	slices.Sort(sizes)
	return []string{
		fmt.Sprintf("Turns given per reservation: %s.", strings.Join(sizes, ", ")),
		fmt.Sprintf("Send time against its turn, around the median, in ms: p1 %.1f, p50 0, p99 %.1f, max %.1f (%d sends, %d turns).", at(0.01), at(0.99), at(1), len(sent), len(l.slots)),
	}
}

// compose acts on the test environment's containers.
func compose(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("docker", append([]string{"compose", "-f", "../../compose.test.yaml"}, args...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("docker compose %v: %v: %s", args, err, output)
	}
}

func (r *run) databaseName() string {
	parsed, err := url.Parse(r.databaseURL)
	if err != nil {
		r.t.Fatal(err)
	}
	return strings.TrimPrefix(parsed.Path, "/")
}

// adminExec runs from another database, since this one is about to refuse connections.
func (r *run) adminExec(statement string) {
	r.t.Helper()
	parsed, err := url.Parse(r.databaseURL)
	if err != nil {
		r.t.Fatal(err)
	}
	parsed.Path = "/postgres"
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, parsed.String())
	if err != nil {
		r.t.Fatalf("connect as admin: %v", err)
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, statement); err != nil {
		r.t.Fatalf("%s: %v", statement, err)
	}
}
