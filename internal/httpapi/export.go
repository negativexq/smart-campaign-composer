package httpapi

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/negativexq/smart-campaign-composer/internal/campaign"
)

// Contact details stay out: a report for reading elsewhere has no need for them.
var csvHeader = []string{
	"user_id", "first_name", "channel", "version", "status", "reason", "attempts", "last_response_code",
	"accepted_at", "delivered_at_simulated", "bounced_at_simulated", "opened_at_test_inbox",
}

func (a *api) deliveriesCSV(w http.ResponseWriter, r *http.Request) {
	filter, err := deliveryFilter(r.URL.Query())
	if err != nil {
		writeError(w, r, err)
		return
	}
	id := r.PathValue("id")
	writer := csv.NewWriter(w)
	started := false
	err = a.campaigns.ExportDeliveries(r.Context(), id, filter, func(delivery campaign.Delivery) error {
		if !started {
			startCSV(w, writer, id)
			started = true
		}
		return writer.Write(csvRow(delivery))
	})
	if err != nil {
		if !started {
			writeError(w, r, err)
			return
		}
		// Part of a file is already out. Breaking the connection says it is incomplete,
		// where ending normally would pass it off as the whole report.
		panic(http.ErrAbortHandler)
	}
	if !started {
		startCSV(w, writer, id)
	}
	writer.Flush()
}

func startCSV(w http.ResponseWriter, writer *csv.Writer, id string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="deliveries-`+id+`.csv"`)
	// Without this mark, Excel misreads Turkish letters.
	w.Write([]byte("\xef\xbb\xbf")) //nolint:errcheck // a lost client ends the next write too
	writer.Write(csvHeader)         //nolint:errcheck // buffered; reported by the next write
}

func csvRow(d campaign.Delivery) []string {
	status := d.Status
	// SENT means the provider accepted it, which is what the screens call it too.
	if status == "SENT" {
		status = "ACCEPTED"
	}
	code := ""
	if d.ResponseCode != nil {
		code = strconv.Itoa(*d.ResponseCode)
	}
	return []string{
		strconv.FormatInt(d.UserID, 10), safeCell(d.FirstName), d.Channel, d.Variant, status, d.Reason, strconv.Itoa(d.Attempts), code,
		csvTime(d.SentAt), csvTime(d.DeliveredAt), csvTime(d.BouncedAt), csvTime(d.OpenedAt),
	}
}

func csvTime(at *time.Time) string {
	if at == nil {
		return ""
	}
	return at.UTC().Format(time.RFC3339)
}

// safeCell keeps a spreadsheet from running customer text as a formula.
func safeCell(text string) string {
	if text != "" && strings.ContainsRune("=+-@\t\r", rune(text[0])) {
		return "'" + text
	}
	return text
}
