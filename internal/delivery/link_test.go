package delivery_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
	"github.com/negativexq/smart-campaign-composer/internal/httpapi"
)

const shopLink = "https://shop.example.com/sonbahar"

var shortLink = regexp.MustCompile(`https://url\.short/([A-Za-z0-9_-]{8})\b`)

// Issue #29: the brief's link reached the email's button and no SMS.
func TestEverySMSSentCarriesItsOwnShortLink(t *testing.T) {
	t.Parallel()
	r := newRigFor(t, campaign.NewCampaign{Brief: brief + " Link: " + shopLink}, always(accepted, nil))
	r.drain(t)
	codes := map[string]bool{}
	sms := 0
	for _, message := range r.sender.calls {
		if strings.Contains(message.Body+message.Subject, "{{") {
			t.Fatalf("%s to %s has a variable left: %q", message.Channel, message.To, message.Body)
		}
		if message.Channel == "email" {
			if !strings.Contains(message.HTML, `href="`+shopLink+`"`) {
				t.Fatalf("the email's button to %s does not open the brief's link", message.To)
			}
			continue
		}
		sms++
		found := shortLink.FindStringSubmatch(message.Body)
		if found == nil || strings.Contains(message.Body, shopLink) {
			t.Fatalf("SMS to %s has no short link: %q", message.To, message.Body)
		}
		codes[found[1]] = true
	}
	if sms == 0 || len(codes) != sms {
		t.Fatalf("%d SMS got %d different short links; each needs its own", sms, len(codes))
	}

	// The short link leads to the brief's link and counts the click.
	mux := http.NewServeMux()
	httpapi.RegisterShortLinks(mux, r.service)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var code string
	for code = range codes {
		break
	}
	for _, method := range []string{http.MethodHead, http.MethodGet, http.MethodGet} {
		request, _ := http.NewRequest(method, server.URL+"/api/l/"+code, nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusFound || response.Header.Get("Location") != shopLink {
			t.Fatalf("%s: %d to %q", method, response.StatusCode, response.Header.Get("Location"))
		}
	}
	for _, unknown := range []string{"AAAAAAAA", "short", "AAAA%21AAA"} {
		response, err := client.Get(server.URL + "/api/l/" + unknown)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: %d, want 404", unknown, response.StatusCode)
		}
	}

	var clicks int
	if err := r.pool.QueryRow(context.Background(), `SELECT link_clicks FROM campaign_deliveries WHERE link_code = $1`, code).Scan(&clicks); err != nil {
		t.Fatal(err)
	}
	if clicks != 2 {
		t.Fatalf("clicks = %d, want 2: two visits, and a HEAD request is not one", clicks)
	}
	report, err := r.service.Report(context.Background(), r.id)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range report.Lifecycle {
		if want := map[string]int{"sms": 1, "email": 0}[step.Channel]; step.Clicked != want {
			t.Fatalf("%s clicked = %d, want %d: one person opened the link twice", step.Channel, step.Clicked, want)
		}
	}
}

func TestAnSMSWithoutALinkGetsNoShortLink(t *testing.T) {
	t.Parallel()
	r := newRigFor(t, campaign.NewCampaign{Brief: brief}, always(accepted, nil))
	r.drain(t)
	for _, message := range r.sender.calls {
		if strings.Contains(message.Body, "url.short") {
			t.Fatalf("a campaign without a link sent one: %q", message.Body)
		}
	}
	var codes int
	if err := r.pool.QueryRow(context.Background(), `SELECT count(*) FROM campaign_deliveries WHERE link_code IS NOT NULL`).Scan(&codes); err != nil {
		t.Fatal(err)
	}
	if codes != 0 {
		t.Fatalf("%d deliveries got a short code without a link", codes)
	}
}
