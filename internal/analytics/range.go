package analytics

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

const maxRangeDuration = 366 * 24 * time.Hour

// ParseRange accepts YYYY-MM-DD dates (to is inclusive) or RFC3339 timestamps
// (to is exclusive). The default is the last 30 UTC calendar days.
func ParseRange(r *http.Request, now time.Time) (Range, error) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	fromValue := strings.TrimSpace(r.URL.Query().Get("from"))
	toValue := strings.TrimSpace(r.URL.Query().Get("to"))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	result := Range{From: today.AddDate(0, 0, -29), To: today.AddDate(0, 0, 1)}
	var err error
	if fromValue != "" {
		result.From, _, err = parseBoundary(fromValue, false)
		if err != nil {
			return Range{}, fmt.Errorf("invalid from date: %w", err)
		}
	}
	if toValue != "" {
		result.To, _, err = parseBoundary(toValue, fromValue == "" || isDateOnly(toValue))
		if err != nil {
			return Range{}, fmt.Errorf("invalid to date: %w", err)
		}
	}
	if !result.From.Before(result.To) {
		return Range{}, fmt.Errorf("from must be before to")
	}
	if result.To.Sub(result.From) > maxRangeDuration {
		return Range{}, ErrRangeTooLarge
	}
	return result, nil
}

func parseBoundary(value string, inclusiveDate bool) (time.Time, bool, error) {
	if isDateOnly(value) {
		parsed, err := time.ParseInLocation("2006-01-02", value, time.UTC)
		if inclusiveDate {
			parsed = parsed.AddDate(0, 0, 1)
		}
		return parsed, true, err
	}
	parsed, err := time.Parse(time.RFC3339, value)
	return parsed.UTC(), false, err
}

func isDateOnly(value string) bool {
	return len(value) == len("2006-01-02") && strings.Count(value, "-") == 2
}

func (r Range) QueryDates() (from, to string) {
	return r.From.UTC().Format("2006-01-02"), r.To.UTC().Add(-time.Nanosecond).Format("2006-01-02")
}
