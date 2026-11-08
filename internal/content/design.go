package content

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// customPrefix starts the template name of a marketer's own design.
const customPrefix = "custom:"

const (
	customTemplate     = "custom"
	maxDesignName      = 60
	maxDesignFooter    = 200
	maxBrandName       = 60
	maxDesignBlocks    = 20
	maxBlockHeading    = 200
	maxBlockText       = 1000
	maxBlockAlt        = 125
	minBlockWidth      = 10
	fullWidth          = 100
	contentWidth       = 600
	sideMargins        = 80
	readableOnWhite    = 4.5
	tintStrength       = 0.08
	darkInk            = "#172033"
	whiteInk           = "#FFFFFF"
	hexColorComponents = 3

	blockLogo    = "logo"
	blockImage   = "image"
	blockHeading = "heading"
	blockText    = "text"
	blockButton  = "button"
	blockDivider = "divider"
	// blockCode is the discount code's box. Designs cannot hold one: it follows the body
	// slot whenever the campaign has a code.
	blockCode = "code"

	// Slots are the blocks whose words come from the campaign, not the template.
	SlotHeadline = "headline"
	SlotBody     = "body"
	SlotCTA      = "cta"
)

var (
	hexColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
	blockID  = regexp.MustCompile(`^[A-Za-z0-9-]{1,40}$`)
	assetID  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

	slotTypes = map[string]string{SlotHeadline: blockHeading, SlotBody: blockText, SlotCTA: blockButton}
)

// Block is one row of a marketer's email: an image, some words, a button or a line.
type Block struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Slot says the campaign fills this block, so the AI can only ever change its words.
	Slot string `json:"slot,omitempty"`
	Text string `json:"text,omitempty"`
	// Label and URL belong to a button that says the same in every campaign.
	Label string `json:"label,omitempty"`
	URL   string `json:"url,omitempty"`
	// AssetID, Alt, Width (percent of the column) and Link belong to images and logos.
	AssetID string `json:"asset_id,omitempty"`
	Alt     string `json:"alt,omitempty"`
	Width   int    `json:"width,omitempty"`
	Link    string `json:"link,omitempty"`
	// Align is left, center or right. Style is how a heading stands out.
	Align string `json:"align,omitempty"`
	Style string `json:"style,omitempty"`
}

// Design is a marketer's own email: a document of blocks, never markup. It cannot add
// a script, break the layout or remove the unsubscribe footer, which is not a block.
type Design struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Accent string `json:"accent"`
	Page   string `json:"page"`
	Button string `json:"button"`
	// Corners is the sheet's corner radius in pixels.
	Corners int    `json:"corners"`
	Font    string `json:"font"`
	// Footer is optional sender details shown above the unsubscribe link.
	Footer string  `json:"footer,omitempty"`
	Blocks []Block `json:"blocks"`
}

// Brand is what new templates start from.
type Brand struct {
	Name        string `json:"name"`
	LogoAssetID string `json:"logo_asset_id"`
	Accent      string `json:"accent"`
	Page        string `json:"page"`
	Button      string `json:"button"`
	Font        string `json:"font"`
	Footer      string `json:"footer"`
}

// DefaultBrand is the brand kit before anyone has set one.
func DefaultBrand() Brand {
	return Brand{Accent: "#315BE8", Page: defaultPage, Button: "rounded", Font: "sans"}
}

// Clean trims what a person typed.
func (b Brand) Clean() Brand {
	b.Name, b.Footer = strings.TrimSpace(b.Name), strings.TrimSpace(b.Footer)
	return b
}

// Check reports the first thing wrong with a brand kit, as a sentence for the marketer.
func (b Brand) Check() string {
	switch {
	case utf8.RuneCountInString(b.Name) > maxBrandName || strings.ContainsAny(b.Name, "\r\n"):
		return "Keep the brand name to one line of up to 60 characters."
	case b.LogoAssetID != "" && !assetID.MatchString(b.LogoAssetID):
		return "Choose the logo from your images."
	case !hexColor.MatchString(b.Accent) || !hexColor.MatchString(b.Page):
		return "Colors must look like #315BE8."
	case !slices.Contains([]string{"square", "rounded", "pill"}, b.Button):
		return "Choose a button shape."
	case !slices.Contains([]string{"sans", "serif"}, b.Font):
		return "Choose a font."
	case utf8.RuneCountInString(b.Footer) > maxDesignFooter:
		return "Keep the footer details to 200 characters."
	}
	return ""
}

