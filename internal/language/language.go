// Package language lists the languages campaigns can be written in, with what
// surrounds a message in each. A language is added here and nowhere else in the backend.
package language

// Fallback is the language of anyone whose own has no version in a campaign.
const Fallback = "en"

// Language is one language we write in.
type Language struct {
	Code string
	Name string
	// MissingName stands in for a person with no first name.
	MissingName string
	// Words are how a brief names it, folded to plain lowercase letters.
	Words []string
	// Footer texts of an email.
	Received, Unsubscribe, LinkLater string
	// CodeLabel names the discount code in an email's code box.
	CodeLabel string
}

var all = []Language{
	{
		Code: "tr", Name: "Turkish", MissingName: "müşterimiz", Words: []string{"turkce", "turkish"},
		Received:    "Kampanya e-postalarına izin verdiğiniz için bu e-postayı aldınız.",
		Unsubscribe: "Abonelikten çık", LinkLater: "bağlantı gönderimde eklenir",
		CodeLabel: "İndirim kodun",
	},
	{
		Code: "en", Name: "English", MissingName: "there", Words: []string{"ingilizce", "english"},
		Received:    "You received this because you agreed to campaign emails.",
		Unsubscribe: "Unsubscribe", LinkLater: "the link is added when this is sent",
		CodeLabel: "Your discount code",
	},
	{
		Code: "de", Name: "German", MissingName: "Kundschaft", Words: []string{"almanca", "german", "deutsch"},
		Received:    "Sie erhalten diese E-Mail, weil Sie Kampagnen-E-Mails zugestimmt haben.",
		Unsubscribe: "Abmelden", LinkLater: "der Link wird beim Versand ergänzt",
		CodeLabel: "Ihr Rabattcode",
	},
	{
		Code: "fr", Name: "French", MissingName: "cher client", Words: []string{"fransizca", "french", "francais"},
		Received:    "Vous recevez cet e-mail car vous avez accepté les e-mails de campagne.",
		Unsubscribe: "Se désabonner", LinkLater: "le lien est ajouté à l'envoi",
		CodeLabel: "Votre code de réduction",
	},
	{
		Code: "es", Name: "Spanish", MissingName: "cliente", Words: []string{"ispanyolca", "spanish", "espanol"},
		Received:    "Recibes este correo porque aceptaste los correos de campañas.",
		Unsubscribe: "Darse de baja", LinkLater: "el enlace se añade al enviar",
		CodeLabel: "Tu código de descuento",
	},
}

// Get finds a language by its code.
func Get(code string) (Language, bool) {
	for _, language := range all {
		if language.Code == code {
			return language, true
		}
	}
	return Language{}, false
}

// Known says whether we can write in this language.
func Known(code string) bool {
	_, ok := Get(code)
	return ok
}

// Name names a language we can write in, or returns nothing.
func Name(code string) string {
	language, _ := Get(code)
	return language.Name
}

// Codes lists every language's code.
func Codes() []string {
	codes := make([]string, len(all))
	for i, language := range all {
		codes[i] = language.Code
	}
	return codes
}

// All lists every language.
func All() []Language {
	return append([]Language(nil), all...)
}

// Words lists how briefs can name any language.
func Words() []string {
	words := []string{}
	for _, language := range all {
		words = append(words, language.Words...)
	}
	return words
}
