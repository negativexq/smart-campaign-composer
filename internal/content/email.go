package content

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"regexp"
	"slices"
	"strings"

	"github.com/negativexq/smart-campaign-composer/internal/language"
)

//go:embed email.html
var emailSource string

// EmailTemplates are the designs a campaign can choose. The first is the default.
var EmailTemplates = []string{"classic", "banner", "offer"}

var (
	emailTemplate = template.Must(template.New("email").Parse(emailSource))
	blankLine     = regexp.MustCompile(`\n\s*\n`)
)

// Email is one person's message, ready to hand to a provider.
type Email struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

// RenderEmail fills the campaign's template. People and models only ever write
// text, which html/template escapes, so neither can add markup or an
// unsafe link. Without an unsubscribeURL this is a preview, whose footer goes nowhere.
func RenderEmail(c Content, firstName, unsubscribeURL string) (Email, error) {
	firstName = NameFor(c.Language, firstName)
	footer := footerFor(c.Language)
	body := Render(c.EmailBody, firstName)
	data := struct {
		Subject, Preheader, Headline string
		Paragraphs                   [][]string
		CTALabel, CTAURL             string
		Unsubscribe                  string
		Lang, Received, StopLabel    string
	}{
		Subject:     Render(c.EmailSubject, firstName),
		Preheader:   Render(c.EmailPreheader, firstName),
		Headline:    Render(c.EmailHeadline, firstName),
		Paragraphs:  paragraphs(body),
		CTALabel:    Render(c.EmailCTALabel, firstName),
		CTAURL:      c.EmailCTAURL,
		Unsubscribe: unsubscribeURL,
		Lang:        footer.lang,
		Received:    footer.received,
		StopLabel:   footer.unsubscribe,
	}
	if data.Unsubscribe == "" {
		data.Unsubscribe = "#"
	}
	var html bytes.Buffer
	if err := emailTemplate.ExecuteTemplate(&html, templateName(c.EmailTemplate), data); err != nil {
		return Email{}, fmt.Errorf("render email: %w", err)
	}

	parts := []string{}
	if data.Headline != "" {
		parts = append(parts, data.Headline)
	}
	parts = append(parts, strings.TrimSpace(body))
	if data.CTAURL != "" {
		parts = append(parts, data.CTALabel+": "+data.CTAURL)
	}
	if unsubscribeURL != "" {
		parts = append(parts, footer.unsubscribe+": "+unsubscribeURL)
	} else {
		parts = append(parts, footer.unsubscribe+": "+footer.linkLater)
	}
	return Email{Subject: data.Subject, HTML: html.String(), Text: strings.Join(parts, "\n\n")}, nil
}

type emailFooter struct{ lang, received, unsubscribe, linkLater string }

func footerFor(code string) emailFooter {
	known, ok := language.Get(code)
	if !ok {
		known, _ = language.Get(language.Fallback)
	}
	return emailFooter{known.Code, known.Received, known.Unsubscribe, known.LinkLater}
}

// templateName falls back to the default, so older drafts and anything already
// stored still send.
func templateName(name string) string {
	if slices.Contains(EmailTemplates, name) {
		return name
	}
	return EmailTemplates[0]
}

func paragraphs(body string) [][]string {
	found := [][]string{}
	for _, block := range blankLine.Split(strings.ReplaceAll(body, "\r\n", "\n"), -1) {
		if block = strings.TrimSpace(block); block != "" {
			found = append(found, strings.Split(block, "\n"))
		}
	}
	return found
}