// AssetPath is where an image is served. A sent email puts the public address before it.
func AssetPath(id string) string { return "/api/assets/" + id + "/file" }

// CustomTemplateName is what a campaign stores to say it uses this design.
func CustomTemplateName(id string) string { return customPrefix + id }

// IsCustomTemplate says whether a template name points to a marketer's own design.
func IsCustomTemplate(name string) bool {
	return strings.HasPrefix(name, customPrefix) && len(name) > len(customPrefix)
}

// CustomTemplateID is the design a template name points to.
func CustomTemplateID(name string) string { return strings.TrimPrefix(name, customPrefix) }

// Clean trims what a person typed and drops words from blocks the campaign fills, so a
// template cannot hide text there.
func (d Design) Clean() Design {
	d.Name, d.Footer = strings.TrimSpace(d.Name), strings.TrimSpace(d.Footer)
	blocks := make([]Block, len(d.Blocks))
	for i, block := range d.Blocks {
		block.Text, block.Alt = strings.TrimSpace(block.Text), strings.TrimSpace(block.Alt)
		block.Label, block.URL, block.Link = strings.TrimSpace(block.Label), strings.TrimSpace(block.URL), strings.TrimSpace(block.Link)
		if block.Slot != "" {
			block.Text, block.Label, block.URL = "", "", ""
		}
		blocks[i] = block
	}
	d.Blocks = blocks
	return d
}

// Check reports the first thing keeping a design from being saved or sent, as a sentence
// for the marketer.
func (d Design) Check() string {
	if d.Name == "" || utf8.RuneCountInString(d.Name) > maxDesignName {
		return "Give the template a name of up to 60 characters."
	}
	if problem := d.checkShape(); problem != "" {
		return problem
	}
	for _, block := range d.Blocks {
		if problem := block.checkFinished(); problem != "" {
			return problem
		}
	}
	return ""
}

// checkShape is what rendering needs. An unfinished template passes, so it can be previewed.
func (d Design) checkShape() string {
	switch {
	case !hexColor.MatchString(d.Accent) || !hexColor.MatchString(d.Page):
		return "Colors must look like #315BE8."
	case !slices.Contains([]string{"square", "rounded", "pill"}, d.Button):
		return "Choose a button shape."
	case !slices.Contains([]int{0, 8, 16}, d.Corners):
		return "Choose a corner size."
	case !slices.Contains([]string{"sans", "serif"}, d.Font):
		return "Choose a font."
	case utf8.RuneCountInString(d.Footer) > maxDesignFooter:
		return "Keep the footer details to 200 characters."
	case len(d.Blocks) > maxDesignBlocks:
		return "A template can have up to 20 blocks."
	}
	seenIDs, seenSlots := map[string]bool{}, map[string]bool{}
	for _, block := range d.Blocks {
		if problem := block.checkShape(); problem != "" {
			return problem
		}
		if seenIDs[block.ID] || (block.Slot != "" && seenSlots[block.Slot]) {
			return "Each block must appear once."
		}
		seenIDs[block.ID], seenSlots[block.Slot] = true, block.Slot != ""
	}
	if !seenSlots[SlotHeadline] || !seenSlots[SlotBody] || !seenSlots[SlotCTA] {
		return "Keep the headline, message and button the AI writes for each campaign."
	}
	return ""
}

