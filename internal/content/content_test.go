package content_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/content"
)

const brief = "Send a 30% discount reminder to Istanbul users"

var limits = content.Limits{SMSName: 10, EmailName: 10}

func valid() content.Content {
	return content.Content{
		SMS:          "Hi {{first_name}}, your 30% discount is waiting.",
		EmailSubject: "{{first_name}}, your 30% discount",
		EmailBody:    "Hi {{first_name}},\n\nYour cart is waiting with 30% off.",
	}
}

func encode(c content.Content) []byte {
	return []byte(`{"sms":` + quote(c.SMS) + `,"email_subject":` + quote(c.EmailSubject) + `,"email_body":` + quote(c.EmailBody) + `}`)
}

func quote(text string) string {
	replaced := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(text)
	return `"` + replaced + `"`
}

func reasons(t *testing.T, err error) string {
	t.Helper()
	var invalid *content.ValidationError
	if !errors.As(err, &invalid) {
		t.Fatalf("want a ValidationError, got: %v", err)
	}
	found := []string{}
	for _, issue := range invalid.Issues {
		found = append(found, issue.Field+":"+issue.Reason)
	}
	return strings.Join(found, ",")
}

func TestParseGeneratedAcceptsValidOutput(t *testing.T) {
	t.Parallel()
	got, err := content.ParseGenerated(encode(valid()), limits, brief)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got != valid() {
		t.Fatalf("got %+v", got)
	}
}

