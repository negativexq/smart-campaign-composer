package content_test

import (
	"slices"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/content"
)

// A brief may write its percent as a word. Only "%" counted before, so a model that
// wrote "%15" for "15 yüzde" was told it had made the discount up.
func TestAPercentWrittenAsAWordIsAnOffer(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]int{
		"indirim kodu 15 yüzde":         15,
		"Yüzde 20 indirim":              20,
		"Send a 20 percent discount":    20,
		"25 per cent off":               25,
		"15 Prozent Rabatt":             15,
		"un 10 por ciento de descuento": 10,
		"30 pour cent de réduction":     30,
		"%15 indirim":                   15,
		"15% off":                       15,
	} {
		if got := content.Discounts(text); !slices.Equal(got, []int{want}) {
			t.Errorf("%q: %v, want [%d]", text, got, want)
		}
	}
	if got := content.Discounts("Son 30 günde sepetine ürün ekleyen 3 kişi"); len(got) > 0 {
		t.Fatalf("plain numbers read as discounts: %v", got)
	}

	written := content.Content{SMS: "Ayşe, %15 indirim seni bekliyor!", EmailSubject: "%15 indirim", EmailBody: "Sepetindeki ürünlerde %15 indirim."}
	if issues := content.CheckDiscounts(written, "Antalya'da sepet hatırlatması, indirim kodu 15 yüzde."); len(issues) > 0 {
		t.Fatalf("%%15 for a brief that says 15 yüzde: %v", issues)
	}
	german := content.Content{SMS: "Anna, 15 Prozent Rabatt!", EmailSubject: "15 Prozent", EmailBody: "15 Prozent auf deinen Warenkorb."}
	if issues := content.CheckDiscounts(german, "Send a 15% discount."); len(issues) > 0 {
		t.Fatalf("a German message with the offer in words: %v", issues)
	}
	if content.OfferAgrees("Send a 20 percent discount", 30) {
		t.Fatal("a form offer of 30% agreed with a brief that says 20 percent")
	}
}
