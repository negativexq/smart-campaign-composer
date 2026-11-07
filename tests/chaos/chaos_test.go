//go:build chaos

// Package chaos breaks a running stack on purpose while a campaign is being sent
// and checks that every message still ends correctly. It needs `make up` and Docker,
// and it stops and starts services, so it stays out of `make test`.
package chaos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

const (
	rateLimit   = 100
	apiURL      = "http://localhost:8081"
	providerURL = "http://localhost:8083"
	brief       = "Send a reminder to users in Istanbul who added a product to their cart in the last 30 days and did not buy."
)

type progress struct {
	Total     int            `json:"total"`
	Processed int            `json:"processed"`
	ByStatus  map[string]int `json:"by_status"`
}

func request(method, url string, body any) (int, []byte, error) {
	var payload io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		payload = bytes.NewReader(encoded)
	}
	built, err := http.NewRequest(method, url, payload)
	if err != nil {
		return 0, nil, err
	}
	built.AddCookie(&http.Cookie{Name: "scc_session", Value: "chaos@example.com"})
	response, err := http.DefaultClient.Do(built)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	return response.StatusCode, data, err
}

func post(t *testing.T, path string, want int) []byte {
	t.Helper()
	status, data, err := request(http.MethodPost, apiURL+path, map[string]string{})
	if err != nil || status != want {
		t.Fatalf("POST %s: %d %s %v", path, status, data, err)
	}
	return data
}

// approve approves the campaign at base with the review revision it has now, as the
// review page does.
func approve(t *testing.T, base string) {
	t.Helper()
	status, data, err := request(http.MethodGet, apiURL+base, nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("GET %s: %d %s %v", base, status, data, err)
	}
	var reviewed struct {
		ReviewRevision string `json:"review_revision"`
	}
	if err := json.Unmarshal(data, &reviewed); err != nil {
		t.Fatal(err)
	}
	status, data, err = request(http.MethodPost, apiURL+base+"/approve", map[string]string{"revision": reviewed.ReviewRevision})
	if err != nil || status != http.StatusOK {
		t.Fatalf("POST %s/approve: %d %s %v", base, status, data, err)
	}
}

func compose(t *testing.T, args ...string) {
	t.Helper()
	command := exec.Command("docker", append([]string{"compose"}, args...)...)
	command.Dir = "../.."
	command.Env = os.Environ()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("docker compose %v: %v\n%s", args, err, output)
	}
}

// bringBack starts only the container the test stopped. A plain `up` could also
// recreate its dependencies, which would wipe the mock provider's counters
// and the duplicate check that relies on them.
func bringBack(t *testing.T, service string) {
	t.Helper()
	compose(t, "up", "-d", "--wait", "--no-deps", "--no-recreate", service)
}

// startSending creates a campaign and sends it. Each campaign has its own
// idempotency keys, so tests can share the same seeded people.
func startSending(t *testing.T) string {
	t.Helper()
	status, data, err := request(http.MethodPost, apiURL+"/api/campaigns", map[string]string{"brief": brief})
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create: %d %s %v", status, data, err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	base := "/api/campaigns/" + created.ID
	post(t, base+"/segment", http.StatusOK)
	post(t, base+"/generate", http.StatusOK)
	approve(t, base)
	post(t, base+"/dispatch", http.StatusAccepted)
	// Runs after the test has brought back what it stopped. A test that failed halfway
	// must not leave messages going out during the next one, which counts them.
	t.Cleanup(func() { settle(created.ID) })
	return created.ID
}

func read(id string) (progress, string, error) {
	var current progress
	status, data, err := request(http.MethodGet, apiURL+"/api/campaigns/"+id+"/progress", nil)
	if err != nil {
		return current, "", err
	}
	if status != http.StatusOK {
		return current, "", fmt.Errorf("progress answered %d", status)
	}
	if err := json.Unmarshal(data, &current); err != nil {
		return current, "", err
	}
	_, data, err = request(http.MethodGet, apiURL+"/api/campaigns/"+id, nil)
	if err != nil {
		return current, "", err
	}
	var view struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		return current, "", err
	}
	return current, view.Status, nil
}

func settle(id string) {
	for deadline := time.Now().Add(3 * time.Minute); time.Now().Before(deadline); time.Sleep(time.Second) {
		if _, status, err := read(id); err == nil && (status == "COMPLETED" || status == "COMPLETED_WITH_FAILURES") {
			return
		}
	}
}

