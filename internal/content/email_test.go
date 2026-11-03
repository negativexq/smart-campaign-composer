package content_test

import (
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/content"
)

var fullEmail = content.Content{
	EmailSubject:   "{{first_name}}, your cart",
	EmailPreheader: "It is still waiting for you",
	EmailHeadline:  "Welcome back, {{first_name}}",
	EmailBody:      "Hi {{first_name}},\nsecond line\n\nA new paragraph.",
	EmailCTALabel:  "See your cart",
	EmailCTAURL:    "https://shop.example.com/cart?a=1&b=2",
}

func renderEmail(t *testing.T, c content.Content, name string) content.Email {
	t.Helper()
	email, err := content.RenderEmail(c, name, "https://app.example.com/api/unsubscribe/token")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return email
}

func TestEmailFillsTheNameEverywhere(t *testing.T) {
	t.Parallel()
	email := renderEmail(t, fullEmail, "Ayşe")
	if email.Subject != "Ayşe, your cart" {
		t.Fatalf("subject: %q", email.Subject)
	}
	for name, text := range map[string]string{"html": email.HTML, "text": email.Text} {
		if strings.Contains(text, "{{") {
			t.Errorf("%s has an unfilled variable: %s", name, text)
		}
		if !strings.Contains(text, "Welcome back, Ayşe") || !strings.Contains(text, "Hi Ayşe") {
			t.Errorf("%s lacks the name: %s", name, text)
		}
	}
}

