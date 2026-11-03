// Package content is the trust boundary for generated messages: untrusted JSON
// becomes a Content only after the rules every message must meet.
package content

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxSMS and the other limits are in characters, after variables are filled.
	MaxSMS            = 160
	MaxEmailSubject   = 80
	MaxEmailBody      = 5000
	MaxEmailPreheader = 120
	MaxEmailHeadline  = 80
	MaxEmailCTALabel  = 30
	MaxEmailCTAURL    = 2000

	// FirstNameFallback replaces {{first_name}} for users without a name.
	FirstNameFallback = "there"

	FieldSMS            = "sms"
	FieldEmailSubject   = "email_subject"
	FieldEmailBody      = "email_body"
	FieldEmailPreheader = "email_preheader"
	FieldEmailHeadline  = "email_headline"
	FieldEmailCTALabel  = "email_cta_label"
	FieldEmailCTAURL    = "email_cta_url"

	firstNameVariable = "first_name"
)

var (
	variablePattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)
	discountPattern = regexp.MustCompile(`%\s*(\d+)|(\d+)\s*%`)
)

// Content is one campaign's messages, with {{first_name}} left unfilled.
type Content struct {
	SMS          string `json:"sms"`
	EmailSubject string `json:"email_subject"`
	EmailBody    string `json:"email_body"`
	// The rest of the email is optional, so older drafts stay valid.
	EmailPreheader string `json:"email_preheader"`
	EmailHeadline  string `json:"email_headline"`
	EmailCTALabel  string `json:"email_cta_label"`
	EmailCTAURL    string `json:"email_cta_url"`
}

// Limits holds the longest first name per channel in the audience, which
// decides whether a message fits for everyone rather than for the average user.
type Limits struct {
	SMSName   int `json:"sms_name_length"`
	EmailName int `json:"email_name_length"`
	// SkipSMS and SkipEmail mark channels the campaign does not use.
	SkipSMS   bool `json:"-"`
	SkipEmail bool `json:"-"`
}

// Keep clears text for channels the campaign does not use, whoever wrote it.
func (l Limits) Keep(c Content) Content {
	if l.SkipSMS {
		c.SMS = ""
	}
	if l.SkipEmail {
		c.EmailSubject, c.EmailBody = "", ""
		c.EmailPreheader, c.EmailHeadline, c.EmailCTALabel, c.EmailCTAURL = "", "", "", ""
	}
	return c
}

