package content_test

import (
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/content"
)

func collect(chunks ...string) map[string]string {
	got := map[string]string{}
	stream := content.NewFieldStream(func(field, text string) { got[field] += text })
	for _, chunk := range chunks {
		stream.Write(chunk)
	}
	return got
}

func TestFieldStreamDecodesAcrossAnyChunkBoundary(t *testing.T) {
	t.Parallel()
	raw := `{"sms":"Hi \"Ayşe\"\nç😀 \\ end","email_subject":"Sub","email_body":"Body\ttext"}`
	want := map[string]string{"sms": "Hi \"Ayşe\"\nç😀 \\ end", "email_subject": "Sub", "email_body": "Body\ttext"}

	for size := 1; size <= len(raw); size++ {
		var chunks []string
		for start := 0; start < len(raw); start += size {
			chunks = append(chunks, raw[start:min(start+size, len(raw))])
		}
		got := collect(chunks...)
		for field, text := range want {
			if got[field] != text {
				t.Fatalf("chunk size %d: %s = %q, want %q", size, field, got[field], text)
			}
		}
	}
}

func TestFieldStreamEmitsTextAsItArrives(t *testing.T) {
	t.Parallel()
	var events []string
	stream := content.NewFieldStream(func(field, text string) { events = append(events, field+"="+text) })

	stream.Write(`{"sms":"Hel`)
	if strings.Join(events, "|") != "sms=Hel" {
		t.Fatalf("partial value must be emitted right away: %v", events)
	}
	stream.Write(`lo"`)
	if strings.Join(events, "|") != "sms=Hel|sms=lo" {
		t.Fatalf("events: %v", events)
	}
}

func TestFieldStreamIgnoresUnknownFieldsAndOtherValues(t *testing.T) {
	t.Parallel()
	got := collect(`{"note":"secret","count":3,"nested":{"sms":"inner"},"list":["sms"],"flag":true,"sms":"real"}`)
	if len(got) != 1 || got["sms"] != "real" {
		t.Fatalf("got %v", got)
	}
}

func TestFieldStreamToleratesWhitespaceAndLeadingText(t *testing.T) {
	t.Parallel()
	got := collect("\n{ \"sms\" :\n \"a\" ,\n \"email_body\" : \"b\" }\n")
	if got["sms"] != "a" || got["email_body"] != "b" {
		t.Fatalf("got %v", got)
	}
}

func TestFieldStreamNeverPanicsOnGarbage(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`}}}`, `{"sms":"\`, `{"sms":"\uZZZZ"`, `{"sms":"\ud83d"`, `{"sms":`, `"sms":"x"`, "\xff\xfe"} {
		collect(raw)
	}
}

func TestStreamEmitsTheEmailParts(t *testing.T) {
	t.Parallel()
	got := map[string]string{}
	stream := content.NewFieldStream(func(field, text string) { got[field] += text })
	stream.Write(`{"email_headline":"Back","email_preheader":"Soon","email_cta_label":"Go","email_cta_url":"https://a.example"}`)
	if got[content.FieldEmailHeadline] != "Back" || got[content.FieldEmailPreheader] != "Soon" || got[content.FieldEmailCTALabel] != "Go" || got[content.FieldEmailCTAURL] != "https://a.example" {
		t.Fatalf("got: %v", got)
	}
}
