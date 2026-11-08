package content_test

import (
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/content"
)

const shopLink = "https://shop.example.com/sonbahar"

func TestEverySMSCarriesTheBriefsLink(t *testing.T) {
	t.Parallel()
	limits := content.Limits{Link: shopLink}
	without := content.Content{SMS: "Hi {{first_name}}, 15% off today.", EmailSubject: "Hello", EmailBody: "Body", EmailCTALabel: "Shop", EmailCTAURL: shopLink, Link: shopLink}
	if !hasReason(content.CheckMessages(without, limits), "missing_link") {
		t.Fatal("an SMS without the link passed")
	}
	with := without
	with.SMS = "Hi {{first_name}}, 15% off today: {{link}}"
	if issues := content.CheckMessages(with, limits); len(issues) > 0 {
		t.Fatalf("an SMS with the link and an email with the button should pass: %v", issues)
	}
	emailOnly := content.Limits{SkipSMS: true, Link: shopLink}
	if issues := content.CheckMessages(emailOnly.Keep(without), emailOnly); len(issues) > 0 {
		t.Fatalf("an email-only campaign has no SMS to carry the link: %v", issues)
	}
}

func TestEveryEmailButtonOpensTheBriefsLink(t *testing.T) {
	t.Parallel()
	limits := content.Limits{Link: shopLink}
	for name, c := range map[string]content.Content{
		"no button":    {SMS: "Hi {{link}}", EmailSubject: "Hello", EmailBody: "Body", Link: shopLink},
		"another link": {SMS: "Hi {{link}}", EmailSubject: "Hello", EmailBody: "Body", EmailCTALabel: "Shop", EmailCTAURL: "https://shop.example.com/other", Link: shopLink},
	} {
		if !hasReason(content.CheckMessages(c, limits), "missing_link") {
			t.Errorf("%s: an email whose button does not open the brief's link passed", name)
		}
	}
	smsOnly := content.Limits{SkipEmail: true, Link: shopLink}
	if issues := content.CheckMessages(smsOnly.Keep(content.Content{SMS: "Hi {{link}}", Link: shopLink}), smsOnly); len(issues) > 0 {
		t.Fatalf("an SMS-only campaign has no email to carry the link: %v", issues)
	}
}

func TestTheLinkVariableIsOnlyForAnSMSWithALink(t *testing.T) {
	t.Parallel()
	if !hasReason(content.CheckMessages(content.Content{SMS: "Go {{link}}", EmailSubject: "Hello", EmailBody: "Body"}, content.Limits{}), "unknown_variable") {
		t.Fatal("{{link}} passed in a campaign whose brief has no link")
	}
	c := content.Content{SMS: "Go {{link}}", EmailSubject: "Hello", EmailBody: "Open {{link}}", EmailCTALabel: "Shop", EmailCTAURL: shopLink, Link: shopLink}
	if !hasReason(content.CheckMessages(c, content.Limits{Link: shopLink}), "unknown_variable") {
		t.Fatal("{{link}} passed in an email, where the button carries the link")
	}
}

func TestMessagesWrittenForAnotherLinkCannotGoOut(t *testing.T) {
	t.Parallel()
	c := content.Content{SMS: "Go {{link}}", EmailSubject: "Hello", EmailBody: "Body", EmailCTALabel: "Shop", EmailCTAURL: "https://old.example.com", Link: "https://old.example.com"}
	if !hasReason(content.CheckMessages(c, content.Limits{Link: shopLink}), "link_changed") {
		t.Fatal("messages written for another link passed")
	}
	c.Link, c.SMS, c.EmailCTALabel, c.EmailCTAURL = "", "Hello", "", ""
	if !hasReason(content.CheckMessages(c, content.Limits{Link: shopLink}), "link_changed") {
		t.Fatal("messages written before the brief had a link passed")
	}
}

func TestTheShortLinkCountsAgainstTheSMSLimit(t *testing.T) {
	t.Parallel()
	if content.ShortLinkLength != len("https://url.short/")+8 {
		t.Fatalf("short link length = %d", content.ShortLinkLength)
	}
	sms := strings.Repeat("x", content.MaxSMS-content.ShortLinkLength) + "{{link}}"
	if got := content.RenderedLength(sms, 0, ""); got != content.MaxSMS {
		t.Fatalf("length = %d, want %d", got, content.MaxSMS)
	}
	c := content.Content{SMS: sms + "!", EmailSubject: "Hello", EmailBody: "Body", EmailCTALabel: "Shop", EmailCTAURL: shopLink, Link: shopLink}
	if !hasReason(content.CheckMessages(c, content.Limits{Link: shopLink}), "too_long") {
		t.Fatal("an SMS one character over once the short link is in passed")
	}
}

func TestALinkTypedOutInTheSMSBecomesTheVariable(t *testing.T) {
	t.Parallel()
	got := content.WithLink(content.Content{
		SMS:         "Shop " + shopLink + ". Not " + shopLink + "-old",
		EmailBody:   "Shop at " + shopLink,
		EmailCTAURL: shopLink,
	}, shopLink)
	if got.SMS != "Shop {{link}}. Not "+shopLink+"-old" {
		t.Errorf("SMS = %q", got.SMS)
	}
	if got.EmailBody != "Shop at "+shopLink || got.EmailCTAURL != shopLink || got.Link != shopLink {
		t.Errorf("the email keeps the link as written: %+v", got)
	}
}

func TestEachSMSGetsItsOwnShortLink(t *testing.T) {
	t.Parallel()
	c := content.Content{SMS: "Hi {{first_name}}, {{discount_code}}: {{ link }}", DiscountCode: "SALE10", Link: shopLink}
	if got := content.RenderSMS(c, "Ayşe", content.ShortLink("AbCd-_12")); got != "Hi Ayşe, SALE10: https://url.short/AbCd-_12" {
		t.Fatalf("RenderSMS = %q", got)
	}
}

func TestAModelThatTypesTheLinkOrMakesOneUpIsHeldToTheRules(t *testing.T) {
	t.Parallel()
	limits := content.Limits{Link: shopLink}
	raw := []byte(`{"sms":"Hi {{first_name}}, shop ` + shopLink + `","email_subject":"Hello","email_body":"Body","email_cta_label":"Shop","email_cta_url":"` + shopLink + `","link":"https://evil.example.com"}`)
	got, err := content.ParseGenerated(raw, limits, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.SMS != "Hi {{first_name}}, shop {{link}}" || got.Link != shopLink {
		t.Fatalf("got %q with link %q; a model must not set the link", got.SMS, got.Link)
	}
	if _, err := content.ParseGenerated([]byte(`{"sms":"Hi {{first_name}}","email_subject":"Hello","email_body":"Body","email_cta_label":"Shop","email_cta_url":"`+shopLink+`"}`), limits, ""); err == nil {
		t.Fatal("a model's SMS without the link passed")
	}
	invented := content.Content{SMS: "Hi {{link}} or https://evil.example.com/x", EmailBody: "See https://evil.example.com/y"}
	if issues := content.CheckTextLinks(invented, "Brief with "+shopLink); len(issues) != 2 {
		t.Fatalf("made-up links in the text: %v", issues)
	}
}
