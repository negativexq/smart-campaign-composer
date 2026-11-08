package content_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/content"
)

func TestABriefNamesItsCodeNextToAWordForOne(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		brief string
		want  []string
	}{
		{"İzmir'deki müşterilere %15 indirim kodu SONBAHAR15 ver.", []string{"SONBAHAR15"}},
		{"SONBAHAR15 kodunu kullanan müşterilere teşekkür et.", []string{"SONBAHAR15"}},
		{"Give 10% off with the code SPRING10.", []string{"SPRING10"}},
		{"Promo code: SPRING-10 for everyone.", []string{"SPRING-10"}},
		{`Kupon "YAZ24" ile ücretsiz kargo.`, []string{"YAZ24"}},
		{"Kodu A1B ya da kodu C2D olsun.", []string{"A1B", "C2D"}},
		{"Kodu SONBAHAR15, tekrar: kodu SONBAHAR15.", []string{"SONBAHAR15"}},
		// None of these is a code: a number, a word that is not capitals, a link, no word for a code.
		{"%15 indirim, 15 kodu yok.", nil},
		{"Tell them about the code of conduct.", nil},
		{"New iPhone15 cases. Link: https://example.com/SALE15", nil},
		{"SONBAHAR15 koleksiyonunu duyur.", nil},
	} {
		got := content.BriefCodes(test.brief)
		if !slices.Equal(got, test.want) && !(len(got) == 0 && len(test.want) == 0) {
			t.Errorf("BriefCodes(%q) = %v, want %v", test.brief, got, test.want)
		}
	}
}