func TestParseGeneratedRejectsUntrustedShapes(t *testing.T) {
	t.Parallel()
	for name, raw := range map[string]string{
		"not json":      `here is your message`,
		"truncated":     `{"sms":"Hi`,
		"trailing":      string(encode(valid())) + ` extra`,
		"unknown field": `{"sms":"a","email_subject":"b","email_body":"c","sql":"x"}`,
		"wrong type":    `{"sms":1,"email_subject":"b","email_body":"c"}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := reasons(t, parseOnly(raw)); !strings.Contains(got, "malformed_output") {
				t.Fatalf("reasons: %s", got)
			}
		})
	}
}

func parseOnly(raw string) error {
	_, err := content.ParseGenerated([]byte(raw), limits, brief)
	return err
}

func TestLimitsCountTheLongestNameInTheAudience(t *testing.T) {
	t.Parallel()
	// 160 characters as written, with a 14 character placeholder.
	sms := "{{first_name}}" + strings.Repeat("x", 146)
	if got := content.RenderedLength(sms, 14); got != 160 {
		t.Fatalf("length: %d", got)
	}

	fits := valid()
	fits.SMS = sms
	if issues := content.CheckMessages(fits, content.Limits{SMSName: 14, EmailName: 1}); len(issues) != 0 {
		t.Fatalf("exactly at the limit must pass: %+v", issues)
	}

	issues := content.CheckMessages(fits, content.Limits{SMSName: 15, EmailName: 1})
	if len(issues) != 1 || issues[0].Reason != "too_long" || issues[0].Field != "sms" {
		t.Fatalf("issues: %+v", issues)
	}
	if !strings.Contains(issues[0].Message, "161") {
		t.Fatalf("message should give the real length: %q", issues[0].Message)
	}
}

func TestLimitsCountCodePointsNotBytes(t *testing.T) {
	t.Parallel()
	c := valid()
	c.SMS = strings.Repeat("ş", 160)
	if issues := content.CheckMessages(c, limits); len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
}

func TestAShortNameStillAllowsForTheFallback(t *testing.T) {
	t.Parallel()
	if got := content.RenderedLength("{{first_name}}", 0); got != len(content.FirstNameFallback) {
		t.Fatalf("length: %d", got)
	}
}

func TestCheckMessagesFindsEachProblem(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*content.Content)
		want   string
	}{
		{"empty sms", func(c *content.Content) { c.SMS = "  " }, "sms:empty"},
		{"empty subject", func(c *content.Content) { c.EmailSubject = "" }, "email_subject:empty"},
		{"empty body", func(c *content.Content) { c.EmailBody = "" }, "email_body:empty"},
		{"long subject", func(c *content.Content) { c.EmailSubject = strings.Repeat("x", 81) }, "email_subject:too_long"},
		{"long body", func(c *content.Content) { c.EmailBody = strings.Repeat("x", content.MaxEmailBody+1) }, "email_body:too_long"},
		{"unknown variable", func(c *content.Content) { c.SMS = "Use {{coupon}}" }, "sms:unknown_variable"},
		{"broken variable", func(c *content.Content) { c.EmailBody = "Hi {{first_name" }, "email_body:malformed_variable"},
		{"line break in subject", func(c *content.Content) { c.EmailSubject = "a\nb" }, "email_subject:line_break"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := valid()
			tt.change(&c)
			found := []string{}
			for _, issue := range content.CheckMessages(c, limits) {
				found = append(found, issue.Field+":"+issue.Reason)
			}
			if strings.Join(found, ",") != tt.want {
				t.Fatalf("got %v, want %s", found, tt.want)
			}
		})
	}
}

func TestVariableSpacingIsAllowed(t *testing.T) {
	t.Parallel()
	c := valid()
	c.SMS = "Hi {{ first_name }}"
	if issues := content.CheckMessages(c, limits); len(issues) != 0 {
		t.Fatalf("issues: %+v", issues)
	}
}

func TestDiscountsMustMatchTheBrief(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, brief string
		change      func(*content.Content)
		want        string
	}{
		{"matches", brief, func(*content.Content) {}, ""},
		{"turkish format", "%30 indirim", func(c *content.Content) { c.SMS = "%30 indirim"; c.EmailSubject = "% 30"; c.EmailBody = "30% off" }, ""},
		{"invented", brief, func(c *content.Content) { c.SMS = "Your 50% and 30% discount" }, "sms:invented_discount"},
		{"invented in subject", brief, func(c *content.Content) { c.EmailSubject = "Now 40% off" }, "email_subject:invented_discount"},
		{"brief has none", "Tell users about free shipping", func(c *content.Content) {
			c.SMS = "Free shipping"
			c.EmailSubject = "Free shipping"
			c.EmailBody = "Now 10% off"
		}, "email_body:invented_discount"},
		{"sms forgets it", brief, func(c *content.Content) { c.SMS = "Your cart is waiting" }, "sms:missing_discount"},
		{"email forgets it", brief, func(c *content.Content) { c.EmailSubject = "Hello"; c.EmailBody = "Your cart" }, "email_body:missing_discount"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := valid()
			tt.change(&c)
			found := []string{}
			for _, issue := range content.CheckDiscounts(c, tt.brief) {
				found = append(found, issue.Field+":"+issue.Reason)
			}
			if strings.Join(found, ",") != tt.want {
				t.Fatalf("got %v, want %q", found, tt.want)
			}
		})
	}
}

func TestParseGeneratedTreatsDiscountsAsHardFailures(t *testing.T) {
	t.Parallel()
	c := valid()
	c.SMS = "Hi, enjoy 90% off and 30%"
	if got := reasons(t, func() error { _, err := content.ParseGenerated(encode(c), limits, brief); return err }()); got != "sms:invented_discount" {
		t.Fatalf("reasons: %s", got)
	}
}

func TestOfferSourcePrefersAnInstructionThatChangesTheDiscount(t *testing.T) {
	t.Parallel()
	if got := content.OfferSource(brief, "make it friendlier"); got != brief {
		t.Fatalf("got %q", got)
	}
	if got := content.OfferSource(brief, "make it 40% instead"); got != "make it 40% instead" {
		t.Fatalf("got %q", got)
	}
}

func TestSchemaForbidsExtraFields(t *testing.T) {
	t.Parallel()
	schema := content.JSONSchema()
	if schema["additionalProperties"] != false {
		t.Fatal("must forbid additional properties")
	}
	if len(schema["required"].([]string)) != 3 {
		t.Fatalf("required: %v", schema["required"])
	}
}

func TestRenderFillsTheNameOrTheFallback(t *testing.T) {
	t.Parallel()
	if got := content.Render("Hi {{first_name}}, {{ first_name }}!", "Ayşe"); got != "Hi Ayşe, Ayşe!" {
		t.Fatalf("got %q", got)
	}
	if got := content.Render("Hi {{first_name}}", " "); got != "Hi "+content.FirstNameFallback {
		t.Fatalf("got %q", got)
	}
}

func TestOnlyTheChosenChannelsAreChecked(t *testing.T) {
	t.Parallel()
	smsOnly := content.Limits{SkipEmail: true}
	if issues := content.CheckMessages(content.Content{SMS: "Hi"}, smsOnly); len(issues) != 0 {
		t.Fatalf("an unused email must not be required: %v", issues)
	}
	if issues := content.CheckMessages(content.Content{EmailSubject: "S", EmailBody: "B"}, smsOnly); len(issues) != 1 || issues[0].Field != content.FieldSMS {
		t.Fatalf("the chosen channel still is: %v", issues)
	}
	emailOnly := content.Limits{SkipSMS: true}
	if issues := content.CheckMessages(content.Content{EmailSubject: "S", EmailBody: "B"}, emailOnly); len(issues) != 0 {
		t.Fatalf("an unused SMS must not be required: %v", issues)
	}
}

func TestGeneratedTextForAnUnusedChannelIsDropped(t *testing.T) {
	t.Parallel()
	raw := `{"sms":"Hi","email_subject":"Sneaky","email_body":"Sneaky"}`
	got, err := content.ParseGenerated([]byte(raw), content.Limits{SkipEmail: true}, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got != (content.Content{SMS: "Hi"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestADiscountIsOnlyRequiredInTheChosenChannels(t *testing.T) {
	t.Parallel()
	got, err := content.ParseGenerated([]byte(`{"sms":"Hi, 20% off"}`), content.Limits{SkipEmail: true}, "20% off")
	if err != nil || got.SMS == "" {
		t.Fatalf("parse: %+v %v", got, err)
	}
	_, err = content.ParseGenerated([]byte(`{"sms":"Hi"}`), content.Limits{SkipEmail: true}, "20% off")
	if err == nil {
		t.Fatal("the chosen channel must still carry the offer")
	}
}

func TestSchemaAsksOnlyForTheChosenChannels(t *testing.T) {
	t.Parallel()
	fields := func(schema map[string]any) []string { return schema["required"].([]string) }
	if got := fields(content.JSONSchema()); len(got) != 3 {
		t.Fatalf("default: %v", got)
	}
	if got := fields(content.JSONSchema("sms")); len(got) != 1 || got[0] != content.FieldSMS {
		t.Fatalf("sms: %v", got)
	}
	email := content.JSONSchema("email")
	if got := fields(email); len(got) != 2 {
		t.Fatalf("email: %v", got)
	}
	if _, found := email["properties"].(map[string]any)[content.FieldSMS]; found {
		t.Fatal("sms must not be offered")
	}
}

func TestAddsOffersOrLinks(t *testing.T) {
	t.Parallel()
	const source = "Send 30% off to Ankara, see https://example.com/a"
	for text, want := range map[string]bool{
		"Send 30% off to Izmir, see https://example.com/a": false,
		"Send to Izmir":                  false,
		"Send 50% off to Izmir":          true,
		"Send to Izmir, see https://x.y": true,
		"Send %20 off, see example.com":  false,
		"Send 40% off":                   false,
	} {
		if got := content.AddsOffersOrLinks(text, source, "make it %20 or 40%"); got != want {
			t.Errorf("%q: got %v", text, got)
		}
	}
}