// Issue is one problem with a field. Reason is a fixed code, safe as a metric
// label; Message can quote the content.
type Issue struct {
	Field   string `json:"field"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

// ValidationError lists everything wrong with some content.
type ValidationError struct {
	Issues []Issue
}

func (e *ValidationError) Error() string {
	messages := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		messages = append(messages, issue.Message)
	}
	return strings.Join(messages, "; ")
}

// ParseGenerated decodes and validates untrusted model output. Discount
// mismatches fail here: a model must not invent or drop an offer.
func ParseGenerated(raw []byte, limits Limits, offerSource string) (Content, error) {
	var decoded Content
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Content{}, malformed(err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Content{}, malformed("trailing content after the object")
	}
	decoded = limits.Keep(decoded)
	issues := append(CheckMessages(decoded, limits), CheckDiscounts(decoded, offerSource)...)
	if len(issues) > 0 {
		return Content{}, &ValidationError{Issues: issues}
	}
	return decoded, nil
}

func malformed(detail string) *ValidationError {
	return &ValidationError{Issues: []Issue{{
		Reason:  "malformed_output",
		Message: "output is not valid JSON for the schema: " + detail,
	}}}
}

// CheckMessages applies the rules that hold no matter who wrote the message.
func CheckMessages(c Content, limits Limits) []Issue {
	var issues []Issue
	for _, rule := range []struct {
		field, label, text string
		max, nameLength    int
		skip               bool
	}{
		{FieldSMS, "SMS", c.SMS, MaxSMS, limits.SMSName, limits.SkipSMS},
		{FieldEmailSubject, "Email subject", c.EmailSubject, MaxEmailSubject, limits.EmailName, limits.SkipEmail},
		{FieldEmailBody, "Email body", c.EmailBody, MaxEmailBody, limits.EmailName, limits.SkipEmail},
	} {
		if !rule.skip {
			issues = append(issues, checkField(rule.field, rule.label, rule.text, rule.max, rule.nameLength)...)
		}
	}
	if !limits.SkipEmail {
		issues = append(issues, checkEmailDesign(c, limits)...)
	}
	if strings.ContainsAny(c.EmailSubject, "\r\n") {
		issues = append(issues, Issue{FieldEmailSubject, "line_break", "Email subject must be a single line."})
	}
	return issues
}

func checkField(field, label, text string, limit, nameLength int) []Issue {
	if strings.TrimSpace(text) == "" {
		return []Issue{{field, "empty", label + " is empty."}}
	}
	var issues []Issue
	if length := RenderedLength(text, nameLength); length > limit {
		issues = append(issues, Issue{field, "too_long", fmt.Sprintf(
			"%s is %d characters with the longest name in this audience; the limit is %d.", label, length, limit)})
	}
	for _, match := range variablePattern.FindAllStringSubmatch(text, -1) {
		if match[1] != firstNameVariable {
			issues = append(issues, Issue{field, "unknown_variable", fmt.Sprintf(
				"%s uses {{%s}}; only {{first_name}} is available.", label, match[1])})
		}
	}
	remainder := variablePattern.ReplaceAllString(text, "")
	if strings.Contains(remainder, "{{") || strings.Contains(remainder, "}}") {
		issues = append(issues, Issue{field, "malformed_variable", label + " has a broken variable; write it as {{first_name}}."})
	}
	return issues
}

func checkEmailDesign(c Content, limits Limits) []Issue {
	var issues []Issue
	for _, rule := range []struct {
		field, label, text string
		max                int
	}{
		{FieldEmailPreheader, "Preview text", c.EmailPreheader, MaxEmailPreheader},
		{FieldEmailHeadline, "Headline", c.EmailHeadline, MaxEmailHeadline},
		{FieldEmailCTALabel, "Button label", c.EmailCTALabel, MaxEmailCTALabel},
	} {
		if strings.TrimSpace(rule.text) != "" {
			issues = append(issues, checkField(rule.field, rule.label, rule.text, rule.max, limits.EmailName)...)
		}
	}
	label, link := strings.TrimSpace(c.EmailCTALabel), strings.TrimSpace(c.EmailCTAURL)
	switch {
	case label != "" && link == "":
		issues = append(issues, Issue{FieldEmailCTAURL, "incomplete_button", "The button has a label but no link."})
	case label == "" && link != "":
		issues = append(issues, Issue{FieldEmailCTALabel, "incomplete_button", "The button has a link but no label."})
	case link != "" && !validLink(link):
		issues = append(issues, Issue{FieldEmailCTAURL, "invalid_link", "The button link must be a full http or https address."})
	}
	return issues
}

func validLink(link string) bool {
	parsed, err := url.Parse(link)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" &&
		utf8.RuneCountInString(link) <= MaxEmailCTAURL && !strings.ContainsAny(link, "{} \t\n")
}

// CheckLinks refuses a generated link that nobody gave: a model must not make up
// where people are sent. A person editing the message may set any valid link.
func CheckLinks(c Content, source string) []Issue {
	if c.EmailCTAURL == "" || strings.Contains(source, c.EmailCTAURL) {
		return nil
	}
	return []Issue{{FieldEmailCTAURL, "invented_link", "The button link is not in the brief. Leave it out or use a link from the brief."}}
}

// RenderedLength is the length in characters once variables are filled with a
// name of nameLength characters, or the fallback if that is longer.
func RenderedLength(template string, nameLength int) int {
	fill := max(nameLength, utf8.RuneCountInString(FirstNameFallback))
	length := utf8.RuneCountInString(template)
	for _, match := range variablePattern.FindAllStringSubmatch(template, -1) {
		if match[1] == firstNameVariable {
			length += fill - utf8.RuneCountInString(match[0])
		}
	}
	return length
}

// Render fills in the variables for one person. A missing name gets the fallback,
// so nobody receives a message with a gap in it.
func Render(template, firstName string) string {
	if strings.TrimSpace(firstName) == "" {
		firstName = FirstNameFallback
	}
	return variablePattern.ReplaceAllStringFunc(template, func(variable string) string {
		if variablePattern.FindStringSubmatch(variable)[1] == firstNameVariable {
			return firstName
		}
		return variable
	})
}

// CheckDiscounts compares percentages in the messages with those in source.
// Each message written must carry the offer and none may mention another one. A
// channel with no text is left out here, and CheckMessages decides if it may be.
func CheckDiscounts(c Content, source string) []Issue {
	allowed := discounts(source)
	var issues []Issue
	for _, part := range []struct{ field, label, text string }{
		{FieldSMS, "SMS", c.SMS},
		{FieldEmailSubject, "Email subject", c.EmailSubject},
		{FieldEmailBody, "Email body", c.EmailBody},
		{FieldEmailPreheader, "Preview text", c.EmailPreheader},
		{FieldEmailHeadline, "Headline", c.EmailHeadline},
		{FieldEmailCTALabel, "Button label", c.EmailCTALabel},
	} {
		for _, found := range discounts(part.text) {
			if !slices.Contains(allowed, found) {
				issues = append(issues, Issue{part.field, "invented_discount", fmt.Sprintf(
					"%s mentions a %d%% discount that the brief does not.", part.label, found)})
			}
		}
	}
	emailText := c.EmailSubject + " " + c.EmailBody
	for _, offer := range allowed {
		if c.SMS != "" && !slices.Contains(discounts(c.SMS), offer) {
			issues = append(issues, Issue{FieldSMS, "missing_discount", fmt.Sprintf("SMS does not mention the %d%% discount from the brief.", offer)})
		}
		if strings.TrimSpace(emailText) != "" && !slices.Contains(discounts(emailText), offer) {
			issues = append(issues, Issue{FieldEmailBody, "missing_discount", fmt.Sprintf("Email does not mention the %d%% discount from the brief.", offer)})
		}
	}
	return issues
}

func discounts(text string) []int {
	found := []int{}
	for _, match := range discountPattern.FindAllStringSubmatch(text, -1) {
		value, err := strconv.Atoi(match[1] + match[2])
		if err == nil && !slices.Contains(found, value) {
			found = append(found, value)
		}
	}
	return found
}

// OfferSource picks the text that defines the offer: an instruction that names a
// discount overrides the brief, because a person just asked for it.
func OfferSource(brief, instruction string) string {
	if len(discounts(instruction)) > 0 {
		return instruction
	}
	return brief
}

// JSONSchema is the output format requested from LLM providers. It only shapes
// the output; length and variable rules are enforced by ParseGenerated.
func JSONSchema(channels ...string) map[string]any {
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	required := []string{}
	properties := map[string]any{}
	if len(channels) == 0 || slices.Contains(channels, "sms") {
		required = append(required, FieldSMS)
		properties[FieldSMS] = text("SMS text")
	}
	if len(channels) == 0 || slices.Contains(channels, "email") {
		required = append(required, FieldEmailSubject, FieldEmailBody)
		properties[FieldEmailSubject] = text("Email subject on a single line")
		properties[FieldEmailBody] = text("Email body in plain text, with a blank line between paragraphs")
		properties[FieldEmailPreheader] = text("Preview text shown after the subject in an inbox, adding to it rather than repeating it")
		properties[FieldEmailHeadline] = text("Short headline at the top of the email")
		properties[FieldEmailCTALabel] = text("Button label, only when the brief gives a link")
		properties[FieldEmailCTAURL] = text("Button link, copied exactly from the brief; empty when the brief gives none")
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}