func (b Block) checkShape() string {
	types := []string{blockLogo, blockImage, blockHeading, blockText, blockButton, blockDivider}
	switch {
	case !blockID.MatchString(b.ID) || !slices.Contains(types, b.Type):
		return "This template has a block that is not known."
	case b.Slot != "" && slotTypes[b.Slot] != b.Type:
		return "This template has a block that is not known."
	case !slices.Contains([]string{"", "left", "center", "right"}, b.Align):
		return "Choose how the block lines up."
	case !slices.Contains([]string{"", "plain", "band", "box", "display", "rule"}, b.Style):
		return "Choose a headline style."
	case b.AssetID != "" && !assetID.MatchString(b.AssetID):
		return "Choose the image from your images."
	case (b.Type == blockImage || b.Type == blockLogo) && (b.Width < minBlockWidth || b.Width > fullWidth):
		return "An image's width must be between 10% and 100%."
	case b.Type == blockHeading && (utf8.RuneCountInString(b.Text) > maxBlockHeading || strings.ContainsAny(b.Text, "\r\n")):
		return "Keep a heading to one line of up to 200 characters."
	case utf8.RuneCountInString(b.Text) > maxBlockText:
		return "Keep a text block to 1000 characters."
	case utf8.RuneCountInString(b.Label) > MaxEmailCTALabel:
		return fmt.Sprintf("Keep a button's label to %d characters.", MaxEmailCTALabel)
	case utf8.RuneCountInString(b.Alt) > maxBlockAlt:
		return "Keep an image description to 125 characters."
	}
	return ""
}

func (b Block) checkFinished() string {
	switch {
	case b.Slot != "":
		return ""
	case b.Type == blockImage || b.Type == blockLogo:
		switch {
		case b.AssetID == "":
			return "Choose a picture for each image block, or remove the block."
		case b.Alt == "":
			return "Describe each image, for people whose email hides pictures."
		case b.Link != "" && !validLink(b.Link):
			return "An image's link must be a full http or https address."
		}
	case b.Type == blockHeading || b.Type == blockText:
		if strings.TrimSpace(b.Text) == "" {
			return "Write something in each heading and text block, or remove the block."
		}
		return checkFixedWords(b.Text)
	case b.Type == blockButton:
		switch {
		case b.Label == "":
			return "Give each button a label, or remove the block."
		case !validLink(b.URL):
			return "A button's link must be a full http or https address."
		}
		return checkFixedWords(b.Label)
	}
	return ""
}

func checkFixedWords(text string) string {
	if issues := checkField("", "Text", text, math.MaxInt, 0, ""); len(issues) > 0 {
		return "Only {{first_name}} can be filled in. Check the braces in your text."
	}
	return ""
}

// AssetIDs lists the images a design shows.
func (d Design) AssetIDs() []string {
	ids := []string{}
	for _, block := range d.Blocks {
		if block.AssetID != "" {
			ids = append(ids, block.AssetID)
		}
	}
	return ids
}

// version changes only when something that shows in the email does.
func (d Design) version() string {
	d.ID, d.Name = "", ""
	encoded, _ := json.Marshal(d) //nolint:errchkjson // plain strings and an int
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:12]
}

// DesignVersion identifies how a design looks right now.
func DesignVersion(d Design) string { return d.version() }

// TemplateVersion identifies the design a message would be sent with.
func TemplateVersion(c Content) string {
	if IsCustomTemplate(c.EmailTemplate) && c.EmailDesign != nil {
		return c.EmailDesign.version()
	}
	return EmailTemplateVersion(c.EmailTemplate)
}

// designLook is a design worked out into what the template needs. The ink colors are
// chosen here, so no choice of accent can leave text unreadable.
type designLook struct {
	Accent, OnAccent, Tint, AccentInk string
	ButtonRadius, Corners             int
	Serif                             bool
}

func lookOf(d Design) designLook {
	radius := map[string]int{"square": 0, "rounded": 8, "pill": 999}[d.Button]
	onAccent := whiteInk
	if contrast(d.Accent, darkInk) > contrast(d.Accent, whiteInk) {
		onAccent = darkInk
	}
	accentInk := d.Accent
	if contrast(d.Accent, whiteInk) < readableOnWhite {
		accentInk = darkInk
	}
	return designLook{
		Accent: d.Accent, OnAccent: onAccent, AccentInk: accentInk, Tint: tint(d.Accent),
		ButtonRadius: radius, Corners: d.Corners, Serif: d.Font == "serif",
	}
}

