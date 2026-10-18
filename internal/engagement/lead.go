package engagement

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonradoff/flipbook/internal/database"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/publishing"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const leadRetentionSettingPrefix = "engagement:lead-retention-days:"

type LeadInput struct {
	FlipbookID string
	Email      string
	Name       string
	Consent    bool
	Source     string
	UTM        models.UTMCampaign
	Metadata   map[string]string
	Access     publishing.AccessRequest
}

type LeadResult struct {
	Lead     *models.Lead
	Grant    *publishing.GrantResult
	Message  string
	Delivery []DeliveryResult
}

func NormalizeEmail(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 254 || !utf8.ValidString(value) || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("%w: a valid email address is required", ErrInvalidInput)
	}
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value || !strings.Contains(value, "@") {
		return "", fmt.Errorf("%w: a valid email address is required", ErrInvalidInput)
	}
	return strings.ToLower(value), nil
}

func cleanName(raw string) (string, error) {
	value := strings.Join(strings.Fields(raw), " ")
	if len(value) > 200 || strings.ContainsAny(value, "\r\n\x00") {
		return "", fmt.Errorf("%w: name must be at most 200 characters", ErrInvalidInput)
	}
	return value, nil
}

func cleanMetadata(values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > 20 {
		return nil, fmt.Errorf("%w: too many metadata fields", ErrInvalidInput)
	}
	clean := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || len(key) > 100 || len(value) > 500 || strings.ContainsAny(key+value, "\r\n\x00") {
			return nil, fmt.Errorf("%w: invalid metadata", ErrInvalidInput)
		}
		clean[key] = value
	}
	return clean, nil
}

func (s *Service) CaptureLead(ctx context.Context, input LeadInput) (*LeadResult, error) {
	email, err := NormalizeEmail(input.Email)
	if err != nil {
		return nil, err
	}
	if !input.Consent {
		return nil, ErrConsentRequired
	}
	name, err := cleanName(input.Name)
	if err != nil {
		return nil, err
	}
	metadata, err := cleanMetadata(input.Metadata)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(input.FlipbookID) == "" || s.publisher == nil {
		return nil, fmt.Errorf("%w: lead grants are unavailable", ErrNotConfigured)
	}
	fb, err := s.store.GetFlipbookContext(ctx, input.FlipbookID)
	if err != nil {
		return nil, translateNotFound(err)
	}
	grant, err := s.publisher.IssueLeadGrant(ctx, fb.WorkspaceID, fb.ID, true, false, input.Access)
	if err != nil {
		return nil, err
	}
	retentionDays, err := s.LeadRetentionDays(ctx, fb.WorkspaceID)
	if err != nil {
		return nil, err
	}
	now := s.timeNow()
	source := strings.TrimSpace(input.Source)
	if source == "" {
		source = "lead_gate"
	}
	if len(source) > 100 {
		return nil, fmt.Errorf("%w: source is too long", ErrInvalidInput)
	}
	lead := &models.Lead{
		WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, Email: email, Name: name,
		Consented: true, ConsentText: fb.LeadGate.ConsentText, ConsentedAt: &now,
		Source: source, UTM: input.UTM, Metadata: metadata, CreatedAt: now,
	}
	if retentionDays > 0 {
		expires := now.Add(time.Duration(retentionDays) * 24 * time.Hour)
		lead.RetentionExpiresAt = &expires
	}
	if err := s.store.UpsertLead(ctx, lead); err != nil {
		return nil, err
	}
	message := fb.LeadGate.SuccessMessage
	if strings.TrimSpace(message) == "" {
		message = "Thank you. Your access is unlocked."
	}
	result := &LeadResult{Lead: lead, Grant: grant, Message: message}
	result.Delivery = s.crm.DeliverLead(ctx, lead)
	if err := s.emit(ctx, fb.WorkspaceID, EventLeadCaptured, map[string]any{
		"flipbook_id": fb.ID, "email": lead.Email, "name": lead.Name,
		"consent": lead.Consented, "consented_at": now, "source": lead.Source,
		"utm": lead.UTM, "metadata": lead.Metadata,
	}); err != nil {
		result.Delivery = append(result.Delivery, DeliveryResult{Provider: "webhook", Error: "webhook could not be queued"})
	}
	return result, nil
}

func (s *Service) DismissLeadGate(ctx context.Context, flipbookID string, access publishing.AccessRequest) (*publishing.GrantResult, error) {
	if s.publisher == nil {
		return nil, fmt.Errorf("%w: lead grants are unavailable", ErrNotConfigured)
	}
	fb, err := s.store.GetFlipbookContext(ctx, flipbookID)
	if err != nil {
		return nil, translateNotFound(err)
	}
	return s.publisher.IssueLeadGrant(ctx, fb.WorkspaceID, fb.ID, false, true, access)
}

func (s *Service) LeadGate(ctx context.Context, flipbookID string, access publishing.AccessRequest) (*publishing.Authorization, error) {
	if s.publisher == nil {
		return nil, fmt.Errorf("%w: lead gates are unavailable", ErrNotConfigured)
	}
	fb, err := s.store.GetFlipbookContext(ctx, flipbookID)
	if err != nil {
		return nil, translateNotFound(err)
	}
	if !fb.LeadGate.Enabled {
		return nil, fmt.Errorf("%w: lead capture is disabled", ErrInvalidInput)
	}
	return s.publisher.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, access)
}

