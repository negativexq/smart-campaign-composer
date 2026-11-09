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

	"github.com/negativexq/smart-campaign-composer/internal/language"
	"github.com/negativexq/smart-campaign-composer/internal/matching"
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
	FieldEmailTemplate  = "email_template"

	firstNameVariable = "first_name"
	// CodeVariable stands for the campaign's discount code. The backend fills it in, so a
	// model cannot mistype or translate the code.
	CodeVariable = "discount_code"
	// MinCodeLength and MaxCodeLength bound a discount code.
	MinCodeLength = 3
	MaxCodeLength = 30
	// LinkVariable stands for the brief's link in an SMS. Each message gets a short link
	// of its own in its place when it is sent, so the SMS stays short and a click can be
	// counted.
	LinkVariable = "link"
	// ShortLinkBase starts every short link. A deployment points this domain at the API's
	// /api/l/ path; the demo keeps it as a stand-in.
	ShortLinkBase = "https://url.short/"
	// ShortCodeLength is how many characters follow ShortLinkBase.
	ShortCodeLength = 8
	// ShortLinkLength is what {{link}} counts as in an SMS.
	ShortLinkLength = len(ShortLinkBase) + ShortCodeLength
	// VariantB marks the second version of a campaign that compares two.
	VariantB = "B"
)

var (
	variablePattern = regexp.MustCompile(`\{\{\s*([a-zA-Z0-9_]+)\s*\}\}`)
	// discountPattern reads a percent written with a sign or a word, in the languages
	// messages are written in: "%15", "15%", "yüzde 15", "15 percent", "15 Prozent".
	discountPattern = regexp.MustCompile(`%\s*(\d+)|(\d+)\s*%|(?i:\byüzde)\s+(\d+)\b|\b(\d+)\s+(?i:yüzde|percent|per\s+cent|prozent|por\s+ciento|pour\s*cent)\b`)
	codeShape       = regexp.MustCompile(`^[A-Z0-9][A-Z0-9-]*$`)
	// briefCode finds a code next to a word that names one: "kodu SONBAHAR15",
	// "SONBAHAR15 kodunu", "code SPRING10", "promo code: SPRING-10".
	briefCode = regexp.MustCompile(`(?i:\b(?:kod|kodu|kodunu|koduyla|kodlu|kupon|kuponu|code|coupon|promo)\b)[\s:"'“”]*(?:(?i:code|kodu)[\s:"'“”]*)?([A-Z0-9][A-Z0-9-]{2,29})\b|\b([A-Z0-9][A-Z0-9-]{2,29})['’]?[\s"'“”]*(?i:kod|kodu|kodunu|koduyla|kodlu|kupon|kuponu|kuponunu|code|coupon)\b`)
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
	// EmailTemplate is a person's choice of design. Models only write text.
	EmailTemplate string `json:"email_template"`
	// EmailDesign is the campaign's own copy of a marketer's design, when it uses one.
	EmailDesign *Design `json:"email_design,omitempty"`
	// Language says who this version is for. Older campaigns have none.
	Language string `json:"language,omitempty"`
	// Variant is "B" for the second version of a campaign that compares two.
	Variant string `json:"variant,omitempty"`
	// DiscountCode fills {{discount_code}}. The backend sets it from the campaign, never
	// from a model or a request, so approval freezes the code with the text.
	DiscountCode string `json:"discount_code,omitempty"`
	// Link is the address the SMS's short link opens and the email's button goes to. The
	// backend sets it from the brief, so approval freezes it with the text.
	Link string `json:"link,omitempty"`
}

// FallbackName is the word used for a person with no name in this language.
func FallbackName(code string) string {
	if known, ok := language.Get(code); ok {
		return known.MissingName
	}
	return FirstNameFallback
}

// NameFor is the name to fill in: the person's, or the word for this language.
func NameFor(language, firstName string) string {
	if strings.TrimSpace(firstName) == "" {
		return FallbackName(language)
	}
	return firstName
}

// ForLanguage picks the version a person reads: their own, or English when a campaign
// does not write in theirs. A campaign from before that rule may have neither, and
// then it is the main version rather than nothing.
func ForLanguage(main Content, translations []Content, wanted string) Content {
	for _, code := range []string{wanted, language.Fallback} {
		if main.Language == code {
			return main
		}
		for _, translation := range translations {
			if translation.Language == code {
				return translation
			}
		}
	}
	return main
}