// emailBlock is a block with its words and image address filled in.
type emailBlock struct {
	ID, Type, Align, Style string
	IsSlot                 bool
	Text                   string
	Paragraphs             [][]string
	Src, Alt, Link         string
	Width                  int
	Full, Placeholder      bool
	Label, URL             string
}

// emailBlocks works a design out for one person. Finished designs are all that can be
// sent; while editing, an image still to be chosen shows as an empty space.
func emailBlocks(c Content, firstName, assetBase string, editing bool) []emailBlock {
	blocks := make([]emailBlock, 0, len(c.EmailDesign.Blocks))
	for _, block := range c.EmailDesign.Blocks {
		out := emailBlock{ID: block.ID, Type: block.Type, Align: block.Align, Style: block.Style, IsSlot: block.Slot != ""}
		if out.Align == "" {
			out.Align = "left"
		}
		switch block.Type {
		case blockLogo, blockImage:
			if block.AssetID == "" && !editing {
				continue
			}
			out.Placeholder = block.AssetID == ""
			out.Full = block.Type == blockImage && block.Width == fullWidth
			column := contentWidth - sideMargins
			if out.Full {
				column = contentWidth
			}
			out.Width = column * block.Width / fullWidth
			out.Alt = block.Alt
			if !out.Placeholder {
				out.Src = assetBase + AssetPath(block.AssetID)
			}
			if validLink(block.Link) {
				out.Link = block.Link
			}
		case blockHeading:
			out.Text = Render(block.Text, firstName, c.DiscountCode)
			if block.Slot == SlotHeadline {
				out.Text = Render(c.EmailHeadline, firstName, c.DiscountCode)
			}
			if out.Style == "" {
				out.Style = "plain"
			}
			if out.Text == "" && editing && !out.IsSlot {
				out.Text = "Your heading"
			}
			if out.Text == "" {
				continue
			}
		case blockText:
			text := block.Text
			if block.Slot == SlotBody {
				text = c.EmailBody
			}
			out.Text = strings.TrimSpace(Render(text, firstName, c.DiscountCode))
			if out.Text == "" && editing && !out.IsSlot {
				out.Text = "Your words"
			}
			out.Paragraphs = paragraphs(out.Text)
		case blockButton:
			out.Label, out.URL = Render(block.Label, firstName, c.DiscountCode), block.URL
			if block.Slot == SlotCTA {
				out.Label, out.URL = Render(c.EmailCTALabel, firstName, c.DiscountCode), c.EmailCTAURL
			}
			if !validLink(out.URL) {
				if !editing || out.IsSlot {
					continue
				}
				out.URL = "#"
			}
			if out.Label == "" && editing {
				out.Label = "Your button"
			}
		}
		blocks = append(blocks, out)
		if block.Slot == SlotBody && c.DiscountCode != "" {
			blocks = append(blocks, emailBlock{ID: block.ID + "-code", Type: blockCode, Align: out.Align})
		}
	}
	return blocks
}

func channels(color string) [hexColorComponents]float64 {
	var parts [hexColorComponents]float64
	for i := range parts {
		value, _ := strconv.ParseUint(color[1+i*2:3+i*2], 16, 8) //nolint:errcheck // checked by hexColor
		parts[i] = float64(value)
	}
	return parts
}

// contrast is the WCAG contrast ratio of two colors.
func contrast(a, b string) float64 {
	first, second := luminance(a), luminance(b)
	return (max(first, second) + 0.05) / (min(first, second) + 0.05)
}

func luminance(color string) float64 {
	weights := [hexColorComponents]float64{0.2126, 0.7152, 0.0722}
	total := 0.0
	for i, value := range channels(color) {
		share := value / 255
		if share <= 0.03928 {
			share /= 12.92
		} else {
			share = math.Pow((share+0.055)/1.055, 2.4)
		}
		total += weights[i] * share
	}
	return total
}

// tint is the accent mixed into white, for a soft box behind a headline.
func tint(color string) string {
	var out strings.Builder
	out.WriteByte('#')
	for _, value := range channels(color) {
		mixed := int(math.Round(255 - (255-value)*tintStrength))
		out.WriteString(strings.ToUpper(strconv.FormatInt(int64(mixed)+256, 16)[1:]))
	}
	return out.String()
}
