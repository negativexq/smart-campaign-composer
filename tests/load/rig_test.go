//go:build load

package load_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel/trace/noop"

	"github.com/negativexq/smart-campaign-composer/internal/ai"
	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/db"
	"github.com/negativexq/smart-campaign-composer/internal/seed"
	"github.com/negativexq/smart-campaign-composer/internal/telemetry"
	"github.com/negativexq/smart-campaign-composer/internal/testsupport"
)

const (
	limit       = 100
	brief       = "Send a 30% discount reminder to users in Istanbul who added a product to their cart in the last 30 days and did not buy."
	firstUserID = 1_000_000
	maxRun      = 15 * time.Minute
)

// scale shrinks every scenario for a quick look at the setup: LOAD_SCALE=0.1.
func scaled(count int) int {
	var factor float64
	if _, err := fmt.Sscanf(os.Getenv("LOAD_SCALE"), "%f", &factor); err != nil || factor <= 0 {
		return count
	}
	return max(int(float64(count)*factor), 20)
}

// run is one scenario in progress: its own database, its own provider and real
// worker processes, so a kill is a real kill and calls cross a real connection.
type run struct {
	t           *testing.T
	pool        *pgxpool.Pool
	campaign    string
	databaseURL string
	provider    *provider
	providerURL string
	workers     []*exec.Cmd
	started     time.Time
	// limit is the rate the workers are given. Only a capacity scenario changes it.
	limit int
	redis *redis.Client
	// mostSending is the most messages seen holding a lease at one time.
	mostSending int
	permits     *permitLog
}

func newRun(t *testing.T, perChannel map[string]int) *run {
	t.Helper()
	ctx := context.Background()
	databaseURL := testsupport.NewDatabaseURL(t)
	pool, err := db.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	clearRateKeys(t)

	r := &run{t: t, pool: pool, databaseURL: databaseURL, provider: newProvider(), limit: limit, redis: testsupport.NewRedis(t)}
	server := httptest.NewServer(r.provider)
	t.Cleanup(server.Close)
	r.providerURL = server.URL
	r.campaign = approvedCampaign(t, pool)
	fillQueue(t, pool, r.campaign, perChannel)
	return r
}

// clearRateKeys starts a scenario with no rate used and no pause left by the last.
func clearRateKeys(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	client := testsupport.NewRedis(t)
	keys, err := client.Keys(ctx, "delivery:*").Result()
	if err != nil {
		t.Fatalf("read redis keys: %v", err)
	}
	if len(keys) > 0 {
		if err := client.Del(ctx, keys...).Err(); err != nil {
			t.Fatalf("clear redis keys: %v", err)
		}
	}
}

