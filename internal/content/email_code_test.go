package content_test

import (
	"errors"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/content"
)

// The email shows the code in a box below the text, so a model that also writes it in
// the text says it twice, as most did when they were allowed to.
func TestAWrittenEmailLeavesTheCodeToItsBox(t *testing.T) {
	t.Parallel()
	limits := content.Limits{Code: "KIS15"}
	for name, raw := range map[string]string{
		"spelled out":   `{"sms":"Hi {{discount_code}}","email_subject":"Hello","email_body":"Your code:\n\nKIS15"}`,
		"as variable":   `{"sms":"Hi {{discount_code}}","email_subject":"Hello","email_body":"Your code: {{discount_code}}"}`,
		"in headline":   `{"sms":"Hi {{discount_code}}","email_subject":"Hello","email_headline":"Code KIS15 inside","email_body":"Your cart is waiting."}`,
		"on the button": `{"sms":"Hi {{discount_code}}","email_subject":"Hello","email_body":"Your cart is waiting.","email_cta_label":"Use KIS15"}`,
	} {
		if _, err := content.ParseGenerated([]byte(raw), limits, ""); !hasReason(issuesOf(err), "code_in_email") {
			t.Errorf("%s: %v", name, err)
		}
	}
	inbox := `{"sms":"Hi {{discount_code}}","email_subject":"Your code KIS15","email_preheader":"Use {{discount_code}} today","email_body":"Your cart is waiting."}`
	if _, err := content.ParseGenerated([]byte(inbox), limits, ""); err != nil {
		t.Fatalf("the subject and preview text are read in the inbox, where the box is not: %v", err)
	}
	smsOnly := content.Limits{Code: "KIS15", SkipEmail: true}
	if _, err := content.ParseGenerated([]byte(`{"sms":"Hi {{discount_code}}"}`), smsOnly, ""); err != nil {
		t.Fatalf("an SMS-only campaign has no email: %v", err)
	}
}

// A person who writes the code into the email means to, so only a model is held to it.
func TestAPersonMayWriteTheCodeIntoTheEmail(t *testing.T) {
	t.Parallel()
	limits := content.Limits{Code: "KIS15"}
	edited := content.Content{SMS: "Hi {{discount_code}}", EmailSubject: "Hello", EmailBody: "Your code: {{discount_code}}", DiscountCode: "KIS15"}
	if hasReason(content.CheckMessages(edited, limits), "code_in_email") {
		t.Fatal("an edited email with the code was refused")
	}
}

func issuesOf(err error) []content.Issue {
	var invalid *content.ValidationError
	if errors.As(err, &invalid) {
		return invalid.Issues
	}
	return nil
}
