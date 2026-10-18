package analytics

import "fmt"

func FormatDuration(seconds int64) string {
	if seconds < 0 {
		seconds = 0
	}
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	if seconds < 3600 {
		return fmt.Sprintf("%dm %ds", seconds/60, seconds%60)
	}
	if seconds < 86400 {
		return fmt.Sprintf("%dh %dm", seconds/3600, seconds%3600/60)
	}
	return fmt.Sprintf("%dd %dh", seconds/86400, seconds%86400/3600)
}

func (t Totals) ActiveTime() string { return FormatDuration(t.ActiveSeconds) }
func (t Totals) AverageActiveTime() string {
	return FormatDuration(int64(t.AverageActiveSeconds + 0.5))
}
func (t Totals) CompletionPercent() float64   { return t.CompletionRate * 100 }
func (t TrendBucket) ActiveTime() string      { return FormatDuration(t.ActiveSeconds) }
func (p PageMetrics) ActiveTime() string      { return FormatDuration(p.ActiveSeconds) }
func (p PageMetrics) DropOffPercent() float64 { return p.DropOffRate * 100 }
func (p PageMetrics) RetentionPercent() float64 {
	return p.RetentionRate * 100
}
func (b Breakdown) ActiveTime() string { return FormatDuration(b.ActiveSeconds) }