// approvedCampaign goes through the real steps, so the workers read real content.
func approvedCampaign(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	if _, err := seed.Apply(ctx, pool, seed.Generate(time.Now(), 13)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mock := ai.NewMock()
	mock.StreamDelay = 0
	metrics := telemetry.NewMetrics(prometheus.NewRegistry())
	service := campaign.NewService(pool, mock, metrics, noop.NewTracerProvider().Tracer("load"), time.Now)
	id, err := service.CreateWith(ctx, campaign.NewCampaign{Brief: brief})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.ProposeSegment(ctx, id, campaign.ProposeInput{}); err != nil {
		t.Fatalf("propose: %v", err)
	}
	generation, err := service.StartGeneration(ctx, id, campaign.GenerateInput{})
	if err != nil {
		t.Fatalf("start generation: %v", err)
	}
	if _, err := generation.Run(ctx, func(campaign.GenerationEvent) {}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	if _, err := service.Approve(ctx, id); err != nil {
		t.Fatalf("approve: %v", err)
	}
	if _, err := service.Dispatch(ctx, id); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	return id
}

// fillQueue replaces the campaign's few messages with the number the scenario wants,
// since no audience in the seed reaches 10,000 on a channel.
func fillQueue(t *testing.T, pool *pgxpool.Pool, id string, perChannel map[string]int) {
	t.Helper()
	ctx := context.Background()
	most := 0
	for _, count := range perChannel {
		most = max(most, count)
	}
	statements := []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM campaign_deliveries WHERE campaign_id = $1`, []any{id}},
		{`INSERT INTO users (id, first_name, city, email, phone, language)
		  SELECT i, 'Load', 'Istanbul', 'load' || i || '@example.test', '+90' || i, 'en' FROM generate_series($1::bigint, $2::bigint) i`,
			[]any{firstUserID, firstUserID + most - 1}},
		{`INSERT INTO consents (user_id, channel, allowed)
		  SELECT i, c, true FROM generate_series($1::bigint, $2::bigint) i, unnest(ARRAY['sms', 'email']) c`,
			[]any{firstUserID, firstUserID + most - 1}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.sql, statement.args...); err != nil {
			t.Fatalf("prepare queue: %v", err)
		}
	}
	for channel, count := range perChannel {
		if _, err := pool.Exec(ctx, `
			INSERT INTO campaign_deliveries (campaign_id, campaign_version_id, user_id, channel, idempotency_key)
			SELECT $1, (SELECT id FROM campaign_versions WHERE campaign_id = $1 ORDER BY version DESC LIMIT 1),
			       i, $2, $1 || ':' || i || ':' || $2
			FROM generate_series($3::bigint, $4::bigint) i`,
			id, channel, firstUserID, firstUserID+count-1); err != nil {
			t.Fatalf("fill queue: %v", err)
		}
	}
}

var buildWorker = sync.OnceValues(func() (string, error) {
	dir, err := os.MkdirTemp("", "scc-load")
	if err != nil {
		return "", err
	}
	binary := filepath.Join(dir, "worker")
	build := exec.Command("go", "build", "-o", binary, "github.com/negativexq/smart-campaign-composer/cmd/worker")
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build worker: %w: %s", err, output)
	}
	return binary, nil
})

func (r *run) startWorkers(count int) {
	r.t.Helper()
	binary, err := buildWorker()
	if err != nil {
		r.t.Fatal(err)
	}
	redisURL := os.Getenv("TEST_REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:56379/0"
	}
	if r.started.IsZero() {
		r.started = time.Now()
		r.resetCounters()
	}
	for range count {
		port := freePort(r.t)
		worker := exec.Command(binary)
		worker.Env = append(os.Environ(),
			"DATABASE_URL="+r.databaseURL, "REDIS_URL="+redisURL, "PROVIDER_URL="+r.providerURL,
			"UNSUBSCRIBE_SECRET=load-test", fmt.Sprintf("RATE_LIMIT_PER_SECOND=%d", r.limit),
			fmt.Sprintf("OPS_PORT=%d", port), "PUBLIC_URL=http://localhost:8080")
		logFile, err := os.Create(filepath.Join(r.t.TempDir(), "worker.log"))
		if err != nil {
			r.t.Fatal(err)
		}
		worker.Stdout, worker.Stderr = logFile, logFile
		if err := worker.Start(); err != nil {
			r.t.Fatalf("start worker: %v", err)
		}
		r.workers = append(r.workers, worker)
		r.t.Cleanup(func() { stopWorker(worker); logFile.Close() })
	}
}

func stopWorker(worker *exec.Cmd) {
	if worker.ProcessState != nil {
		return
	}
	_ = worker.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = worker.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		_ = worker.Process.Kill()
		<-done
	}
}

// kill ends a worker at once, leaving what it holds behind.
func (r *run) kill(index int) {
	r.t.Helper()
	worker := r.workers[index]
	if err := worker.Process.Kill(); err != nil {
		r.t.Fatalf("kill worker: %v", err)
	}
	_ = worker.Wait()
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

// waitUntilDone returns the campaign's final status, or what is open if it never ends.
func (r *run) waitUntilDone() string {
	r.t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(maxRun)
	for time.Now().Before(deadline) {
		var status string
		if err := r.pool.QueryRow(ctx, `SELECT status FROM campaigns WHERE id = $1`, r.campaign).Scan(&status); err != nil {
			// A scenario may have taken the database away on purpose.
			time.Sleep(500 * time.Millisecond)
			continue
		}
		if status != "DISPATCHING" {
			return status
		}
		var sending int
		if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM campaign_deliveries WHERE campaign_id = $1 AND status = 'SENDING'`, r.campaign).Scan(&sending); err == nil {
			r.mostSending = max(r.mostSending, sending)
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "STILL_SENDING"
}

// result is one scenario's record in the report.
type result struct {
	Name           string                    `json:"name"`
	Question       string                    `json:"question"`
	Workers        int                       `json:"workers"`
	Setup          string                    `json:"setup"`
	CampaignStatus string                    `json:"campaign_status"`
	Seconds        float64                   `json:"seconds_from_start_to_finished"`
	Duplicates     int                       `json:"duplicates_accepted"`
	KeyRepeats     int                       `json:"repeats_recognised_by_key"`
	Abandoned      int                       `json:"calls_abandoned_by_the_caller"`
	MostSending    int                       `json:"most_messages_holding_a_lease"`
	Cost           cost                      `json:"cost"`
	Channels       map[string]channelFigures `json:"channels"`
	Notes          []string                  `json:"notes"`
	Violations     []string                  `json:"violations"`
}

// finish reads the outcome and checks what must hold in every scenario.
func (r *run) finish(name, question, setup string, status string) *result {
	r.t.Helper()
	ctx := context.Background()
	out := &result{
		Name: name, Question: question, Setup: setup, Workers: len(r.workers), CampaignStatus: status,
		Seconds: time.Since(r.started).Seconds(), Duplicates: r.provider.duplicates(),
		Channels: map[string]channelFigures{}, Notes: []string{}, Violations: []string{},
	}
	out.MostSending = r.mostSending
	out.Cost = r.readCost()
	if r.permits != nil {
		out.Notes = append(out.Notes, r.permits.report(r.provider, "email")...)
	}
	r.provider.mu.Lock()
	out.KeyRepeats = r.provider.repeats
	out.Abandoned = r.provider.abandoned
	r.provider.mu.Unlock()

	rows, err := r.pool.Query(ctx, `SELECT channel, status, count(*), COALESCE(sum(attempts), 0) FROM campaign_deliveries WHERE campaign_id = $1 GROUP BY 1, 2`, r.campaign)
	if err != nil {
		r.t.Fatalf("read deliveries: %v", err)
	}
	type row struct {
		channel, status string
		count, attempts int
	}
	found := []row{}
	for rows.Next() {
		var item row
		if err := rows.Scan(&item.channel, &item.status, &item.count, &item.attempts); err != nil {
			r.t.Fatalf("read deliveries: %v", err)
		}
		found = append(found, item)
	}
	rows.Close()
	for _, item := range found {
		figures, ok := out.Channels[item.channel]
		if !ok {
			figures = r.provider.figures(item.channel, r.limit)
			figures.Statuses = map[string]int{}
			figures.CompletedRate = r.completedRate(item.channel)
		}
		figures.Statuses[item.status] += item.count
		figures.AttemptsUsed += item.attempts
		out.Channels[item.channel] = figures
		if slices.Contains([]string{"QUEUED", "SENDING", "RETRYING"}, item.status) {
			out.Violations = append(out.Violations, fmt.Sprintf("%d %s messages still %s at the end", item.count, item.channel, item.status))
		}
	}
	for channel, figures := range out.Channels {
		if figures.PeakPerSecond > r.limit {
			out.Violations = append(out.Violations, fmt.Sprintf("%s: %d calls in one second at the provider, limit %d (%d calls over)", channel, figures.PeakPerSecond, r.limit, figures.OverLimitCalls))
		}
	}
	if out.Duplicates > 0 {
		out.Violations = append(out.Violations, fmt.Sprintf("%d messages accepted as new a second time", out.Duplicates))
	}
	if status != "COMPLETED" {
		out.Violations = append(out.Violations, "campaign ended as "+status)
	}
	return out
}

// completedRate is how fast deliveries reached SENT, over the middle 90%.
func (r *run) completedRate(channel string) float64 {
	var rate *float64
	err := r.pool.QueryRow(context.Background(), `
		WITH ordered AS (SELECT sent_at, ntile(20) OVER (ORDER BY sent_at) AS part FROM campaign_deliveries
		                 WHERE campaign_id = $1 AND channel = $2 AND sent_at IS NOT NULL)
		SELECT count(*) / NULLIF(EXTRACT(EPOCH FROM max(sent_at) - min(sent_at)), 0)::float8
		FROM ordered WHERE part BETWEEN 2 AND 19`, r.campaign, channel).Scan(&rate)
	if err != nil {
		r.t.Fatalf("completed rate: %v", err)
	}
	if rate == nil {
		return 0
	}
	return *rate
}

var (
	reportMu sync.Mutex
	results  []*result
)

// record adds a scenario to the report. A broken rule fails the test: finding where
// the design breaks is what these runs are for, so failures are expected to happen.
func record(t *testing.T, out *result) {
	t.Helper()
	reportMu.Lock()
	results = append(results, out)
	reportMu.Unlock()
	encoded, _ := json.Marshal(out)
	t.Logf("%s", encoded)
	for _, violation := range out.Violations {
		t.Errorf("%s: %s", out.Name, violation)
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	if err := writeReport(); err != nil {
		fmt.Fprintln(os.Stderr, "write report:", err)
		code = 1
	}
	os.Exit(code)
}

func writeReport() error {
	if len(results) == 0 {
		return nil
	}
	commit, _ := exec.Command("git", "rev-parse", "--short", "HEAD").Output()
	changed, _ := exec.Command("git", "status", "--porcelain", "--", "../../internal", "../../cmd").Output()
	state := strings.TrimSpace(string(commit))
	if len(changed) > 0 {
		state += " with uncommitted changes"
	}
	id := time.Now().UTC().Format("2006-01-02-1504")
	machine := fmt.Sprintf("%s/%s, %d CPUs, %s", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	if chosen := os.Getenv("RATE_LIMITER"); chosen != "" {
		machine += ", limiter " + chosen + " " + os.Getenv("PACING_INTERVAL_MS") + " ms"
	}
	encoded, err := json.MarshalIndent(map[string]any{"commit": state, "machine": machine, "limit": limit, "scale": os.Getenv("LOAD_SCALE"), "results": results}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll("results", 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join("results", id+".json"), append(encoded, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join("results", id+".md"), []byte(markdown(state, machine)), 0o644)
}

func markdown(commit, machine string) string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Delivery load run\n\nCommit: %s  \nMachine: %s  \nLimit: %d calls a second per channel, at the provider\n\n", commit, machine, limit)
	out.WriteString("| Scenario | Workers | Channel | Requests | Accepted | Duplicates | Peak in any second | Calls over | Request/s | Accepted/s | Completed/s | Longest gap (s) | Run (s) | Result |\n")
	out.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, item := range results {
		verdict := "pass"
		if len(item.Violations) > 0 {
			verdict = "**fail**"
		}
		channels := make([]string, 0, len(item.Channels))
		for channel := range item.Channels {
			channels = append(channels, channel)
		}
		slices.Sort(channels)
		for _, channel := range channels {
			f := item.Channels[channel]
			fmt.Fprintf(&out, "| %s | %d | %s | %d | %d | %d | %d | %d | %.1f | %.1f | %.1f | %.2f | %.0f | %s |\n",
				item.Name, item.Workers, channel, f.Requests, f.UniqueAccepted, item.Duplicates, f.PeakPerSecond, f.OverLimitCalls,
				f.RequestRate, f.AcceptedRate, f.CompletedRate, f.LongestGap, item.Seconds, verdict)
		}
	}
	out.WriteString("\n| Scenario | Channel | Peak sent | Peak in 100 ms | In 100 ms after longest gap | In 1 s after longest gap | Most holding a lease | DB statements / message | Redis commands / message | Claim calls | Claim ms |\n|---|---|---|---|---|---|---|---|---|---|---|\n")
	for _, item := range results {
		for channel, f := range item.Channels {
			fmt.Fprintf(&out, "| %s | %s | %d | %d | %d | %d | %d | %.1f | %.1f | %d | %.0f |\n", item.Name, channel, f.PeakSent, f.Peak100ms, f.AfterGap100ms, f.AfterGapSecond,
				item.MostSending, item.Cost.StatementsPerMessage, item.Cost.RedisPerMessage, item.Cost.ClaimCalls, item.Cost.ClaimMilliseconds)
		}
	}
	out.WriteString("\n")
	for _, item := range results {
		fmt.Fprintf(&out, "## %s\n\n%s\n\n%s\n\n", item.Name, item.Question, item.Setup)
		for _, note := range item.Notes {
			fmt.Fprintf(&out, "- %s\n", note)
		}
		for _, violation := range item.Violations {
			fmt.Fprintf(&out, "- **Broken:** %s\n", violation)
		}
		out.WriteString("\n")
	}
	return out.String()
}

var _ http.Handler = (*provider)(nil)
