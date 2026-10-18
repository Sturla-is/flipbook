package analytics

import (
	"sort"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
)

var breakdownDimensions = []string{
	"referrers", "devices", "browsers", "countries", "utm_sources",
	"utm_mediums", "utm_campaigns", "utm_contents", "utm_terms", "placements",
	"ctas", "link_hosts", "download_formats", "search_terms", "share_channels",
}

type ReportInput struct {
	WorkspaceID  string
	FlipbookID   string
	Range        Range
	Publications []Publication
	Events       []*models.AnalyticsEvent
	LegacyViews  map[string]int64
	Now          time.Time
}

type summaryAccumulator struct {
	seen     map[string]struct{}
	sessions map[string]struct{}
	readers  map[string]struct{}
	totals   Totals
}

func newSummaryAccumulator() *summaryAccumulator {
	return &summaryAccumulator{
		seen: make(map[string]struct{}), sessions: make(map[string]struct{}), readers: make(map[string]struct{}),
	}
}

func (a *summaryAccumulator) add(event *models.AnalyticsEvent) bool {
	if event.SessionIDHash != "" {
		a.sessions[event.SessionIDHash] = struct{}{}
	}
	if event.ReaderIDHash != "" {
		a.readers[event.ReaderIDHash] = struct{}{}
	}
	countable := a.countable(event)
	switch event.Type {
	case models.AnalyticsView:
		if countable {
			a.totals.Views++
		}
	case models.AnalyticsPageExposure:
		a.totals.PageViews++
		a.totals.ActiveSeconds += event.ActiveSeconds
	case models.AnalyticsQualifiedRead:
		if countable {
			a.totals.QualifiedReads++
		}
	case models.AnalyticsActiveTime:
		a.totals.ActiveSeconds += event.ActiveSeconds
	case models.AnalyticsCompletion:
		if countable {
			a.totals.Completions++
		}
	case models.AnalyticsCTAClick:
		a.totals.CTAClicks++
	case models.AnalyticsOutboundClick:
		a.totals.OutboundClicks++
	case models.AnalyticsDownload:
		a.totals.Downloads++
	case models.AnalyticsSearch:
		a.totals.Searches++
	case models.AnalyticsShare:
		a.totals.Shares++
	case models.AnalyticsLead:
		a.totals.Leads++
	case models.AnalyticsPurchase:
		a.totals.Purchases++
	}
	return countable
}

func (a *summaryAccumulator) countable(event *models.AnalyticsEvent) bool {
	switch event.Type {
	case models.AnalyticsView, models.AnalyticsQualifiedRead, models.AnalyticsCompletion:
	default:
		return true
	}
	identity := event.ID
	if event.SessionIDHash != "" {
		identity = event.SessionIDHash
	}
	key := event.FlipbookID + "\x00" + string(event.Type) + "\x00" + identity
	if _, exists := a.seen[key]; exists {
		return false
	}
	a.seen[key] = struct{}{}
	return true
}

func (a *summaryAccumulator) finish() Totals {
	a.totals.Sessions = int64(len(a.sessions))
	a.totals.UniqueReaders = int64(len(a.readers))
	if a.totals.QualifiedReads > 0 {
		a.totals.CompletionRate = float64(a.totals.Completions) / float64(a.totals.QualifiedReads)
	}
	denominator := a.totals.Sessions
	if denominator == 0 {
		denominator = a.totals.QualifiedReads
	}
	if denominator > 0 {
		a.totals.AverageActiveSeconds = float64(a.totals.ActiveSeconds) / float64(denominator)
	}
	return a.totals
}

func summarizeEvents(events []*models.AnalyticsEvent) Totals {
	accumulator := newSummaryAccumulator()
	for _, event := range events {
		accumulator.add(event)
	}
	return accumulator.finish()
}

type trendAccumulator struct {
	bucket   TrendBucket
	sessions map[string]struct{}
}

type pageAccumulator struct {
	metrics  PageMetrics
	sessions map[string]struct{}
}

type breakdownAccumulator struct {
	metrics  Breakdown
	sessions map[string]struct{}
}

type funnelAccumulator struct {
	steps    map[string]*FunnelStep
	sessions map[string]map[string]struct{}
}

func newFunnelAccumulator() *funnelAccumulator {
	funnel := &funnelAccumulator{
		steps: make(map[string]*FunnelStep), sessions: make(map[string]map[string]struct{}),
	}
	for _, name := range funnelStepNames {
		funnel.steps[name] = &FunnelStep{Name: name}
		funnel.sessions[name] = make(map[string]struct{})
	}
	return funnel
}

