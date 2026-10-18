package engagement

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/metavert-io/flipbook/internal/config"
	"github.com/metavert-io/flipbook/internal/models"
)

const (
	ProviderHubSpot    = "hubspot"
	ProviderSalesforce = "salesforce"
	ProviderMailchimp  = "mailchimp"
)

type CRMStatus struct {
	Provider      string
	Enabled       bool
	Configured    bool
	Verified      bool
	LastError     string
	LastSuccessAt *time.Time
}

type providerState struct {
	enabled       bool
	configured    bool
	lastError     string
	lastSuccessAt *time.Time
}

type crmLeadConnector interface {
	SyncLead(ctx context.Context, lead *models.Lead) error
}

type crmSubscriberConnector interface {
	SyncSubscriber(ctx context.Context, subscriber *models.Subscriber) error
}

type CRMDispatcher struct {
	client     *http.Client
	connectors map[string]any
	order      []string
	mu         sync.RWMutex
	states     map[string]providerState
}

func NewCRMDispatcher(cfg config.CRMConfig, client *http.Client) *CRMDispatcher {
	if client == nil {
		client = &http.Client{
			Timeout: 15 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	dispatcher := &CRMDispatcher{
		client: client, connectors: make(map[string]any), order: []string{ProviderHubSpot, ProviderSalesforce, ProviderMailchimp},
		states: make(map[string]providerState),
	}
	hubspot, hubspotErr := newHubSpotConnector(cfg.HubSpot, client)
	salesforce, salesforceErr := newSalesforceConnector(cfg.Salesforce, client)
	mailchimp, mailchimpErr := newMailchimpConnector(cfg.Mailchimp, client)
	dispatcher.connectors[ProviderHubSpot] = hubspot
	dispatcher.connectors[ProviderSalesforce] = salesforce
	dispatcher.connectors[ProviderMailchimp] = mailchimp
	dispatcher.states[ProviderHubSpot] = initialProviderState(cfg.HubSpot.Enabled, cfg.HubSpotConfigured(), hubspotErr)
	dispatcher.states[ProviderSalesforce] = initialProviderState(cfg.Salesforce.Enabled, cfg.SalesforceConfigured(), salesforceErr)
	dispatcher.states[ProviderMailchimp] = initialProviderState(cfg.Mailchimp.Enabled, cfg.MailchimpConfigured(), mailchimpErr)
	return dispatcher
}

func initialProviderState(enabled, configured bool, err error) providerState {
	state := providerState{enabled: enabled, configured: configured && err == nil}
	if err != nil {
		state.lastError = "provider configuration is invalid"
	} else if enabled && !configured {
		state.lastError = "provider configuration is incomplete"
	}
	return state
}

func (d *CRMDispatcher) DeliverLead(ctx context.Context, lead *models.Lead) []DeliveryResult {
	if d == nil || lead == nil {
		return nil
	}
	results := make([]DeliveryResult, 0)
	for _, provider := range d.order {
		connector, ok := d.connectors[provider].(crmLeadConnector)
		if !ok || !d.providerEnabled(provider) {
			continue
		}
		if !d.providerConfigured(provider) {
			results = append(results, d.recordFailure(provider))
			continue
		}
		if err := connector.SyncLead(ctx, lead); err != nil {
			results = append(results, d.recordFailure(provider))
			continue
		}
		results = append(results, d.recordSuccess(provider))
	}
	return results
}

func (d *CRMDispatcher) DeliverSubscriber(ctx context.Context, subscriber *models.Subscriber) []DeliveryResult {
	if d == nil || subscriber == nil {
		return nil
	}
	results := make([]DeliveryResult, 0)
	for _, provider := range d.order {
		connector, ok := d.connectors[provider].(crmSubscriberConnector)
		if !ok || !d.providerEnabled(provider) {
			continue
		}
		if !d.providerConfigured(provider) {
			results = append(results, d.recordFailure(provider))
			continue
		}
		if err := connector.SyncSubscriber(ctx, subscriber); err != nil {
			results = append(results, d.recordFailure(provider))
			continue
		}
		results = append(results, d.recordSuccess(provider))
	}
	return results
}

func (d *CRMDispatcher) providerEnabled(provider string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.states[provider].enabled
}

func (d *CRMDispatcher) providerConfigured(provider string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.states[provider].configured
}

func (d *CRMDispatcher) recordFailure(provider string) DeliveryResult {
	d.mu.Lock()
	state := d.states[provider]
	state.lastError = "provider delivery failed"
	d.states[provider] = state
	d.mu.Unlock()
	return DeliveryResult{Provider: provider, Error: "provider delivery failed"}
}

func (d *CRMDispatcher) recordSuccess(provider string) DeliveryResult {
	now := time.Now().UTC()
	d.mu.Lock()
	state := d.states[provider]
	state.lastError = ""
	state.lastSuccessAt = &now
	d.states[provider] = state
	d.mu.Unlock()
	return DeliveryResult{Provider: provider, Delivered: true}
}

func (d *CRMDispatcher) Status() []CRMStatus {
	if d == nil {
		return nil
	}
	d.mu.RLock()
	defer d.mu.RUnlock()
	statuses := make([]CRMStatus, 0, len(d.order))
	for _, provider := range d.order {
		state := d.states[provider]
		statuses = append(statuses, CRMStatus{
			Provider: provider, Enabled: state.enabled, Configured: state.configured,
			Verified: state.lastSuccessAt != nil, LastError: state.lastError, LastSuccessAt: state.lastSuccessAt,
		})
	}
	return statuses
}

func (s *Service) CRMStatus() []CRMStatus { return s.crm.Status() }

type hubSpotConnector struct {
	config  config.CRMProviderConfig
	baseURL string
	client  *http.Client
}

func newHubSpotConnector(cfg config.CRMProviderConfig, client *http.Client) (*hubSpotConnector, error) {
	base := cfg.InstanceURL
	if base == "" {
		base = "https://api.hubapi.com"
	}
	base, err := providerBaseURL(base)
	return &hubSpotConnector{config: cfg, baseURL: base, client: client}, err
}

func (c *hubSpotConnector) SyncLead(ctx context.Context, lead *models.Lead) error {
	firstName, lastName := splitContactName(lead.Name)
	properties := map[string]string{"email": lead.NormalizedEmail}
	if properties["email"] == "" {
		properties["email"] = strings.ToLower(strings.TrimSpace(lead.Email))
	}
	if firstName != "" {
		properties["firstname"] = firstName
	}
	if lastName != "" {
		properties["lastname"] = lastName
	}
	body := map[string]any{"inputs": []any{map[string]any{
		"id": properties["email"], "idProperty": "email", "properties": properties,
	}}}
	request, err := jsonRequest(ctx, http.MethodPost, c.baseURL+"/crm/v3/objects/contacts/batch/upsert", body)
	if err != nil {
		return err
	}
	setBearer(request, providerCredential(c.config))
	_, err = performJSONRequest(c.client, request)
	return err
}

type salesforceConnector struct {
	config  config.CRMProviderConfig
	baseURL string
	client  *http.Client
}

func newSalesforceConnector(cfg config.CRMProviderConfig, client *http.Client) (*salesforceConnector, error) {
	base, err := providerBaseURL(cfg.InstanceURL)
	return &salesforceConnector{config: cfg, baseURL: base, client: client}, err
}

func (c *salesforceConnector) SyncLead(ctx context.Context, lead *models.Lead) error {
	email := lead.NormalizedEmail
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(lead.Email))
	}
	query := "SELECT Id FROM Lead WHERE Email = '" + soqlString(email) + "' LIMIT 1"
	endpoint := c.baseURL + "/services/data/v60.0/query?q=" + url.QueryEscape(query)
	request, err := jsonRequest(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	setBearer(request, providerCredential(c.config))
	response, err := performJSONRequest(c.client, request)
	if err != nil {
		return err
	}
	var existing struct {
		Records []struct {
			ID string `json:"Id"`
		} `json:"records"`
	}
	if err := json.Unmarshal(response, &existing); err != nil {
		return errors.New("salesforce response was invalid")
	}
	firstName, lastName := splitContactName(lead.Name)
	if lastName == "" {
		lastName = "Reader"
	}
	body := map[string]string{"Email": email, "LastName": lastName, "Company": "Flipbook"}
	if firstName != "" {
		body["FirstName"] = firstName
	}
	method := http.MethodPost
	endpoint = c.baseURL + "/services/data/v60.0/sobjects/Lead/"
	if len(existing.Records) > 0 && existing.Records[0].ID != "" {
		method = http.MethodPatch
		endpoint = c.baseURL + "/services/data/v60.0/sobjects/Lead/" + url.PathEscape(existing.Records[0].ID)
	}
	request, err = jsonRequest(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	setBearer(request, providerCredential(c.config))
	_, err = performJSONRequest(c.client, request)
	return err
}

type mailchimpConnector struct {
	config  config.CRMProviderConfig
	baseURL string
	client  *http.Client
}

func newMailchimpConnector(cfg config.CRMProviderConfig, client *http.Client) (*mailchimpConnector, error) {
	base := cfg.InstanceURL
	if base == "" && cfg.APIKey != "" {
		separator := strings.LastIndex(cfg.APIKey, "-")
		if separator >= 0 && separator < len(cfg.APIKey)-1 {
			base = "https://" + cfg.APIKey[separator+1:] + ".api.mailchimp.com/3.0"
		}
	}
	if base == "" {
		return &mailchimpConnector{config: cfg, client: client}, errors.New("mailchimp API base URL could not be derived")
	}
	base, err := providerBaseURL(base)
	return &mailchimpConnector{config: cfg, baseURL: base, client: client}, err
}

func (c *mailchimpConnector) SyncLead(ctx context.Context, lead *models.Lead) error {
	email := lead.NormalizedEmail
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(lead.Email))
	}
	firstName, lastName := splitContactName(lead.Name)
	body := map[string]any{
		"email_address": email, "status_if_new": "transactional",
		"merge_fields": map[string]string{"FNAME": firstName, "LNAME": lastName},
	}
	return c.upsertMember(ctx, email, body)
}

