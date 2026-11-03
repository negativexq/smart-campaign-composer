// Package content is the trust boundary for generated messages: untrusted JSON
// becomes a Content only after the rules every message must meet.
package content

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// MaxSMS and the other limits are in characters, after variables are filled.
	MaxSMS          = 160
	MaxEmailSubject = 80
	MaxEmailBody    = 5000

	// FirstNameFallback replaces {{first_name}} for users without a name.
	FirstNameFallback = "there"

	FieldSMS          = "sms"
	FieldEmailSubject = "email_subject"
	FieldEmailBody    = "email_body"

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
}

// Limits holds the longest first name per channel in the audience, which
// decides whether a message fits for everyone rather than for the average user.
type Limits struct {
	SMSName   int `json:"sms_name_length"`
	EmailName int `json:"email_name_length"`
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
	}{
		{FieldSMS, "SMS", c.SMS, MaxSMS, limits.SMSName},
		{FieldEmailSubject, "Email subject", c.EmailSubject, MaxEmailSubject, limits.EmailName},
		{FieldEmailBody, "Email body", c.EmailBody, MaxEmailBody, limits.EmailName},
	} {
		issues = append(issues, checkField(rule.field, rule.label, rule.text, rule.max, rule.nameLength)...)
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
// Each message must carry the offer and none may mention another one.
func CheckDiscounts(c Content, source string) []Issue {
	allowed := discounts(source)
	var issues []Issue
	for _, part := range []struct{ field, label, text string }{
		{FieldSMS, "SMS", c.SMS},
		{FieldEmailSubject, "Email subject", c.EmailSubject},
		{FieldEmailBody, "Email body", c.EmailBody},
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
		if !slices.Contains(discounts(c.SMS), offer) {
			issues = append(issues, Issue{FieldSMS, "missing_discount", fmt.Sprintf("SMS does not mention the %d%% discount from the brief.", offer)})
		}
		if !slices.Contains(discounts(emailText), offer) {
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
func JSONSchema() map[string]any {
	text := func(description string) map[string]any {
		return map[string]any{"type": "string", "description": description}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{FieldSMS, FieldEmailSubject, FieldEmailBody},
		"properties": map[string]any{
			FieldSMS:          text("SMS text"),
			FieldEmailSubject: text("Email subject on a single line"),
			FieldEmailBody:    text("Email body in plain text"),
		},
	}
}