var funnelStepNames = []string{"view", "qualified_read", "completion", "cta_click", "download", "lead", "purchase"}

var funnelEventTypes = map[string]models.AnalyticsEventType{
	"view": models.AnalyticsView, "qualified_read": models.AnalyticsQualifiedRead,
	"completion": models.AnalyticsCompletion, "cta_click": models.AnalyticsCTAClick,
	"download": models.AnalyticsDownload, "lead": models.AnalyticsLead, "purchase": models.AnalyticsPurchase,
}

type reportAggregator struct {
	main       *summaryAccumulator
	trends     map[string]*trendAccumulator
	pages      map[int]*pageAccumulator
	breakdowns map[string]map[string]*breakdownAccumulator
	funnel     *funnelAccumulator
	funnels    map[string]*funnelAccumulator
}

func newReportAggregator() *reportAggregator {
	result := &reportAggregator{
		main: newSummaryAccumulator(), trends: make(map[string]*trendAccumulator),
		pages: make(map[int]*pageAccumulator), breakdowns: make(map[string]map[string]*breakdownAccumulator),
		funnel: newFunnelAccumulator(), funnels: make(map[string]*funnelAccumulator),
	}
	for _, dimension := range breakdownDimensions {
		result.breakdowns[dimension] = make(map[string]*breakdownAccumulator)
	}
	return result
}

func BuildReport(input ReportInput) Report {
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	aggregator := newReportAggregator()
	eventsByPublication := make(map[string][]*models.AnalyticsEvent)
	for _, event := range input.Events {
		if event == nil {
			continue
		}
		eventsByPublication[event.FlipbookID] = append(eventsByPublication[event.FlipbookID], event)
		aggregator.addEvent(event, input.Range)
	}
	report := Report{
		GeneratedAt: now, Range: input.Range,
		Scope:  Scope{WorkspaceID: input.WorkspaceID, FlipbookID: input.FlipbookID},
		Totals: aggregator.main.finish(), Trends: aggregator.finishTrends(input.Range),
		Pages: aggregator.finishPages(input.Publications), Breakdowns: aggregator.finishBreakdowns(),
		Funnel: aggregator.funnel.finish(), Funnels: aggregator.finishFunnels(),
		Publications: make([]PublicationMetrics, 0, len(input.Publications)),
	}
	for _, publication := range input.Publications {
		legacy := input.LegacyViews[publication.ID]
		totals := summarizeEvents(eventsByPublication[publication.ID])
		totals.LegacyViews = legacy
		report.Publications = append(report.Publications, PublicationMetrics{Publication: publication, Totals: totals})
		report.Totals.LegacyViews += legacy
	}
	sort.SliceStable(report.Publications, func(i, j int) bool {
		left, right := report.Publications[i], report.Publications[j]
		if left.Totals.Views != right.Totals.Views {
			return left.Totals.Views > right.Totals.Views
		}
		if left.Totals.QualifiedReads != right.Totals.QualifiedReads {
			return left.Totals.QualifiedReads > right.Totals.QualifiedReads
		}
		return left.Publication.Title < right.Publication.Title
	})
	return report
}

func (a *reportAggregator) addEvent(event *models.AnalyticsEvent, reportRange Range) {
	countable := a.main.add(event)
	a.addTrend(event, reportRange, countable)
	a.addPage(event, countable)
	a.addBreakdowns(event, countable)
	a.funnel.add(event, countable)
	if event.UTM.Campaign != "" {
		key := "campaign: " + event.UTM.Campaign
		a.funnelFor(key).add(event, countable)
	}
	placement := event.Placement
	if placement == "" {
		placement = PlacementStandalone
	}
	a.funnelFor("placement: "+placement).add(event, countable)
}

func (a *reportAggregator) addTrend(event *models.AnalyticsEvent, reportRange Range, countable bool) {
	day := event.OccurredAt.UTC().Format("2006-01-02")
	if event.OccurredAt.Before(reportRange.From) || !event.OccurredAt.Before(reportRange.To) {
		return
	}
	bucket := a.trends[day]
	if bucket == nil {
		bucket = &trendAccumulator{bucket: TrendBucket{Date: day}, sessions: make(map[string]struct{})}
		a.trends[day] = bucket
	}
	if event.SessionIDHash != "" {
		bucket.sessions[event.SessionIDHash] = struct{}{}
	}
	switch event.Type {
	case models.AnalyticsView:
		if countable {
			bucket.bucket.Views++
		}
	case models.AnalyticsQualifiedRead:
		if countable {
			bucket.bucket.QualifiedReads++
		}
	case models.AnalyticsPageExposure, models.AnalyticsActiveTime:
		bucket.bucket.ActiveSeconds += event.ActiveSeconds
	case models.AnalyticsCompletion:
		if countable {
			bucket.bucket.Completions++
		}
	}
}