func (c *mailchimpConnector) SyncSubscriber(ctx context.Context, subscriber *models.Subscriber) error {
	status := "pending"
	switch subscriber.Status {
	case models.SubscriberSubscribed:
		status = "subscribed"
	case models.SubscriberUnsubscribed:
		status = "unsubscribed"
	}
	body := map[string]any{
		"email_address": subscriber.NormalizedEmail, "status": status, "status_if_new": status,
	}
	return c.upsertMember(ctx, subscriber.NormalizedEmail, body)
}

func (c *mailchimpConnector) upsertMember(ctx context.Context, email string, body map[string]any) error {
	digest := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(email))))
	endpoint := c.baseURL + "/lists/" + url.PathEscape(c.config.ListID) + "/members/" + hex.EncodeToString(digest[:])
	request, err := jsonRequest(ctx, http.MethodPut, endpoint, body)
	if err != nil {
		return err
	}
	if c.config.APIKey != "" {
		request.SetBasicAuth("flipbook", c.config.APIKey)
	} else {
		setBearer(request, c.config.AccessToken)
	}
	_, err = performJSONRequest(c.client, request)
	return err
}

func providerCredential(cfg config.CRMProviderConfig) string {
	if cfg.AccessToken != "" {
		return cfg.AccessToken
	}
	return cfg.APIKey
}