// Versions is what an approved campaign stored, as much of it as sending needs.
type Versions struct {
	Content
	Translations []Content `json:"translations"`
	VariantB     *struct {
		Content
		Translations []Content `json:"translations"`
	} `json:"variant_b"`
}

// ForPerson picks the version someone receives.
func (v Versions) ForPerson(variant, language string) Content {
	if variant == VariantB && v.VariantB != nil {
		return ForLanguage(v.VariantB.Content, v.VariantB.Translations, language)
	}
	return ForLanguage(v.Content, v.Translations, language)
}

// OffersDiffer reports whether two versions promise different discounts.
func OffersDiffer(a, b Content) bool {
	first, second := allDiscounts(a), allDiscounts(b)
	slices.Sort(first)
	slices.Sort(second)
	return !slices.Equal(first, second)
}

func allDiscounts(c Content) []int {
	return discounts(strings.Join([]string{c.SMS, c.EmailSubject, c.EmailPreheader, c.EmailHeadline, c.EmailBody, c.EmailCTALabel}, "\n"))
}

// Limits holds the longest first name per channel in the audience, which
// decides whether a message fits for everyone rather than for the average user.
type Limits struct {
	SMSName   int `json:"sms_name_length"`
	EmailName int `json:"email_name_length"`
	// SkipSMS and SkipEmail mark channels the campaign does not use.
	SkipSMS   bool `json:"-"`
	SkipEmail bool `json:"-"`
	// Code is the campaign's discount code, or empty.
	Code string `json:"-"`
	// Link is the brief's link, or empty.
	Link string `json:"-"`
}

// Keep clears text for channels the campaign does not use, whoever wrote it.
func (l Limits) Keep(c Content) Content {
	if l.SkipSMS {
		c.SMS = ""
	}
	if l.SkipEmail {
		c.EmailSubject, c.EmailBody = "", ""
		c.EmailPreheader, c.EmailHeadline, c.EmailCTALabel, c.EmailCTAURL, c.EmailTemplate = "", "", "", "", ""
		c.EmailDesign = nil
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
	// Each message is a sentence of its own.
	return strings.Join(messages, " ")
}

// ParseGenerated decodes and validates untrusted model output. Discount
// mismatches fail here: a model must not invent or drop an offer.
func ParseGenerated(raw []byte, limits Limits, offerSource string) (Content, error) {
	var decoded struct {
		Content
		NotApplied []string `json:"not_applied"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return Content{}, malformed(err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Content{}, malformed("trailing content after the object")
	}
	generated := limits.Keep(decoded.Content)
	generated.EmailTemplate, generated.Language, generated.Variant = "", "", ""
	generated.EmailDesign = nil
	generated = WithLink(WithCode(generated, limits.Code), limits.Link)
	issues := append(CheckMessages(generated, limits), CheckDiscounts(generated, offerSource)...)
	issues = append(issues, CheckEmailCode(generated, limits)...)
	if len(issues) > 0 {
		return Content{}, &ValidationError{Issues: issues}
	}
	return generated, nil
}

// ReportedNotApplied reads what a model says it did not do. It is only ever shown,
// so an output that says nothing or cannot be read simply reports nothing.
func ReportedNotApplied(raw []byte) []string {
	var decoded struct {
		NotApplied []string `json:"not_applied"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil
	}
	reported := []string{}
	for _, note := range decoded.NotApplied {
		if note = strings.TrimSpace(note); note != "" {
			reported = append(reported, note)
		}
	}
	return reported
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
			issues = append(issues, checkField(rule.field, rule.label, rule.text, rule.max, rule.nameLength, c.DiscountCode, rule.field == FieldSMS && c.Link != "")...)
		}
	}
	issues = append(issues, CheckCode(c, limits)...)
	issues = append(issues, CheckLink(c, limits)...)
	if !limits.SkipEmail {
		if c.EmailTemplate != "" && !usableTemplate(c) {
			issues = append(issues, Issue{FieldEmailTemplate, "unknown_template", "This email template does not exist."})
		}
		issues = append(issues, checkEmailDesign(c, limits)...)
	}
	if strings.ContainsAny(c.EmailSubject, "\r\n") {
		issues = append(issues, Issue{FieldEmailSubject, "line_break", "Email subject must be a single line."})
	}
	return issues
}

