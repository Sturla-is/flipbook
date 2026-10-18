package sales

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
)

func (s *Service) Report(ctx context.Context, filter ReportFilter) (*Report, error) {
	if filter.WorkspaceID == "" {
		filter.WorkspaceID = models.DefaultWorkspaceID
	}
	if filter.Limit == 0 {
		filter.Limit = 500
	}
	if filter.Limit < 1 || filter.Limit > 10000 || filter.Offset < 0 {
		return nil, invalid("invalid sales report pagination")
	}
	if !filter.From.IsZero() && !filter.To.IsZero() && !filter.From.Before(filter.To) {
		return nil, invalid("report start date must be before end date")
	}
	switch filter.Status {
	case "", models.PurchasePending, models.PurchasePaid, models.PurchaseFailed, models.PurchaseCancelled, models.PurchaseRefunded:
	default:
		return nil, invalid("invalid purchase status filter")
	}
	queryLimit := filter.Limit
	if queryLimit < 10000 {
		queryLimit++
	}
	purchases, err := s.store.ListPurchases(ctx, database.PurchaseFilter{
		WorkspaceID: filter.WorkspaceID, FlipbookID: filter.FlipbookID, Status: filter.Status,
		From: filter.From, To: filter.To, Limit: queryLimit, Offset: filter.Offset,
	})
	if err != nil {
		return nil, normalizeError(err)
	}
	report := &Report{Orders: make([]*Order, 0, len(purchases)), Totals: []CurrencyTotal{}, Customers: []Customer{}}
	if len(purchases) > int(filter.Limit) {
		report.Truncated = true
		purchases = purchases[:filter.Limit]
	} else if len(purchases) == int(filter.Limit) && filter.Limit == 10000 {
		report.Truncated = true
	}
	sort.SliceStable(purchases, func(i, j int) bool {
		return purchases[i].CreatedAt.After(purchases[j].CreatedAt)
	})
	totals := make(map[string]*CurrencyTotal)
	publicationTitles := make(map[string]string)
	type customerKey struct{ email, currency string }
	customers := make(map[customerKey]*Customer)
	customerPublications := make(map[customerKey]map[string]struct{})
	for _, purchase := range purchases {
		if purchase == nil {
			continue
		}
		title, cached := publicationTitles[purchase.FlipbookID]
		if !cached {
			title = purchase.FlipbookID
			fb, err := s.store.GetFlipbookInWorkspace(ctx, purchase.WorkspaceID, purchase.FlipbookID)
			if err == nil && fb.Title != "" {
				title = fb.Title
			}
			publicationTitles[purchase.FlipbookID] = title
		}
		report.Orders = append(report.Orders, &Order{Purchase: purchase, PublicationTitle: title})
		if purchase.Status != models.PurchasePaid && purchase.Status != models.PurchaseRefunded {
			continue
		}
		currency := strings.ToUpper(purchase.Currency)
		total := totals[currency]
		if total == nil {
			total = &CurrencyTotal{Currency: currency}
			totals[currency] = total
		}
		total.Orders++
		total.GrossCents += purchase.AmountCents
		if purchase.Status == models.PurchaseRefunded {
			total.RefundedCents += purchase.AmountCents
		}
		email := strings.ToLower(strings.TrimSpace(purchase.Email))
		if email == "" {
			email = "unknown"
		}
		key := customerKey{email: email, currency: currency}
		customer := customers[key]
		if customer == nil {
			customer = &Customer{Email: email, Currency: currency}
			customers[key] = customer
			customerPublications[key] = make(map[string]struct{})
		}
		customer.Orders++
		customer.GrossCents += purchase.AmountCents
		if purchase.Status == models.PurchaseRefunded {
			customer.RefundedCents += purchase.AmountCents
		}
		customerPublications[key][purchase.FlipbookID] = struct{}{}
		purchasedAt := purchase.CreatedAt
		if purchase.PurchasedAt != nil {
			purchasedAt = *purchase.PurchasedAt
		}
		if customer.FirstPurchasedAt.IsZero() || purchasedAt.Before(customer.FirstPurchasedAt) {
			customer.FirstPurchasedAt = purchasedAt
		}
		if customer.LastPurchasedAt.IsZero() || purchasedAt.After(customer.LastPurchasedAt) {
			customer.LastPurchasedAt = purchasedAt
		}
	}
	for _, total := range totals {
		total.NetCents = total.GrossCents - total.RefundedCents
		report.Totals = append(report.Totals, *total)
	}
	sort.Slice(report.Totals, func(i, j int) bool { return report.Totals[i].Currency < report.Totals[j].Currency })
	for key, customer := range customers {
		customer.NetCents = customer.GrossCents - customer.RefundedCents
		customer.PublicationCount = len(customerPublications[key])
		report.Customers = append(report.Customers, *customer)
	}
	sort.Slice(report.Customers, func(i, j int) bool {
		if report.Customers[i].LastPurchasedAt.Equal(report.Customers[j].LastPurchasedAt) {
			if report.Customers[i].Email == report.Customers[j].Email {
				return report.Customers[i].Currency < report.Customers[j].Currency
			}
			return report.Customers[i].Email < report.Customers[j].Email
		}
		return report.Customers[i].LastPurchasedAt.After(report.Customers[j].LastPurchasedAt)
	})
	report.TotalCount = len(report.Orders)
	return report, nil
}

func DateRange(fromValue, toValue string) (time.Time, time.Time, error) {
	var from, to time.Time
	var err error
	if fromValue != "" {
		from, err = time.Parse("2006-01-02", fromValue)
		if err != nil {
			return from, to, invalid("invalid report start date")
		}
	}
	if toValue != "" {
		to, err = time.Parse("2006-01-02", toValue)
		if err != nil {
			return from, to, invalid("invalid report end date")
		}
		to = to.Add(24 * time.Hour)
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return from, to, invalid("report start date must be before end date")
	}
	return from.UTC(), to.UTC(), nil
}
