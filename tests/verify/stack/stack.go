// Package stack drives the verification stack that `make verify-up` starts: its
// API, its mock provider, its database and its containers. The load and security
// tests share it.
package stack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Brief is the campaign the tests send: about 15,400 messages on the default seed,
// split between email and SMS.
const Brief = "Send a 20% discount reminder to users in Istanbul who added a product to their cart in the last 30 days and did not buy."

// Project is the compose project the stack runs under.
func Project() string { return env("COMPOSE_PROJECT_NAME", "scc-verify") }

// APIURL is where the stack's API listens.
func APIURL() string { return "http://localhost:" + env("VERIFY_API_PORT", "18081") }

// ProviderURL is where the stack's mock provider listens.
func ProviderURL() string { return "http://localhost:" + env("VERIFY_PROVIDER_PORT", "18083") }

// WebURL is where the stack's nginx frontend listens.
func WebURL() string { return "http://localhost:" + env("VERIFY_WEB_PORT", "18080") }

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// Root is the repository root.
func Root() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..")
}

// Session is the cookie every API call carries unless it asks for none.
var Session = &http.Cookie{Name: "scc_session", Value: "verify@example.com"}

// Response is what a call returned.
type Response struct {
	Status int
	Body   []byte
	Header http.Header
}

// Request describes one call to the stack.
type Request struct {
	Method  string
	URL     string
	JSON    any    // encoded as the body when set
	Raw     []byte // the body as is, when JSON is not set
	Header  map[string]string
	Cookie  *http.Cookie // nil sends Session; use NoCookie to send none
	Timeout time.Duration
}

// NoCookie asks for a call without a session.
var NoCookie = &http.Cookie{}

// Do makes one call. A transport error fails the test.
func Do(t testing.TB, r Request) Response {
	t.Helper()
	var body io.Reader
	switch {
	case r.JSON != nil:
		encoded, err := json.Marshal(r.JSON)
		if err != nil {
			t.Fatal(err)
		}
		body = bytes.NewReader(encoded)
	case r.Raw != nil:
		body = bytes.NewReader(r.Raw)
	}
	request, err := http.NewRequest(r.Method, r.URL, body)
	if err != nil {
		t.Fatal(err)
	}
	if r.JSON != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	for name, value := range r.Header {
		request.Header.Set(name, value)
	}
	switch r.Cookie {
	case nil:
		request.AddCookie(Session)
	case NoCookie:
	default:
		request.AddCookie(r.Cookie)
	}
	timeout := r.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	response, err := (&http.Client{Timeout: timeout}).Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", r.Method, r.URL, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("%s %s: %v", r.Method, r.URL, err)
	}
	return Response{Status: response.StatusCode, Body: data, Header: response.Header}
}

// API calls the stack's API with the session cookie and wants the given status.
func API(t testing.TB, method, path string, body any, want int) []byte {
	t.Helper()
	response := Do(t, Request{Method: method, URL: APIURL() + path, JSON: body})
	if response.Status != want {
		t.Fatalf("%s %s: %d, want %d: %.300s", method, path, response.Status, want, response.Body)
	}
	return response.Body
}

// Decode reads a JSON body into a value of type T.
func Decode[T any](t testing.TB, data []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatalf("decode %.200s: %v", data, err)
	}
	return value
}