func TestACodeIsCapitalsDigitsAndHyphens(t *testing.T) {
	t.Parallel()
	for code, want := range map[string]bool{
		"SONBAHAR15": true, "SPRING-10": true, "A1B": true, strings.Repeat("A", 30): true,
		"AB": false, strings.Repeat("A", 31): false, "sonbahar15": false, "-SALE": false, "SALE 15": false, "İNDİRİM": false, "SALE{}": false,
	} {
		if got := content.ValidCode(code); got != want {
			t.Errorf("ValidCode(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestACodeTypedOutBecomesTheVariable(t *testing.T) {
	t.Parallel()
	got := content.WithCode(content.Content{
		SMS:          "Use SONBAHAR15 or sonbahar15 today, not XSONBAHAR15.",
		EmailSubject: "SONBAHAR15 is yours",
		EmailBody:    "Your code is SONBAHAR15.",
		EmailCTAURL:  "https://example.com/SONBAHAR15",
	}, "SONBAHAR15")
	if got.SMS != "Use {{discount_code}} or {{discount_code}} today, not XSONBAHAR15." {
		t.Errorf("SMS = %q", got.SMS)
	}
	if got.EmailSubject != "{{discount_code}} is yours" || got.EmailBody != "Your code is {{discount_code}}." {
		t.Errorf("email = %q / %q", got.EmailSubject, got.EmailBody)
	}
	if got.EmailCTAURL != "https://example.com/SONBAHAR15" {
		t.Errorf("a link is an address, not text, and must stay as it is: %q", got.EmailCTAURL)
	}
	if got.DiscountCode != "SONBAHAR15" {
		t.Errorf("code = %q", got.DiscountCode)
	}
}

func TestEverySMSCarriesTheCode(t *testing.T) {
	t.Parallel()
	limits := content.Limits{Code: "SONBAHAR15"}
	without := content.Content{SMS: "Hi {{first_name}}, 15% off today.", EmailSubject: "Hello", EmailBody: "Body", DiscountCode: "SONBAHAR15"}
	if !hasReason(content.CheckMessages(without, limits), "missing_code") {
		t.Fatal("an SMS without the code passed")
	}
	with := without
	with.SMS = "Hi {{first_name}}, 15% off with {{discount_code}}."
	if issues := content.CheckMessages(with, limits); len(issues) > 0 {
		t.Fatalf("an SMS with the code and an email without it in the text should pass, since the email shows a box: %v", issues)
	}
	emailOnly := limits
	emailOnly.SkipSMS = true
	if issues := content.CheckMessages(content.Limits{SkipSMS: true, Code: "SONBAHAR15"}.Keep(without), emailOnly); len(issues) > 0 {
		t.Fatalf("an email-only campaign has no SMS to carry the code: %v", issues)
	}
}

func TestTheCodeVariableNeedsACode(t *testing.T) {
	t.Parallel()
	c := content.Content{SMS: "Use {{discount_code}}", EmailSubject: "Hello", EmailBody: "Body"}
	if !hasReason(content.CheckMessages(c, content.Limits{}), "unknown_variable") {
		t.Fatal("{{discount_code}} passed in a campaign with no code")
	}
}

func TestMessagesWrittenForAnotherCodeCannotGoOut(t *testing.T) {
	t.Parallel()
	c := content.Content{SMS: "Use {{discount_code}}", EmailSubject: "Hello", EmailBody: "Body", DiscountCode: "OLD10"}
	if !hasReason(content.CheckMessages(c, content.Limits{Code: "NEW20"}), "code_changed") {
		t.Fatal("messages written for OLD10 passed for NEW20")
	}
	c.DiscountCode, c.SMS = "", "Hello"
	if !hasReason(content.CheckMessages(c, content.Limits{Code: "NEW20"}), "code_changed") {
		t.Fatal("messages written before the campaign had a code passed")
	}
}

func TestTheCodeCountsAgainstTheSMSLimit(t *testing.T) {
	t.Parallel()
	code := strings.Repeat("A", 30)
	sms := strings.Repeat("x", 140) + "{{discount_code}}"
	if got := content.RenderedLength(sms, 0, code); got != 170 {
		t.Fatalf("length = %d, want 170", got)
	}
	issues := content.CheckMessages(content.Content{SMS: sms, EmailSubject: "Hello", EmailBody: "Body", DiscountCode: code}, content.Limits{Code: code})
	if !hasReason(issues, "too_long") {
		t.Fatal("an SMS that is 170 characters once the code is filled in passed")
	}
}

func TestTheCodeIsFilledInWhereverItIsWritten(t *testing.T) {
	t.Parallel()
	if got := content.Render("Hi {{first_name}}, use {{ discount_code }}.", "Ayşe", "SONBAHAR15"); got != "Hi Ayşe, use SONBAHAR15." {
		t.Fatalf("Render = %q", got)
	}
	if got := content.Render("Use {{discount_code}}", "Ayşe", ""); got != "Use {{discount_code}}" {
		t.Fatalf("without a code nothing is made up: %q", got)
	}
}

func TestAModelThatTypesTheCodeIsHeldToTheSameRules(t *testing.T) {
	t.Parallel()
	raw := []byte(`{"sms":"Hi {{first_name}}, code SONBAHAR15","email_subject":"Hello","email_body":"Body","discount_code":"FREE100"}`)
	got, err := content.ParseGenerated(raw, content.Limits{Code: "SONBAHAR15"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.SMS != "Hi {{first_name}}, code {{discount_code}}" || got.DiscountCode != "SONBAHAR15" {
		t.Fatalf("got %q with code %q; a model must not set the code", got.SMS, got.DiscountCode)
	}
	if _, err := content.ParseGenerated([]byte(`{"sms":"Hi {{first_name}}","email_subject":"Hello","email_body":"Body"}`), content.Limits{Code: "SONBAHAR15"}, ""); err == nil {
		t.Fatal("a model's SMS without the code passed")
	}
}

func TestEveryEmailShowsTheCodeInABox(t *testing.T) {
	t.Parallel()
	base := content.Content{EmailSubject: "Hello", EmailBody: "Your cart is waiting.", EmailHeadline: "Still here", EmailCTALabel: "Open", EmailCTAURL: "https://example.com", Language: "tr"}
	cases := map[string]content.Content{}
	for _, name := range content.EmailTemplates {
		c := base
		c.EmailTemplate = name
		cases[name] = c
	}
	custom := customDesignContent(blockDesign())
	custom.Language = "tr"
	cases["custom"] = custom
	for name, c := range cases {
		c.DiscountCode = "SONBAHAR15"
		email, err := content.RenderEmail(c, "Ayşe", "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.Contains(email.HTML, "SONBAHAR15") || !strings.Contains(email.HTML, "İndirim kodun") {
			t.Errorf("%s: the HTML has no code box", name)
		}
		if !strings.Contains(email.Text, "İndirim kodun: SONBAHAR15") {
			t.Errorf("%s: the plain text has no code: %q", name, email.Text)
		}
		// The box sits below the words and above the button.
		box, words, button := strings.Index(email.HTML, "SONBAHAR15"), strings.Index(email.HTML, "Your cart is waiting."), strings.Index(email.HTML, ">Open<")
		if !(words < box && box < button) {
			t.Errorf("%s: the box is not between the text and the button: %d %d %d", name, words, box, button)
		}

		c.DiscountCode = ""
		plain, err := content.RenderEmail(c, "Ayşe", "")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if strings.Contains(plain.HTML, "İndirim kodun") || strings.Contains(plain.Text, "İndirim kodun") {
			t.Errorf("%s: an email without a code shows a code box", name)
		}
	}
}

func TestTheCodeBoxSpeaksTheReadersLanguage(t *testing.T) {
	t.Parallel()
	for language, label := range map[string]string{"tr": "İndirim kodun", "en": "Your discount code", "de": "Ihr Rabattcode", "fr": "Votre code de réduction", "es": "Tu código de descuento"} {
		email, err := content.RenderEmail(content.Content{EmailSubject: "Hello", EmailBody: "Body", Language: language, DiscountCode: "SALE10"}, "Ayşe", "")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(email.Text, label+": SALE10") {
			t.Errorf("%s: %q", language, email.Text)
		}
	}
}

func TestARewriteCannotBringInANewCode(t *testing.T) {
	t.Parallel()
	if !content.AddsOffersOrLinks("Kodu YENI20 ile al.", "Kodu ESKI10 ile al.") {
		t.Fatal("a rewrite brought in a code that was never given")
	}
	if content.AddsOffersOrLinks("ESKI10 kodunu kullan.", "Kodu ESKI10 ile al.") {
		t.Fatal("the same code was taken for a new one")
	}
}

func hasReason(issues []content.Issue, reason string) bool {
	return slices.ContainsFunc(issues, func(issue content.Issue) bool { return issue.Reason == reason })
}