func TestEmailHTMLIsResponsiveAndHasEveryPart(t *testing.T) {
	t.Parallel()
	html := renderEmail(t, fullEmail, "Ayşe").HTML
	for _, want := range []string{
		`name="viewport"`,
		"@media",
		"It is still waiting for you",
		`href="https://shop.example.com/cart?a=1&amp;b=2"`,
		">See your cart<",
		"second line",
		"<p",
		`href="https://app.example.com/api/unsubscribe/token"`,
		"Unsubscribe",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(html, "<script") {
		t.Error("an email has no scripts")
	}
}

func TestEmailEscapesWhatPeopleAndModelsWrite(t *testing.T) {
	t.Parallel()
	hostile := fullEmail
	hostile.EmailHeadline = `<img src=x onerror=alert(1)> {{first_name}}`
	hostile.EmailBody = `<script>alert(1)</script> & more`
	hostile.EmailPreheader = `"><b>bold</b>`
	html := renderEmail(t, hostile, `<b onclick="x()">Ann</b>`).HTML
	for _, bad := range []string{"<img", "<script", "<b>", "<b onclick"} {
		if strings.Contains(html, bad) {
			t.Errorf("%q reached the HTML", bad)
		}
	}
	if !strings.Contains(html, "&amp; more") {
		t.Error("text must stay readable after escaping")
	}
}

func TestEmailNeverLinksToAnUnsafeAddress(t *testing.T) {
	t.Parallel()
	hostile := fullEmail
	hostile.EmailCTAURL = "javascript:alert(1)"
	if html := renderEmail(t, hostile, "Ann").HTML; strings.Contains(html, "javascript:") {
		t.Fatalf("html: %s", html)
	}
}

func TestEmailWithOnlyTheRequiredPartsStillReads(t *testing.T) {
	t.Parallel()
	email := renderEmail(t, content.Content{EmailSubject: "Hello", EmailBody: "Just text."}, "")
	if !strings.Contains(email.HTML, "Just text.") || strings.Contains(email.HTML, "<a role=") {
		t.Fatalf("html: %s", email.HTML)
	}
	if strings.Contains(email.Text, "See") || !strings.Contains(email.Text, "Just text.") {
		t.Fatalf("text: %s", email.Text)
	}
}

func TestEmailPlainTextAlternativeHasTheLinks(t *testing.T) {
	t.Parallel()
	text := renderEmail(t, fullEmail, "Ayşe").Text
	for _, want := range []string{
		"Welcome back, Ayşe",
		"A new paragraph.",
		"See your cart: https://shop.example.com/cart?a=1&b=2",
		"Unsubscribe: https://app.example.com/api/unsubscribe/token",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "<") || strings.Contains(text, "&amp;") {
		t.Fatalf("plain text must not carry markup: %s", text)
	}
}

func TestEmailPreviewHasNoRealUnsubscribeLink(t *testing.T) {
	t.Parallel()
	email, err := content.RenderEmail(fullEmail, "Ayşe", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(email.HTML, "Unsubscribe") || !strings.Contains(email.Text, "Unsubscribe") {
		t.Fatalf("the footer belongs in a preview too: %s", email.Text)
	}
}

func TestOptionalEmailFieldsAreCheckedOnlyWhenWritten(t *testing.T) {
	t.Parallel()
	base := content.Content{SMS: "Hi", EmailSubject: "Hello", EmailBody: "Text"}
	if issues := content.CheckMessages(base, content.Limits{}); len(issues) != 0 {
		t.Fatalf("they are optional: %+v", issues)
	}
	long := strings.Repeat("a", 200)
	for name, tt := range map[string]struct {
		change func(*content.Content)
		field  string
		reason string
	}{
		"long preview text":  {func(c *content.Content) { c.EmailPreheader = long }, content.FieldEmailPreheader, "too_long"},
		"long headline":      {func(c *content.Content) { c.EmailHeadline = long }, content.FieldEmailHeadline, "too_long"},
		"unknown variable":   {func(c *content.Content) { c.EmailHeadline = "{{city}}" }, content.FieldEmailHeadline, "unknown_variable"},
		"long button label":  {func(c *content.Content) { c.EmailCTALabel, c.EmailCTAURL = long, "https://a.example" }, content.FieldEmailCTALabel, "too_long"},
		"label without link": {func(c *content.Content) { c.EmailCTALabel = "Go" }, content.FieldEmailCTAURL, "incomplete_button"},
		"link without label": {func(c *content.Content) { c.EmailCTAURL = "https://a.example" }, content.FieldEmailCTALabel, "incomplete_button"},
		"script link":        {func(c *content.Content) { c.EmailCTALabel, c.EmailCTAURL = "Go", "javascript:alert(1)" }, content.FieldEmailCTAURL, "invalid_link"},
		"relative link":      {func(c *content.Content) { c.EmailCTALabel, c.EmailCTAURL = "Go", "/cart" }, content.FieldEmailCTAURL, "invalid_link"},
		"variable in link":   {func(c *content.Content) { c.EmailCTALabel, c.EmailCTAURL = "Go", "https://a.example/{{first_name}}" }, content.FieldEmailCTAURL, "invalid_link"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			changed := base
			tt.change(&changed)
			issues := content.CheckMessages(changed, content.Limits{})
			for _, issue := range issues {
				if issue.Field == tt.field && issue.Reason == tt.reason {
					return
				}
			}
			t.Fatalf("issues: %+v", issues)
		})
	}
}

func TestAGeneratedLinkMustComeFromThePerson(t *testing.T) {
	t.Parallel()
	source := "Remind them. Link: https://shop.example.com/sale"
	if issues := content.CheckLinks(content.Content{EmailCTAURL: "https://shop.example.com/sale"}, source); len(issues) != 0 {
		t.Fatalf("given link: %+v", issues)
	}
	if issues := content.CheckLinks(content.Content{}, source); len(issues) != 0 {
		t.Fatalf("no link: %+v", issues)
	}
	issues := content.CheckLinks(content.Content{EmailCTAURL: "https://evil.example.com"}, source)
	if len(issues) != 1 || issues[0].Reason != "invented_link" || issues[0].Field != content.FieldEmailCTAURL {
		t.Fatalf("invented link: %+v", issues)
	}
}

func TestADiscountCannotBeInventedInTheHeadline(t *testing.T) {
	t.Parallel()
	c := content.Content{SMS: "20%", EmailSubject: "20%", EmailBody: "20%", EmailHeadline: "50% off", EmailPreheader: "60% off", EmailCTALabel: "Get 70%"}
	found := map[string]bool{}
	for _, issue := range content.CheckDiscounts(c, "a 20% discount") {
		found[issue.Field] = issue.Reason == "invented_discount"
	}
	for _, field := range []string{content.FieldEmailHeadline, content.FieldEmailPreheader, content.FieldEmailCTALabel} {
		if !found[field] {
			t.Errorf("%s: %v", field, found)
		}
	}
}

func TestAnUnusedEmailChannelDropsEveryEmailField(t *testing.T) {
	t.Parallel()
	kept := content.Limits{SkipEmail: true}.Keep(fullEmail)
	if kept != (content.Content{}) {
		t.Fatalf("kept: %+v", kept)
	}
}

func TestSchemaOffersTheEmailPartsButRequiresOnlySubjectAndBody(t *testing.T) {
	t.Parallel()
	schema := content.JSONSchema("email")
	properties := schema["properties"].(map[string]any)
	for _, field := range []string{content.FieldEmailPreheader, content.FieldEmailHeadline, content.FieldEmailCTALabel, content.FieldEmailCTAURL} {
		if _, ok := properties[field]; !ok {
			t.Errorf("missing %s", field)
		}
	}
	if required := schema["required"].([]string); len(required) != 2 {
		t.Fatalf("required: %v", required)
	}
	if _, ok := content.JSONSchema("sms")["properties"].(map[string]any)[content.FieldEmailHeadline]; ok {
		t.Fatal("an SMS campaign is not asked for a headline")
	}
}
