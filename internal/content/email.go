package content

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
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

const defaultPage = "#F5F7FA"

var (
	emailTemplate = template.Must(template.New("email").Parse(emailSource))
	blankLine     = regexp.MustCompile(`\n\s*\n`)

	templateVersions = readTemplateVersions()
)

// Email is one person's message, ready to hand to a provider.
type Email struct {
	Subject string `json:"subject"`
	HTML    string `json:"html"`
	Text    string `json:"text"`
}

// RenderOptions says where a message will be read.
type RenderOptions struct {
	// AssetBase is the public address images load from. Empty suits a preview in the app.
	AssetBase string
	// Editing marks each block so the template editor can select it, and shows unfinished
	// ones. It is never set for a message going to a person.
	Editing bool
}

// RenderEmail fills the campaign's template for a preview.
func RenderEmail(c Content, firstName, unsubscribeURL string) (Email, error) {
	return RenderEmailWith(c, firstName, unsubscribeURL, RenderOptions{})
}

// RenderEmailWith fills the campaign's template. People and models only ever write
// text, which html/template escapes, so neither can add markup or an
// unsafe link. Without an unsubscribeURL this is a preview, whose footer goes nowhere.
func RenderEmailWith(c Content, firstName, unsubscribeURL string, options RenderOptions) (Email, error) {
	firstName = NameFor(c.Language, firstName)
	footer := footerFor(c.Language)
	body := Render(c.EmailBody, firstName)
	data := struct {
		Subject, Preheader, Headline string
		Paragraphs                   [][]string
		CTALabel, CTAURL             string
		Unsubscribe                  string
		Lang, Received, StopLabel    string
		Page                         string
		Design                       designLook
		Blocks                       []emailBlock
		Editing                      bool
		Details                      string
		PadTop, PadBottom            bool
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
		Page:        defaultPage,
		Editing:     options.Editing,
	}
	name := templateName(c.EmailTemplate)
	parts := []string{}
	if renderableDesign(c) {
		name = customTemplate
		data.Page = c.EmailDesign.Page
		data.Design = lookOf(*c.EmailDesign)
		data.Details = c.EmailDesign.Footer
		data.Blocks = emailBlocks(c, firstName, options.AssetBase, options.Editing)
		if len(data.Blocks) > 0 {
			first, last := data.Blocks[0], data.Blocks[len(data.Blocks)-1]
			data.PadTop = !first.Full && first.Style != "band"
			data.PadBottom = !last.Full && last.Style != "band"
		}
		parts = blocksAsText(data.Blocks)
		if data.Details != "" {
			parts = append(parts, data.Details)
		}
	} else {
		if data.Headline != "" {
			parts = append(parts, data.Headline)
		}
		parts = append(parts, strings.TrimSpace(body))
		if data.CTAURL != "" {
			parts = append(parts, data.CTALabel+": "+data.CTAURL)
		}
	}
	if data.Unsubscribe == "" {
		data.Unsubscribe = "#"
	}
	var html bytes.Buffer
	if err := emailTemplate.ExecuteTemplate(&html, name, data); err != nil {
		return Email{}, fmt.Errorf("render email: %w", err)
	}
	if unsubscribeURL != "" {
		parts = append(parts, footer.unsubscribe+": "+unsubscribeURL)
	} else {
		parts = append(parts, footer.unsubscribe+": "+footer.linkLater)
	}
	return Email{Subject: data.Subject, HTML: html.String(), Text: strings.Join(parts, "\n\n")}, nil
}

// blocksAsText is the plain version, in the order the marketer laid the blocks out.
func blocksAsText(blocks []emailBlock) []string {
	parts := []string{}
	for _, block := range blocks {
		switch block.Type {
		case blockHeading, blockText:
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case blockButton:
			parts = append(parts, block.Label+": "+block.URL)
		}
	}
	return parts
}

type emailFooter struct{ lang, received, unsubscribe, linkLater string }

func footerFor(code string) emailFooter {
	known, ok := language.Get(code)
	if !ok {
		known, _ = language.Get(language.Fallback)
	}
	return emailFooter{known.Code, known.Received, known.Unsubscribe, known.LinkLater}
}

// EmailTemplateVersion identifies a design as it stands now and changes only when that
// design does, so an approval can record exactly which one it approved.
func EmailTemplateVersion(name string) string {
	return templateVersions[templateName(name)]
}

// Read at startup, because rendering rewrites a template's parsed form to escape it.
func readTemplateVersions() map[string]string {
	versions := make(map[string]string, len(EmailTemplates))
	for _, name := range EmailTemplates {
		sum := sha256.Sum256([]byte(emailTemplate.Lookup(name).Tree.Root.String()))
		versions[name] = hex.EncodeToString(sum[:])[:12]
	}
	return versions
}

// usableTemplate says whether a message's design exists. A marketer's own design has to
// travel with the message, so nothing has to look it up to send.
func usableTemplate(c Content) bool {
	return slices.Contains(EmailTemplates, c.EmailTemplate) || usableCustomDesign(c)
}

func usableCustomDesign(c Content) bool {
	return IsCustomTemplate(c.EmailTemplate) && c.EmailDesign != nil && c.EmailDesign.Check() == ""
}

// renderableDesign may still be unfinished, which only an editor's preview can be.
func renderableDesign(c Content) bool {
	return IsCustomTemplate(c.EmailTemplate) && c.EmailDesign != nil && c.EmailDesign.checkShape() == ""
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
