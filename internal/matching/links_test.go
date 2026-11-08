package matching_test

import (
	"strings"
	"testing"

	"github.com/negativexq/smart-campaign-composer/internal/matching"
)

func TestWithoutLinksDropsAddressesAndKeepsTheRest(t *testing.T) {
	t.Parallel()
	for brief, want := range map[string]string{
		"Link: https://shop.example.com/sepet":                   "",
		"Tıkla: http://shop.example.com/cart?ref=sms.":           "Tıkla:",
		"Kampanya linki shop.example.com/kis, İzmir":             "Kampanya İzmir",
		"Bu link için teşekkürler, İzmir müşterileri":            "Bu link için teşekkürler, İzmir müşterileri",
		"www.shop.example.com/izmir adresinde":                   "adresinde",
		"shop.example.com/son-7-gun sayfasında":                  "sayfasında",
		"İzmir'de sepete ekleyenler (https://a.example/x)":       "İzmir'de sepete ekleyenler (",
		"Son 7 günde 3.5/5 puan veren İzmir müşterileri":         "Son 7 günde 3.5/5 puan veren İzmir müşterileri",
		"1.11/30 arası alışveriş yapanlar":                       "1.11/30 arası alışveriş yapanlar",
		"Ankara ve/veya İzmir, e-posta ile":                      "Ankara ve/veya İzmir, e-posta ile",
		"example.com adresli sitemizde son 30 gün sepet":         "example.com adresli sitemizde son 30 gün sepet",
		"%20 indirim, kod SEPET20, 14 gün içinde sepete ekleyen": "%20 indirim, kod SEPET20, 14 gün içinde sepete ekleyen",
	} {
		if got := strings.Join(strings.Fields(matching.WithoutLinks(brief)), " "); got != want {
			t.Errorf("%q: got %q, want %q", brief, got, want)
		}
	}
}