func (a *reportAggregator) addPage(event *models.AnalyticsEvent, countable bool) {
	if event.Page < 1 {
		return
	}
	page := a.pages[event.Page]
	if page == nil {
		page = &pageAccumulator{metrics: PageMetrics{Page: event.Page}, sessions: make(map[string]struct{})}
		a.pages[event.Page] = page
	}
	if event.SessionIDHash != "" {
		page.sessions[event.SessionIDHash] = struct{}{}
	}
	switch event.Type {
	case models.AnalyticsPageExposure:
		page.metrics.PageViews++
		page.metrics.ActiveSeconds += event.ActiveSeconds
	case models.AnalyticsActiveTime:
		page.metrics.ActiveSeconds += event.ActiveSeconds
	case models.AnalyticsFurthestPage:
		page.metrics.Furthest++
	case models.AnalyticsCompletion:
		if countable {
			page.metrics.Completions++
		}
	}
}

func (a *reportAggregator) addBreakdowns(event *models.AnalyticsEvent, countable bool) {
	for _, dimension := range breakdownDimensions {
		key, ok := breakdownKey(dimension, event)
		if !ok {
			continue
		}
		bucket := a.breakdowns[dimension][key]
		if bucket == nil {
			bucket = &breakdownAccumulator{metrics: Breakdown{Key: key}, sessions: make(map[string]struct{})}
			a.breakdowns[dimension][key] = bucket
		}
		bucket.metrics.Events++
		if event.SessionIDHash != "" {
			bucket.sessions[event.SessionIDHash] = struct{}{}
		}
		if event.Type == models.AnalyticsView && countable {
			bucket.metrics.Views++
		}
		if event.Type == models.AnalyticsQualifiedRead && countable {
			bucket.metrics.QualifiedReads++
		}
		if event.Type == models.AnalyticsPageExposure || event.Type == models.AnalyticsActiveTime {
			bucket.metrics.ActiveSeconds += event.ActiveSeconds
		}
	}
}

func breakdownKey(dimension string, event *models.AnalyticsEvent) (string, bool) {
	value := ""
	switch dimension {
	case "referrers":
		value = nonempty(event.Referrer, "(direct)")
	case "devices":
		value = nonempty(event.Device, "(unknown)")
	case "browsers":
		value = nonempty(event.Browser, "(unknown)")
	case "countries":
		value = nonempty(event.Country, "(unknown)")
	case "utm_sources":
		value = nonempty(event.UTM.Source, "(direct)")
	case "utm_mediums":
		value = nonempty(event.UTM.Medium, "(none)")
	case "utm_campaigns":
		value = nonempty(event.UTM.Campaign, "(none)")
	case "utm_contents":
		value = nonempty(event.UTM.Content, "(none)")
	case "utm_terms":
		value = nonempty(event.UTM.Term, "(none)")
	case "placements":
		value = nonempty(event.Placement, PlacementStandalone)
	case "ctas":
		if event.Type != models.AnalyticsCTAClick {
			return "", false
		}
		value = nonempty(event.Metadata["cta_id"], "(default)")
	case "link_hosts":
		if event.Type != models.AnalyticsCTAClick && event.Type != models.AnalyticsOutboundClick {
			return "", false
		}
		value = nonempty(event.TargetHost, "(internal)")
	case "download_formats":
		if event.Type != models.AnalyticsDownload {
			return "", false
		}
		value = nonempty(event.Metadata["format"], "(unknown)")
	case "search_terms":
		if event.Type != models.AnalyticsSearch || event.SearchTerms == "" {
			return "", false
		}
		value = event.SearchTerms
	case "share_channels":
		if event.Type != models.AnalyticsShare {
			return "", false
		}
		value = nonempty(event.Metadata["channel"], "(unknown)")
	}
	return value, value != ""
}

