package content

import (
	"bytes"
	_ "embed"
	"fmt"
	"html/template"
	"regexp"
	"strings"
)

//go:embed email.html
var emailSource string

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

// RenderEmail fills the one template every campaign uses. People and models only
// ever write text, which html/template escapes, so neither can add markup or an
// unsafe link. Without an unsubscribeURL this is a preview, whose footer goes nowhere.
func RenderEmail(c Content, firstName, unsubscribeURL string) (Email, error) {
	body := Render(c.EmailBody, firstName)
	data := struct {
		Subject, Preheader, Headline string
		Paragraphs                   [][]string
		CTALabel, CTAURL             string
		Unsubscribe                  string
	}{
		Subject:     Render(c.EmailSubject, firstName),
		Preheader:   Render(c.EmailPreheader, firstName),
		Headline:    Render(c.EmailHeadline, firstName),
		Paragraphs:  paragraphs(body),
		CTALabel:    Render(c.EmailCTALabel, firstName),
		CTAURL:      c.EmailCTAURL,
		Unsubscribe: unsubscribeURL,
	}
	if data.Unsubscribe == "" {
		data.Unsubscribe = "#"
	}
	var html bytes.Buffer
	if err := emailTemplate.Execute(&html, data); err != nil {
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
		parts = append(parts, "Unsubscribe: "+unsubscribeURL)
	} else {
		parts = append(parts, "Unsubscribe: the link is added when this is sent")
	}
	return Email{Subject: data.Subject, HTML: html.String(), Text: strings.Join(parts, "\n\n")}, nil
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