func ValidateLeadGate(settings models.LeadGateSettings, pageCount int) error {
	if !settings.Enabled {
		return nil
	}
	if !settings.GateReading && !settings.GateDownloads {
		return fmt.Errorf("%w: select reading or download gating", ErrInvalidInput)
	}
	if !settings.Mandatory && !settings.Dismissible {
		return fmt.Errorf("%w: select mandatory or dismissible mode", ErrInvalidInput)
	}
	if settings.Mandatory && settings.Dismissible {
		return fmt.Errorf("%w: mandatory gates cannot be dismissible", ErrInvalidInput)
	}
	if settings.PreviewPages < 0 || (pageCount > 0 && settings.PreviewPages > pageCount) {
		return fmt.Errorf("%w: preview pages must be between zero and the publication page count", ErrInvalidInput)
	}
	if len(settings.ConsentText) > 5000 || len(settings.SuccessMessage) > 2000 {
		return fmt.Errorf("%w: gate messages are too long", ErrInvalidInput)
	}
	return nil
}

func (s *Service) UpdateLeadGate(ctx context.Context, workspaceID, flipbookID string, settings models.LeadGateSettings) (*models.Flipbook, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, translateNotFound(err)
	}
	if err := ValidateLeadGate(settings, fb.PageCount); err != nil {
		return nil, err
	}
	fb.LeadGate = settings
	if err := s.store.UpdateFlipbookMetadata(ctx, fb); err != nil {
		return nil, err
	}
	return fb, nil
}

func (s *Service) LeadRetentionDays(_ context.Context, workspaceID string) (int, error) {
	value, err := s.store.GetSetting(leadRetentionSettingPrefix + workspaceID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return s.leadRetentionDays, nil
	}
	if err != nil {
		return 0, err
	}
	days, err := strconv.Atoi(value)
	if err != nil || days < 0 || days > 3650 {
		return 0, errors.New("stored lead retention is invalid")
	}
	return days, nil
}

func (s *Service) SetLeadRetentionDays(ctx context.Context, workspaceID string, days int, applyExisting bool) error {
	if strings.TrimSpace(workspaceID) == "" || days < 0 || days > 3650 {
		return fmt.Errorf("%w: retention must be between zero and 3650 days", ErrInvalidInput)
	}
	if err := s.store.SetSetting(leadRetentionSettingPrefix+workspaceID, strconv.Itoa(days)); err != nil {
		return err
	}
	if !applyExisting {
		return nil
	}
	for offset := int64(0); ; offset += 1000 {
		leads, err := s.store.ListLeads(ctx, database.LeadFilter{WorkspaceID: workspaceID, Limit: 1000, Offset: offset})
		if err != nil {
			return err
		}
		for _, lead := range leads {
			if days == 0 {
				lead.RetentionExpiresAt = nil
			} else {
				expires := s.timeNow().Add(time.Duration(days) * 24 * time.Hour)
				lead.RetentionExpiresAt = &expires
			}
			if err := s.store.UpsertLead(ctx, lead); err != nil {
				return err
			}
		}
		if len(leads) < 1000 {
			return nil
		}
	}
}

func (s *Service) DeleteLead(ctx context.Context, workspaceID, leadID string) error {
	if strings.TrimSpace(leadID) == "" {
		return fmt.Errorf("%w: lead id is required", ErrInvalidInput)
	}
	return s.store.DeleteLead(ctx, workspaceID, leadID)
}

func (s *Service) DeleteLeadsForFlipbook(ctx context.Context, workspaceID, flipbookID string) error {
	if strings.TrimSpace(flipbookID) == "" {
		return fmt.Errorf("%w: flipbook id is required", ErrInvalidInput)
	}
	return s.store.DeleteLeadsForFlipbook(ctx, workspaceID, flipbookID)
}

func (s *Service) ExportLeadsCSV(ctx context.Context, output io.Writer, filter database.LeadFilter) error {
	writer := csv.NewWriter(output)
	if err := writer.Write([]string{"id", "flipbook_id", "email", "name", "consented", "consented_at", "source", "utm_source", "utm_medium", "utm_campaign", "created_at", "retention_expires_at"}); err != nil {
		return err
	}
	filter.Offset = 0
	filter.Limit = 1000
	for {
		leads, err := s.store.ListLeads(ctx, filter)
		if err != nil {
			return err
		}
		for _, lead := range leads {
			consentedAt, retention := "", ""
			if lead.ConsentedAt != nil {
				consentedAt = lead.ConsentedAt.UTC().Format(time.RFC3339)
			}
			if lead.RetentionExpiresAt != nil {
				retention = lead.RetentionExpiresAt.UTC().Format(time.RFC3339)
			}
			record := []string{
				lead.ID, lead.FlipbookID, lead.Email, lead.Name, strconv.FormatBool(lead.Consented),
				consentedAt, lead.Source, lead.UTM.Source, lead.UTM.Medium, lead.UTM.Campaign,
				lead.CreatedAt.UTC().Format(time.RFC3339), retention,
			}
			for index := range record {
				record[index] = safeCSVCell(record[index])
			}
			if err := writer.Write(record); err != nil {
				return err
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			return err
		}
		if len(leads) < 1000 {
			return nil
		}
		filter.Offset += 1000
	}
}

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t")
	if strings.HasPrefix(trimmed, "=") || strings.HasPrefix(trimmed, "+") ||
		strings.HasPrefix(trimmed, "-") || strings.HasPrefix(trimmed, "@") {
		return "'" + value
	}
	return value
}

func translateNotFound(err error) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return ErrNotFound
	}
	return err
}