func checkField(field, label, text string, limit, nameLength int, code string, link bool) []Issue {
	if strings.TrimSpace(text) == "" {
		return []Issue{{field, "empty", label + " is empty."}}
	}
	var issues []Issue
	if length := RenderedLength(text, nameLength, code); length > limit {
		issues = append(issues, Issue{field, "too_long", fmt.Sprintf(
			"%s is %d characters with the longest name in this audience; the limit is %d.", label, length, limit)})
	}
	for _, match := range variablePattern.FindAllStringSubmatch(text, -1) {
		switch {
		case match[1] == firstNameVariable:
		case match[1] == CodeVariable && code != "":
		case match[1] == CodeVariable:
			issues = append(issues, Issue{field, "unknown_variable", fmt.Sprintf(
				"%s uses {{%s}}, but this campaign has no discount code.", label, CodeVariable)})
		case match[1] == LinkVariable && link:
		case match[1] == LinkVariable && field != FieldSMS:
			issues = append(issues, Issue{field, "unknown_variable", fmt.Sprintf(
				"%s uses {{%s}}, which is only for the SMS. The email's button carries the link.", label, LinkVariable)})
		case match[1] == LinkVariable:
			issues = append(issues, Issue{field, "unknown_variable", fmt.Sprintf(
				"%s uses {{%s}}, but the brief has no link.", label, LinkVariable)})
		default:
			issues = append(issues, Issue{field, "unknown_variable", fmt.Sprintf(
				"%s uses {{%s}}; only {{first_name}} and {{discount_code}} are available.", label, match[1])})
		}
	}
	remainder := variablePattern.ReplaceAllString(text, "")
	// A lone brace is what a broken variable leaves behind, and nobody writes one to a customer.
	if strings.ContainsAny(remainder, "{}") {
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
			issues = append(issues, checkField(rule.field, rule.label, rule.text, rule.max, limits.EmailName, c.DiscountCode, false)...)
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
	if c.EmailCTAURL == "" || knownLink(c.EmailCTAURL, source) {
		return nil
	}
	return []Issue{{FieldEmailCTAURL, "invented_link", "The button link is not in the brief. Leave it out or use a link from the brief."}}
}

// Links lists the links in text, each once, in order. A link written without its
// scheme, as in www.shop.example.com/sale, is listed with https:// in front, since a
// button and a short link need a full address.
func Links(text string) []string {
	found := []string{}
	for _, link := range linkPattern.FindAllString(text, -1) {
		if link = FullLink(link); !slices.Contains(found, link) {
			found = append(found, link)
		}
	}
	return found
}

// FullLink puts https:// in front of a link written without a scheme.
func FullLink(link string) string {
	if scheme.MatchString(link) {
		return link
	}
	return "https://" + link
}

// knownLink says whether source gives link, as written or with its scheme left out.
func knownLink(link, source string) bool {
	return strings.Contains(source, link) || slices.Contains(Links(source), FullLink(strings.TrimSpace(link)))
}

// CheckTextLinks refuses a link in generated text that nobody gave, as CheckLinks does
// for the button: a model must not make up where people are sent.
func CheckTextLinks(c Content, source string) []Issue {
	issues := []Issue{}
	for _, field := range textFields(c) {
		for _, link := range linkPattern.FindAllString(field.text, -1) {
			if !knownLink(link, source) {
				issues = append(issues, Issue{field.name, "invented_link", fmt.Sprintf("It links to %s, which is not in the brief. Use a link from the brief.", link)})
				break
			}
		}
	}
	return issues
}

// RenderedLength is the length in characters once variables are filled with a
// name of nameLength characters, or the fallback if that is longer, and the code.
func RenderedLength(template string, nameLength int, code string) int {
	fill := max(nameLength, utf8.RuneCountInString(FirstNameFallback))
	length := utf8.RuneCountInString(template)
	for _, match := range variablePattern.FindAllStringSubmatch(template, -1) {
		switch {
		case match[1] == firstNameVariable:
			length += fill - utf8.RuneCountInString(match[0])
		case match[1] == CodeVariable && code != "":
			length += utf8.RuneCountInString(code) - utf8.RuneCountInString(match[0])
		case match[1] == LinkVariable:
			length += ShortLinkLength - utf8.RuneCountInString(match[0])
		}
	}
	return length
}

// Render fills in the variables for one person. A missing name gets the fallback,
// so nobody receives a message with a gap in it.
func Render(template, firstName, code string) string {
	if strings.TrimSpace(firstName) == "" {
		firstName = FirstNameFallback
	}
	return variablePattern.ReplaceAllStringFunc(template, func(variable string) string {
		switch name := variablePattern.FindStringSubmatch(variable)[1]; {
		case name == firstNameVariable:
			return firstName
		case name == CodeVariable && code != "":
			return code
		}
		return variable
	})
}

// RenderSMS fills in an SMS for one person: their name, the code and their short link.
func RenderSMS(c Content, firstName, shortLink string) string {
	text := Render(c.SMS, firstName, c.DiscountCode)
	if shortLink == "" {
		return text
	}
	return variablePattern.ReplaceAllStringFunc(text, func(variable string) string {
		if variablePattern.FindStringSubmatch(variable)[1] == LinkVariable {
			return shortLink
		}
		return variable
	})
}

// ShortLink is the address a message's short code stands for in an SMS.
func ShortLink(code string) string {
	return ShortLinkBase + code
}

// BriefLinks lists the links a brief names, each once, in order.
func BriefLinks(text string) []string {
	return Links(text)
}

// WithLink sets a version's link and turns the link written out in the SMS into
// {{link}}, so every SMS gets a short link in its place. The email keeps the link on
// its button, where its length does not matter. A button that opens the link written
// without its scheme, as the brief may have it, gets the full address.
func WithLink(c Content, link string) Content {
	c.Link = link
	if link == "" {
		return c
	}
	if button := strings.TrimSpace(c.EmailCTAURL); button != "" && FullLink(button) == link {
		c.EmailCTAURL = link
	}
	c.SMS = linkPattern.ReplaceAllStringFunc(c.SMS, func(found string) string {
		if FullLink(found) == link {
			return "{{" + LinkVariable + "}}"
		}
		return found
	})
	return c
}

// HasLink says whether text carries {{link}}.
func HasLink(text string) bool {
	for _, match := range variablePattern.FindAllStringSubmatch(text, -1) {
		if match[1] == LinkVariable {
			return true
		}
	}
	return false
}

// CheckLink holds a version to the brief's link: every SMS carries it as a short link,
// and every email's button opens it. A version written for another link, or for none,
// cannot go out with this one.
func CheckLink(c Content, limits Limits) []Issue {
	var issues []Issue
	if c.Link != limits.Link {
		issues = append(issues, Issue{FieldSMS, "link_changed", "These messages were written for another link. Write them again."})
	}
	if limits.Link == "" {
		return issues
	}
	if !limits.SkipSMS && strings.TrimSpace(c.SMS) != "" && !HasLink(c.SMS) {
		issues = append(issues, Issue{FieldSMS, "missing_link", fmt.Sprintf(
			"SMS does not carry the link %s from the brief. Add {{%s}} where it goes; it becomes a short link when sent.", limits.Link, LinkVariable)})
	}
	if !limits.SkipEmail && strings.TrimSpace(c.EmailBody) != "" && strings.TrimSpace(c.EmailCTAURL) != limits.Link {
		issues = append(issues, Issue{FieldEmailCTAURL, "missing_link", fmt.Sprintf(
			"The email's button must open %s from the brief.", limits.Link)})
	}
	return issues
}

// ValidCode says whether code can be a discount code: capital letters, digits and
// hyphens, so it reads the same in every language and fits an SMS.
func ValidCode(code string) bool {
	return len(code) >= MinCodeLength && len(code) <= MaxCodeLength && codeShape.MatchString(code)
}

// BriefCodes lists the discount codes a brief names, each once, in order.
func BriefCodes(text string) []string {
	found := []string{}
	for _, match := range briefCode.FindAllStringSubmatch(text, -1) {
		code := match[1] + match[2]
		// A code has a letter; "15" next to "kodu" is a number, not a code.
		if strings.ContainsAny(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && ValidCode(code) && !slices.Contains(found, code) {
			found = append(found, code)
		}
	}
	return found
}

// WithCode sets a version's code and turns every place the text spells the code out
// into {{discount_code}}, so it is filled in like the rest and checked the same way.
func WithCode(c Content, code string) Content {
	c.DiscountCode = code
	if code == "" {
		return c
	}
	spelled := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(code) + `\b`)
	variable := "{{" + CodeVariable + "}}"
	for _, field := range []*string{&c.SMS, &c.EmailSubject, &c.EmailBody, &c.EmailPreheader, &c.EmailHeadline, &c.EmailCTALabel} {
		*field = spelled.ReplaceAllLiteralString(*field, variable)
	}
	return c
}

// HasCode says whether text carries the discount code.
func HasCode(text string) bool {
	for _, match := range variablePattern.FindAllStringSubmatch(text, -1) {
		if match[1] == CodeVariable {
			return true
		}
	}
	return false
}

// CheckEmailCode keeps a written email from spelling out the code. The email shows
// it in a box below the text, so a body that writes it too says it twice. The subject
// and preview text may carry it, since they are read in the inbox, where the box is
// not. It holds for what a model writes: a person who puts it in the text means to.
func CheckEmailCode(c Content, limits Limits) []Issue {
	if limits.SkipEmail || limits.Code == "" {
		return nil
	}
	var issues []Issue
	for _, field := range []struct{ name, label, text string }{
		{FieldEmailHeadline, "Email headline", c.EmailHeadline},
		{FieldEmailBody, "Email body", c.EmailBody},
		{FieldEmailCTALabel, "Email button", c.EmailCTALabel},
	} {
		if HasCode(field.text) {
			issues = append(issues, Issue{field.name, "code_in_email", fmt.Sprintf(
				"%s writes the discount code, but the email already shows it in a box below the text. Leave it out of the %s.", field.label, strings.ToLower(field.label))})
		}
	}
	return issues
}

// CheckCode holds a version to the campaign's code. An SMS must carry it, since it is
// the whole message; an email always shows it in a box of its own. A version written
// for another code, or for none, cannot go out with this one.
func CheckCode(c Content, limits Limits) []Issue {
	var issues []Issue
	if c.DiscountCode != limits.Code {
		issues = append(issues, Issue{FieldSMS, "code_changed", "These messages were written for another discount code. Write them again."})
	}
	if limits.Code != "" && !limits.SkipSMS && strings.TrimSpace(c.SMS) != "" && !HasCode(c.SMS) {
		issues = append(issues, Issue{FieldSMS, "missing_code", fmt.Sprintf(
			"SMS does not carry the discount code %s. Add {{%s}} where it goes.", limits.Code, CodeVariable)})
	}
	return issues
}

// ReasonNoButton marks an email that will go out without a button.
const ReasonNoButton = "no_button"

// CheckButton points out an email with no button. A model only adds one when the brief
// has a link, so without this a template's button would disappear without a word. An email
// may still go without one, so this is for a person to judge.
func CheckButton(c Content) []Issue {
	if strings.TrimSpace(c.EmailBody) == "" || strings.TrimSpace(c.EmailCTALabel) != "" || strings.TrimSpace(c.EmailCTAURL) != "" {
		return nil
	}
	return []Issue{{FieldEmailCTAURL, ReasonNoButton, "The brief has no link, so this email has no button. You can add one in the Button section, or put a link in the brief and write the messages again."}}
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

// linkPattern finds a link with its scheme, one starting with www., or a domain
// followed by a path, as matching.WithoutLinks does for the audience. A number such
// as 3.5/5 is not a domain, because a domain ends in letters.
var (
	linkPattern = regexp.MustCompile(`(?i)(?:\bhttps?://|\bwww\.|\b(?:[a-z0-9-]+\.)+[a-z]{2,}/)[^\s<>"]*[^\s<>".,;:!?)]`)
	scheme      = regexp.MustCompile(`(?i)^https?://`)
)

// Discounts lists the percentages text mentions, each once, in order.
func Discounts(text string) []int {
	return discounts(text)
}

func discounts(text string) []int {
	found := []int{}
	for _, match := range discountPattern.FindAllStringSubmatch(text, -1) {
		value, err := strconv.Atoi(strings.Join(match[1:], ""))
		if err == nil && !slices.Contains(found, value) {
			found = append(found, value)
		}
	}
	return found
}

// OfferAgrees reports whether text mentions no discount other than percent.
func OfferAgrees(text string, percent int) bool {
	for _, found := range discounts(text) {
		if found != percent {
			return false
		}
	}
	return true
}

// AddsOffersOrLinks reports whether text has a discount, code or link that none of the
// sources has. Message writers trust these in a brief, so a model rewriting one
// must not bring in new ones.
func AddsOffersOrLinks(text string, sources ...string) bool {
	source := strings.Join(sources, "\n")
	known := discounts(source)
	for _, found := range discounts(text) {
		if !slices.Contains(known, found) {
			return true
		}
	}
	if slices.ContainsFunc(BriefCodes(text), func(code string) bool { return !slices.Contains(BriefCodes(source), code) }) {
		return true
	}
	return slices.ContainsFunc(linkPattern.FindAllString(text, -1), func(link string) bool { return !knownLink(link, source) })
}

// CheckFacts refuses generated text that states a city or number the source does
// not. A model must not make up where an offer applies or how long it lasts.
func CheckFacts(c Content, source string, cities []string) []Issue {
	named := matching.Cities(source, cities)
	numbers := matching.Numbers(source)
	sourceKey := matching.Key(source)
	issues := []Issue{}
	for _, field := range textFields(c) {
		for _, mention := range matching.Cities(field.text, cities) {
			if !slices.ContainsFunc(named, func(other matching.CityMention) bool { return other.City == mention.City }) {
				issues = append(issues, Issue{field.name, "invented_city", fmt.Sprintf("It mentions %s, which is not in the brief.", mention.City)})
				break
			}
		}
		for _, place := range matching.Places(field.text, cities) {
			if !strings.Contains(sourceKey, matching.Key(place)) {
				issues = append(issues, Issue{field.name, "invented_city", fmt.Sprintf("It mentions %s, which is not in the brief.", place)})
				break
			}
		}
		for _, number := range matching.Numbers(field.text) {
			if !slices.Contains(numbers, number) {
				issues = append(issues, Issue{field.name, "invented_number", fmt.Sprintf("It mentions %s, which is not in the brief.", number)})
				break
			}
		}
	}
	return issues
}

func textFields(c Content) []struct{ name, text string } {
	return []struct{ name, text string }{
		{FieldSMS, c.SMS},
		{FieldEmailSubject, c.EmailSubject},
		{FieldEmailPreheader, c.EmailPreheader},
		{FieldEmailHeadline, c.EmailHeadline},
		{FieldEmailBody, c.EmailBody},
		{FieldEmailCTALabel, c.EmailCTALabel},
	}
}

// CheckTargeting refuses generated text that tells a person how they were picked,
// by stating a length of time the rule selects on. The same length may be said when
// the marketer gives it a second time, because then it is also about the offer.
func CheckTargeting(c Content, windows []int, source string) []Issue {
	stated := matching.Durations(source)
	issues := []Issue{}
	for _, field := range textFields(c) {
		for _, duration := range matching.Durations(field.text) {
			index := slices.IndexFunc(windows, duration.Contains)
			if index < 0 {
				continue
			}
			mentions := 0
			for _, other := range stated {
				if other.Contains(windows[index]) {
					mentions++
				}
			}
			if mentions < 2 {
				issues = append(issues, Issue{field.name, "reveals_targeting", "It tells the person how they were picked. Write about the offer, and leave out how long ago they did something."})
				break
			}
		}
	}
	return issues
}

// MissingFromInstruction lists the numbers and cities an instruction names that the
// message does not, which is how a skipped instruction shows without asking the model.
func MissingFromInstruction(message, instruction string, cities []string) []string {
	said := matching.Numbers(message)
	saidKey := matching.Key(message)
	missing := []string{}
	for _, number := range matching.Numbers(instruction) {
		if !slices.Contains(said, number) && !slices.Contains(missing, number) {
			missing = append(missing, number)
		}
	}
	for _, mention := range matching.Cities(instruction, cities) {
		if !strings.Contains(saidKey, matching.Key(mention.City)) {
			missing = append(missing, mention.City)
		}
	}
	return missing
}

// DiscountsDiffer reports whether the SMS and the email promise different
// discounts. One channel saying nothing is left to the check against the brief.
func DiscountsDiffer(c Content) bool {
	sms := discounts(c.SMS)
	email := discounts(strings.Join([]string{c.EmailSubject, c.EmailPreheader, c.EmailHeadline, c.EmailBody, c.EmailCTALabel}, "\n"))
	if len(sms) == 0 || len(email) == 0 {
		return false
	}
	slices.Sort(sms)
	slices.Sort(email)
	return !slices.Equal(sms, email)
}

// NewDiscount reports whether an instruction names a discount the brief does not.
func NewDiscount(instruction, brief string) bool {
	known := discounts(brief)
	return slices.ContainsFunc(discounts(instruction), func(found int) bool { return !slices.Contains(known, found) })
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
	properties["not_applied"] = map[string]any{
		"type":        "array",
		"description": "Anything the instruction asked for that these messages do not do, and why",
		"items":       map[string]any{"type": "string"},
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             required,
		"properties":           properties,
	}
}