func nonempty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (a *reportAggregator) finishTrends(reportRange Range) []TrendBucket {
	result := make([]TrendBucket, 0)
	for day := reportRange.From.UTC(); day.Before(reportRange.To.UTC()); day = day.AddDate(0, 0, 1) {
		key := day.Format("2006-01-02")
		bucket := TrendBucket{Date: key}
		if existing := a.trends[key]; existing != nil {
			bucket = existing.bucket
			bucket.Sessions = int64(len(existing.sessions))
		}
		result = append(result, bucket)
	}
	return result
}

func (a *reportAggregator) finishPages(publications []Publication) []PageMetrics {
	maxPages := 0
	if len(publications) == 1 {
		maxPages = publications[0].PageCount
		if maxPages > 2000 {
			maxPages = 2000
		}
	}
	for page := range a.pages {
		if page > maxPages {
			maxPages = page
		}
	}
	result := make([]PageMetrics, 0, maxPages)
	for number := 1; number <= maxPages; number++ {
		page := a.pages[number]
		if page == nil {
			if len(publications) != 1 {
				continue
			}
			result = append(result, PageMetrics{Page: number})
			continue
		}
		page.metrics.Sessions = int64(len(page.sessions))
		result = append(result, page.metrics)
	}
	for index := range result {
		currentBase := result[index].Sessions
		if currentBase == 0 {
			currentBase = result[index].PageViews
		}
		if index == 0 {
			if currentBase > 0 {
				result[index].RetentionRate = 1
			}
			continue
		}
		previousBase := result[index-1].Sessions
		if previousBase == 0 {
			previousBase = result[index-1].PageViews
		}
		firstBase := result[0].Sessions
		if firstBase == 0 {
			firstBase = result[0].PageViews
		}
		if firstBase > 0 {
			result[index].RetentionRate = float64(currentBase) / float64(firstBase)
		}
		if previousBase > currentBase {
			result[index].DropOff = previousBase - currentBase
		}
		if previousBase > 0 {
			result[index].DropOffRate = float64(result[index].DropOff) / float64(previousBase)
		}
	}
	return result
}

func (a *reportAggregator) finishBreakdowns() map[string][]Breakdown {
	result := make(map[string][]Breakdown, len(breakdownDimensions))
	for _, dimension := range breakdownDimensions {
		buckets := make([]Breakdown, 0, len(a.breakdowns[dimension]))
		for _, bucket := range a.breakdowns[dimension] {
			bucket.metrics.Sessions = int64(len(bucket.sessions))
			buckets = append(buckets, bucket.metrics)
		}
		sort.SliceStable(buckets, func(i, j int) bool {
			if buckets[i].Views != buckets[j].Views {
				return buckets[i].Views > buckets[j].Views
			}
			if buckets[i].Sessions != buckets[j].Sessions {
				return buckets[i].Sessions > buckets[j].Sessions
			}
			if buckets[i].Events != buckets[j].Events {
				return buckets[i].Events > buckets[j].Events
			}
			return buckets[i].Key < buckets[j].Key
		})
		if len(buckets) > 50 {
			buckets = buckets[:50]
		}
		result[dimension] = buckets
	}
	return result
}

func (f *funnelAccumulator) add(event *models.AnalyticsEvent, countable bool) {
	for name, eventType := range funnelEventTypes {
		if event.Type != eventType {
			continue
		}
		if countable {
			f.steps[name].Events++
		}
		if event.SessionIDHash != "" {
			f.sessions[name][event.SessionIDHash] = struct{}{}
		}
	}
}

func (f *funnelAccumulator) finish() []FunnelStep {
	result := make([]FunnelStep, 0, len(funnelStepNames))
	for _, name := range funnelStepNames {
		step := *f.steps[name]
		step.Sessions = int64(len(f.sessions[name]))
		result = append(result, step)
	}
	return result
}

func (a *reportAggregator) funnelFor(key string) *funnelAccumulator {
	funnel := a.funnels[key]
	if funnel == nil {
		funnel = newFunnelAccumulator()
		a.funnels[key] = funnel
	}
	return funnel
}

func (a *reportAggregator) finishFunnels() []Funnel {
	result := make([]Funnel, 0, len(a.funnels))
	for key, funnel := range a.funnels {
		result = append(result, Funnel{Key: key, Steps: funnel.finish()})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Steps[0].Events != result[j].Steps[0].Events {
			return result[i].Steps[0].Events > result[j].Steps[0].Events
		}
		return result[i].Key < result[j].Key
	})
	if len(result) > 20 {
		result = result[:20]
	}
	return result
}
