package analytics

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/metavert-io/flipbook/internal/models"
)

var reportCSVHeader = []string{
	"section", "key", "date", "page", "views", "page_views", "qualified_reads",
	"sessions", "unique_readers", "active_seconds", "completions", "completion_rate",
	"events", "drop_off", "drop_off_rate", "details",
}

func WriteReportCSV(w io.Writer, report Report) error {
	writer := csv.NewWriter(w)
	if err := writer.Write(reportCSVHeader); err != nil {
		return err
	}
	write := func(row []string) error {
		for index := range row {
			if index == 1 || index == len(row)-1 {
				row[index] = spreadsheetSafe(row[index])
			}
		}
		return writer.Write(row)
	}
	blank := make([]string, len(reportCSVHeader))
	row := func(section, key string) []string {
		result := append([]string(nil), blank...)
		result[0], result[1] = section, key
		return result
	}
	summary := row("summary", "selected scope")
	setTotals(summary, report.Totals)
	if err := write(summary); err != nil {
		return err
	}
	for _, publication := range report.Publications {
		line := row("publication", publication.Publication.Title)
		setTotals(line, publication.Totals)
		line[15] = "id=" + publication.Publication.ID + ";" + line[15]
		if err := write(line); err != nil {
			return err
		}
	}
	for _, trend := range report.Trends {
		line := row("trend", trend.Date)
		line[2] = trend.Date
		line[4] = number(trend.Views)
		line[6] = number(trend.QualifiedReads)
		line[7] = number(trend.Sessions)
		line[9] = number(trend.ActiveSeconds)
		line[10] = number(trend.Completions)
		if err := write(line); err != nil {
			return err
		}
	}
	for _, page := range report.Pages {
		line := row("page", strconv.Itoa(page.Page))
		line[3] = strconv.Itoa(page.Page)
		line[5] = number(page.PageViews)
		line[7] = number(page.Sessions)
		line[9] = number(page.ActiveSeconds)
		line[10] = number(page.Completions)
		line[13] = number(page.DropOff)
		line[14] = decimal(page.DropOffRate)
		line[15] = "retention_rate=" + decimal(page.RetentionRate) + ";furthest=" + number(page.Furthest)
		if err := write(line); err != nil {
			return err
		}
	}
	for _, dimension := range breakdownDimensions {
		for _, bucket := range report.Breakdowns[dimension] {
			line := row(dimension, bucket.Key)
			line[4] = number(bucket.Views)
			line[6] = number(bucket.QualifiedReads)
			line[7] = number(bucket.Sessions)
			line[9] = number(bucket.ActiveSeconds)
			line[12] = number(bucket.Events)
			if err := write(line); err != nil {
				return err
			}
		}
	}
	if err := writeFunnelCSV(write, row, "funnel", report.Funnel); err != nil {
		return err
	}
	for _, funnel := range report.Funnels {
		if err := writeFunnelCSV(write, row, funnel.Key, funnel.Steps); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

func writeFunnelCSV(write func([]string) error, row func(string, string) []string, key string, steps []FunnelStep) error {
	for _, step := range steps {
		line := row("funnel", key)
		line[12] = number(step.Events)
		line[7] = number(step.Sessions)
		line[15] = step.Name
		if err := write(line); err != nil {
			return err
		}
	}
	return nil
}

func setTotals(row []string, totals Totals) {
	row[4] = number(totals.Views)
	row[5] = number(totals.PageViews)
	row[6] = number(totals.QualifiedReads)
	row[7] = number(totals.Sessions)
	row[8] = number(totals.UniqueReaders)
	row[9] = number(totals.ActiveSeconds)
	row[10] = number(totals.Completions)
	row[11] = decimal(totals.CompletionRate)
	row[15] = strings.Join([]string{
		"cta_clicks=" + number(totals.CTAClicks), "outbound_clicks=" + number(totals.OutboundClicks),
		"downloads=" + number(totals.Downloads), "searches=" + number(totals.Searches),
		"shares=" + number(totals.Shares), "leads=" + number(totals.Leads),
		"purchases=" + number(totals.Purchases), "legacy_views_all_time=" + number(totals.LegacyViews),
	}, ";")
}

func WriteEventsCSV(w io.Writer, events []*models.AnalyticsEvent) error {
	writer := csv.NewWriter(w)
	header := []string{
		"id", "occurred_at", "workspace_id", "flipbook_id", "revision_id", "type", "page",
		"active_seconds", "value", "referrer", "utm_source", "utm_medium", "utm_campaign",
		"utm_content", "utm_term", "placement", "device", "browser", "country", "search_terms",
		"target_host", "metadata",
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	for _, event := range SanitizeEvents(events) {
		metadata, err := json.Marshal(event.Metadata)
		if err != nil {
			return err
		}
		row := []string{
			event.ID, event.OccurredAt.Format(timeFormat), event.WorkspaceID, event.FlipbookID,
			event.RevisionID, string(event.Type), strconv.Itoa(event.Page), number(event.ActiveSeconds),
			number(event.Value), event.Referrer, event.UTM.Source, event.UTM.Medium, event.UTM.Campaign,
			event.UTM.Content, event.UTM.Term, event.Placement, event.Device, event.Browser,
			event.Country, event.SearchTerms, event.TargetHost, string(metadata),
		}
		for index, value := range row {
			row[index] = spreadsheetSafe(value)
		}
		if err := writer.Write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	return writer.Error()
}

const timeFormat = "2006-01-02T15:04:05.000Z07:00"

func number(value int64) string    { return strconv.FormatInt(value, 10) }
func decimal(value float64) string { return strconv.FormatFloat(value, 'f', 4, 64) }

func spreadsheetSafe(value string) string {
	if value == "" {
		return ""
	}
	first := value[0]
	if first == '=' || first == '+' || first == '-' || first == '@' || first == '\t' || first == '\r' {
		return "'" + value
	}
	return value
}

func ExportFilename(base, extension string, report Report) string {
	from, to := report.Range.QueryDates()
	name := fmt.Sprintf("%s-%s-%s.%s", base, from, to, extension)
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '-'
	}, name)
}