func setBearer(request *http.Request, credential string) {
	request.Header.Set("Authorization", "Bearer "+credential)
}

func splitContactName(name string) (string, string) {
	parts := strings.Fields(name)
	if len(parts) < 2 {
		return strings.Join(parts, " "), ""
	}
	return strings.Join(parts[:len(parts)-1], " "), parts[len(parts)-1]
}

func soqlString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `\'`)
}

func providerBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("provider instance URL is invalid")
	}
	if parsed.Scheme != "https" {
		loopback := parsed.Scheme == "http" && (strings.EqualFold(parsed.Hostname(), "localhost") || (net.ParseIP(parsed.Hostname()) != nil && net.ParseIP(parsed.Hostname()).IsLoopback()))
		if !loopback {
			return "", errors.New("provider instance URL must use HTTPS except on loopback")
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return strings.TrimRight(parsed.String(), "/"), nil
}

func jsonRequest(ctx context.Context, method, endpoint string, body any) (*http.Request, error) {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, errors.New("provider request could not be created")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Flipbook-Engagement/1.0")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	return request, nil
}

func performJSONRequest(client *http.Client, request *http.Request) ([]byte, error) {
	if client == nil {
		return nil, errors.New("provider HTTP client is not configured")
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, errors.New("provider request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, errors.New("provider response could not be read")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("provider returned HTTP %d", response.StatusCode)
	}
	return body, nil
}