// Compose runs docker compose against the verification stack with extra
// environment, such as the provider faults for a scenario.
func Compose(t testing.TB, extra map[string]string, args ...string) string {
	t.Helper()
	output, err := ComposeErr(extra, args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

// ComposeErr is Compose for code that runs outside the test goroutine.
func ComposeErr(extra map[string]string, args ...string) (string, error) {
	files := []string{"compose", "-f", "compose.yaml", "-f", "tests/verify/compose.verify.yaml"}
	command := exec.Command("docker", append(files, args...)...)
	command.Dir = Root()
	command.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+Project())
	for name, value := range extra {
		command.Env = append(command.Env, name+"="+value)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker compose %v: %w\n%s", args, err, output)
	}
	return string(output), nil
}

// Docker runs a docker command and returns its output, failing the test on error.
func Docker(t testing.TB, args ...string) string {
	t.Helper()
	output, err := DockerErr(args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

// DockerErr is Docker for code that runs outside the test goroutine.
func DockerErr(args ...string) (string, error) {
	output, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("docker %v: %w\n%s", args, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

// Container is the name of the first container of a service.
func Container(service string) string { return Project() + "-" + service + "-1" }

// SQL runs one query in the stack's database and returns its rows, one per line,
// with columns separated by |.
func SQL(t testing.TB, query string, args ...any) string {
	t.Helper()
	return Docker(t, "exec", Container("postgres"), "psql", "-U", "scc", "-d", "scc", "-v", "ON_ERROR_STOP=1", "-AtF", "|", "-c", fmt.Sprintf(query, args...))
}

// Approved creates a campaign from the brief and takes it to approved.
func Approved(t testing.TB, brief string) string {
	t.Helper()
	created := Decode[struct{ ID string }](t, API(t, http.MethodPost, "/api/campaigns", map[string]string{"brief": brief}, http.StatusCreated))
	for _, step := range []string{"segment", "generate"} {
		API(t, http.MethodPost, "/api/campaigns/"+created.ID+"/"+step, map[string]string{}, http.StatusOK)
	}
	reviewed := Decode[struct {
		ReviewRevision string `json:"review_revision"`
	}](t, API(t, http.MethodGet, "/api/campaigns/"+created.ID, nil, http.StatusOK))
	API(t, http.MethodPost, "/api/campaigns/"+created.ID+"/approve", map[string]string{"revision": reviewed.ReviewRevision}, http.StatusOK)
	return created.ID
}

// Dispatch starts sending a campaign.
func Dispatch(t testing.TB, campaign string) {
	t.Helper()
	response := Do(t, Request{Method: http.MethodPost, URL: APIURL() + "/api/campaigns/" + campaign + "/dispatch", JSON: map[string]string{}, Timeout: 15 * time.Minute})
	if response.Status != http.StatusOK && response.Status != http.StatusAccepted {
		t.Fatalf("dispatch: %d %.300s", response.Status, response.Body)
	}
}

// Status is a campaign's status.
func Status(t testing.TB, campaign string) string {
	t.Helper()
	return Decode[struct{ Status string }](t, API(t, http.MethodGet, "/api/campaigns/"+campaign, nil, http.StatusOK)).Status
}

// StatusErr reads the campaign's status, returning an error instead of failing the
// test, for the moments the API cannot answer.
func StatusErr(campaign string) (string, error) {
	request, err := http.NewRequest(http.MethodGet, APIURL()+"/api/campaigns/"+campaign, nil)
	if err != nil {
		return "", err
	}
	request.AddCookie(Session)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %.200s", response.StatusCode, data)
	}
	var campaignStatus struct{ Status string }
	if err := json.Unmarshal(data, &campaignStatus); err != nil {
		return "", err
	}
	return campaignStatus.Status, nil
}

// WaitFinished waits until the campaign completes and returns its final status. An
// API that cannot answer for a while, as when the database is down, is waited out.
func WaitFinished(t testing.TB, campaign string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	status, last := "", error(nil)
	for {
		got, err := StatusErr(campaign)
		if err == nil {
			status = got
		}
		last = err
		if strings.HasPrefix(status, "COMPLETED") {
			return status
		}
		if time.Now().After(deadline) {
			t.Fatalf("campaign still %s after %s (last error: %v)", status, timeout, last)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// ProviderStats is what the mock provider counted for one channel since it started.
type ProviderStats struct {
	Accepted      int `json:"accepted"`
	Duplicates    int `json:"duplicates"`
	RateLimited   int `json:"rate_limited"`
	ServerErrors  int `json:"server_errors"`
	Unauthorized  int `json:"unauthorized"`
	Late          int `json:"late"`
	Throttled     int `json:"throttled"`
	PeakPerSecond int `json:"peak_per_second"`
	// PeakAt is when the busiest one-second window ended.
	PeakAt *time.Time `json:"peak_at,omitempty"`
}

// Provider reads the mock provider's counters by channel.
func Provider(t testing.TB) map[string]ProviderStats {
	t.Helper()
	stats, err := ProviderErr()
	if err != nil {
		t.Fatal(err)
	}
	return stats
}

// ProviderErr is Provider for code that runs outside the test goroutine.
func ProviderErr() (map[string]ProviderStats, error) {
	response, err := (&http.Client{Timeout: 10 * time.Second}).Get(ProviderURL() + "/v1/stats")
	if err != nil {
		return nil, fmt.Errorf("provider stats: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("provider stats: %d", response.StatusCode)
	}
	var stats map[string]ProviderStats
	if err := json.NewDecoder(response.Body).Decode(&stats); err != nil {
		return nil, fmt.Errorf("provider stats: %w", err)
	}
	return stats, nil
}

// WriteResult saves a run's results under tests/verify/results as name.json and,
// when markdown is given, name.md.
func WriteResult(t testing.TB, name string, value any, markdown string) {
	t.Helper()
	dir := filepath.Join(Root(), "tests", "verify", "results")
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".json"), append(encoded, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	if markdown != "" {
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(markdown), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("results: tests/verify/results/%s.json", name)
}

// Stamp names a result file by the time the run started.
func Stamp(kind string, started time.Time) string {
	return started.UTC().Format("2006-01-02-1504") + "-" + kind
}