func waitForProcessed(t *testing.T, id string, atLeast int) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for time.Now().Before(deadline) {
		if current, _, err := read(id); err == nil && current.Processed >= atLeast {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("campaign did not reach %d processed", atLeast)
}

func waitForEnd(t *testing.T, id string, within time.Duration) progress {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		current, status, err := read(id)
		if err == nil && (status == "COMPLETED" || status == "COMPLETED_WITH_FAILURES") {
			return current
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("campaign did not finish within %v", within)
	return progress{}
}

type channelStats struct {
	Accepted      int `json:"accepted"`
	PeakPerSecond int `json:"peak_per_second"`
}

func providerStats(t *testing.T) map[string]channelStats {
	t.Helper()
	status, data, err := request(http.MethodGet, providerURL+"/v1/stats", nil)
	if err != nil || status != http.StatusOK {
		t.Fatalf("provider stats: %d %v", status, err)
	}
	var stats map[string]channelStats
	if err := json.Unmarshal(data, &stats); err != nil {
		t.Fatalf("provider stats: %v: %s", err, data)
	}
	return stats
}

func accepted(t *testing.T) int {
	t.Helper()
	total := 0
	for _, channel := range providerStats(t) {
		total += channel.Accepted
	}
	return total
}

// checkEveryMessageEnded holds for all tests: nothing is left in flight and no
// person got a message the records do not show.
func checkEveryMessageEnded(t *testing.T, result progress, before int) {
	t.Helper()
	for _, open := range []string{"QUEUED", "SENDING", "RETRYING"} {
		if result.ByStatus[open] != 0 {
			t.Errorf("%d deliveries left %s: %v", result.ByStatus[open], open, result.ByStatus)
		}
	}
	if result.Processed != result.Total {
		t.Errorf("processed %d of %d", result.Processed, result.Total)
	}
	got := accepted(t) - before
	sent, unknown := result.ByStatus["SENT"], result.ByStatus["UNKNOWN"]
	// An UNKNOWN may or may not have reached the provider. Nothing else may differ.
	if got < sent || got > sent+unknown {
		t.Errorf("provider accepted %d distinct messages, records say %d sent and %d unknown", got, sent, unknown)
	}
	// Restarts and retries must not push the rate past the limit, as the provider counts it.
	for name, channel := range providerStats(t) {
		if channel.PeakPerSecond > rateLimit {
			t.Errorf("%s reached the provider at %d a second, limit %d", name, channel.PeakPerSecond, rateLimit)
		}
	}
	t.Logf("result: %v, provider accepted %d", result.ByStatus, got)
}

func TestAKilledWorkerLosesNothing(t *testing.T) {
	before := accepted(t)
	id := startSending(t)
	waitForProcessed(t, id, 300)
	compose(t, "kill", "-s", "SIGKILL", "worker")
	t.Cleanup(func() { bringBack(t, "worker") })

	stranded, _, err := read(id)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("after the kill: %v", stranded.ByStatus)

	bringBack(t, "worker")
	checkEveryMessageEnded(t, waitForEnd(t, id, 3*time.Minute), before)
}

func TestAStoppedWorkerLeavesNothingInFlight(t *testing.T) {
	before := accepted(t)
	id := startSending(t)
	waitForProcessed(t, id, 300)
	compose(t, "stop", "worker")
	t.Cleanup(func() { bringBack(t, "worker") })

	stopped, _, err := read(id)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.ByStatus["SENDING"] != 0 {
		t.Errorf("a graceful stop left %d in flight", stopped.ByStatus["SENDING"])
	}

	bringBack(t, "worker")
	checkEveryMessageEnded(t, waitForEnd(t, id, time.Minute), before)
}

func TestNothingIsSentWithoutTheRateLimiter(t *testing.T) {
	before := accepted(t)
	id := startSending(t)
	waitForProcessed(t, id, 300)
	compose(t, "stop", "redis")
	t.Cleanup(func() { bringBack(t, "redis") })

	time.Sleep(2 * time.Second) // calls that already had permission finish
	during := accepted(t)
	time.Sleep(5 * time.Second)
	if later := accepted(t); later != during {
		t.Fatalf("%d messages went out without the rate limiter", later-during)
	}

	bringBack(t, "redis")
	checkEveryMessageEnded(t, waitForEnd(t, id, 2*time.Minute), before)
}

// The mock provider keeps its counters in memory, so its counts start over here and
// are not compared with the deliveries.
func TestAProviderOutageIsWaitedOut(t *testing.T) {
	id := startSending(t)
	waitForProcessed(t, id, 300)
	compose(t, "stop", "mock-provider")
	t.Cleanup(func() { bringBack(t, "mock-provider") })

	// Long enough to use up every retry if a refused connection counted as an attempt.
	time.Sleep(30 * time.Second)
	bringBack(t, "mock-provider")

	result := waitForEnd(t, id, 2*time.Minute)
	if result.ByStatus["FAILED"] != 0 {
		t.Errorf("an outage must not fail messages: %v", result.ByStatus)
	}
	if result.Processed != result.Total {
		t.Errorf("processed %d of %d", result.Processed, result.Total)
	}
	t.Logf("result: %v", result.ByStatus)
}

func TestSendingResumesAfterADatabaseOutage(t *testing.T) {
	before := accepted(t)
	id := startSending(t)
	waitForProcessed(t, id, 300)
	compose(t, "stop", "postgres")
	t.Cleanup(func() { bringBack(t, "postgres") })

	time.Sleep(5 * time.Second)
	if status, _, err := request(http.MethodGet, apiURL+"/readyz", nil); err == nil && status == http.StatusOK {
		t.Error("the API must not report ready without its database")
	}

	bringBack(t, "postgres")
	checkEveryMessageEnded(t, waitForEnd(t, id, 3*time.Minute), before)
}
