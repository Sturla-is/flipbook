package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jonradoff/flipbook/internal/config"
	"github.com/jonradoff/flipbook/internal/models"
	"github.com/jonradoff/flipbook/internal/storage"
)

var testPDF = []byte("%PDF-1.7\n1 0 obj<</Type/Catalog>>endobj\n%%EOF\n")

func TestCanvaExportReuploadStatisticsAndStableRevisionMapping(t *testing.T) {
	store := newFakeStore()
	queue := &fakeQueue{}
	exportCount := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/exports":
			exportCount++
			if request.Header.Get("Authorization") != "Bearer access-token" {
				t.Errorf("missing Canva bearer token: %q", request.Header.Get("Authorization"))
			}
			var input struct {
				DesignID string `json:"design_id"`
				Format   struct {
					Type string `json:"type"`
				} `json:"format"`
			}
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.DesignID != "design-1" || input.Format.Type != "pdf" {
				t.Errorf("unexpected Canva export request: %+v", input)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"job":{"id":"job-%d","status":"in_progress"}}`, exportCount)
		case request.Method == http.MethodGet && strings.HasPrefix(request.URL.Path, "/exports/"):
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"job":{"id":%q,"status":"success","urls":[%q]}}`, strings.TrimPrefix(request.URL.Path, "/exports/"), server.URL+"/files/design.pdf")
		case request.Method == http.MethodGet && request.URL.Path == "/files/design.pdf":
			writer.Header().Set("Content-Type", "application/pdf")
			_, _ = writer.Write(testPDF)
		case request.Method == http.MethodGet && request.URL.Path == "/designs/design-1":
			if request.Header.Get("Authorization") != "Bearer access-token" {
				t.Errorf("statistics request is not authenticated")
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"design":{"id":"design-1","title":"Quarterly report","page_count":12,"created_at":1700000000,"updated_at":1700000100}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/designs" && request.URL.Query().Get("limit") == "1":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"items":[]}`))
		default:
			http.Error(writer, "unexpected mock request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	service := testService(t, store, queue, ProviderCanva, server.URL)
	seedConnection(t, service, store, ProviderCanva, false)

	first, err := service.ExportAndReupload(context.Background(), ExportRequest{
		Provider: ProviderCanva, WorkspaceID: "workspace", UserID: "user", ExternalID: "design-1", Title: "Board report",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.FlipbookID == "" || first.RevisionID == "" || first.JobID != "conversion-1" || first.Status != "queued" {
		t.Fatalf("unexpected first export result: %+v", first)
	}
	flipbook := store.flipbooks[first.FlipbookID]
	if flipbook == nil || flipbook.ExternalSource != ProviderCanva || flipbook.ExternalID != "design-1" || flipbook.PublicationState != models.PublicationDraft {
		t.Fatalf("provider document was not mapped to a draft flipbook: %+v", flipbook)
	}
	if flipbook.CurrentRevisionID != first.RevisionID || flipbook.SourceChecksum == "" {
		t.Fatalf("initial revision mapping is incomplete: %+v", flipbook)
	}
	if len(queue.jobs) != 1 || queue.jobs[0].RevisionID != first.RevisionID {
		t.Fatalf("conversion was not queued for the initial revision: %+v", queue.jobs)
	}
	contents, err := os.ReadFile(queue.jobs[0].SourcePath)
	if err != nil || string(contents) != string(testPDF) {
		t.Fatalf("stored provider PDF does not match: %v", err)
	}

	second, err := service.ExportAndReupload(context.Background(), ExportRequest{
		Provider: ProviderCanva, WorkspaceID: "workspace", UserID: "user", ExternalID: "design-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.FlipbookID != first.FlipbookID || second.RevisionID == first.RevisionID || len(store.flipbooks) != 1 {
		t.Fatalf("re-export did not reuse the stable publication: first=%+v second=%+v", first, second)
	}
	revision := store.revisions[second.RevisionID]
	if revision == nil || revision.Number != 2 || revision.AssetVersion != 2 || revision.ExternalID != "design-1" || revision.SourceChecksum == "" {
		t.Fatalf("second provider revision was not mapped correctly: %+v", revision)
	}
	if len(queue.jobs) != 2 || queue.jobs[0].SourcePath == queue.jobs[1].SourcePath || queue.jobs[1].RevisionID != second.RevisionID {
		t.Fatalf("re-export source or revision queue is unstable: %+v", queue.jobs)
	}

	statistics, err := service.Statistics(context.Background(), "workspace", "user", ProviderCanva, "design-1")
	if err != nil {
		t.Fatal(err)
	}
	if statistics.Title != "Quarterly report" || statistics.PageCount != 12 || statistics.CreatedAt != "2023-11-14T22:13:20Z" || statistics.Data["design"] == nil {
		t.Fatalf("unexpected Canva statistics: %+v", statistics)
	}
	verified, err := service.VerifyConnection(context.Background(), "workspace", "user", ProviderCanva)
	if err != nil || verified.LastVerifiedAt == nil || verified.Status != models.IntegrationConnected {
		t.Fatalf("Canva connection verification failed: %+v, %v", verified, err)
	}
	if exportCount != 2 {
		t.Fatalf("Canva export count = %d, want 2", exportCount)
	}
}

func TestAdobeExpressExportRefreshesTokensAndReadsStatistics(t *testing.T) {
	store := newFakeStore()
	queue := &fakeQueue{}
	refreshCalls := 0
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case request.Method == http.MethodPost && request.URL.Path == "/oauth/token":
			refreshCalls++
			if err := request.ParseForm(); err != nil {
				t.Error(err)
			}
			if request.Form.Get("grant_type") != "refresh_token" || request.Form.Get("refresh_token") != "refresh-token" || request.Form.Get("client_id") != "client-id" || request.Form.Get("client_secret") != "client-secret" {
				t.Errorf("invalid Adobe refresh request: %v", request.Form)
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"access_token":"refreshed-token","refresh_token":"rotated-refresh","expires_in":3600,"token_type":"Bearer"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/beta/export-rendition":
			if request.Header.Get("Authorization") != "Bearer refreshed-token" || request.Header.Get("X-Api-Key") != "client-id" {
				t.Errorf("Adobe export authentication is incorrect: %v", request.Header)
			}
			var input map[string]any
			if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			options, _ := input["options"].(map[string]any)
			if input["id"] != "urn:aaid:sc:VA6C2:abc" || input["pages"] != "1,3-5" || options["format"] != "application/pdf" || options["downloadIndividualPdfFiles"] != false {
				t.Errorf("unexpected Adobe export request: %+v", input)
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"jobId":"adobe-job-1","statusUrl":%q}`, server.URL+"/status/adobe-job-1")
		case request.Method == http.MethodGet && request.URL.Path == "/status/adobe-job-1":
			if request.Header.Get("Authorization") != "Bearer refreshed-token" || request.Header.Get("X-Api-Key") != "client-id" {
				t.Errorf("Adobe status authentication is incorrect")
			}
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"jobId":"adobe-job-1","status":"succeeded","pageRenditionsResult":[{"pageNumber":1,"renditionUrl":%q}]}`, server.URL+"/renditions/adobe.pdf")
		case request.Method == http.MethodGet && request.URL.Path == "/renditions/adobe.pdf":
			_, _ = writer.Write(testPDF)
		case request.Method == http.MethodGet && request.URL.Path == "/beta/tagged-documents/urn:aaid:sc:VA6C2:abc":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"document":{"id":"urn:aaid:sc:VA6C2:abc","name":"Adobe brochure","pageCount":3,"updatedAt":"2026-01-02T03:04:05Z"}}`))
		default:
			http.Error(writer, "unexpected mock request", http.StatusNotFound)
		}
	}))
	defer server.Close()
	service := testService(t, store, queue, ProviderAdobeExpress, server.URL)
	connection := seedConnection(t, service, store, ProviderAdobeExpress, true)

	result, err := service.ExportAndReupload(context.Background(), ExportRequest{
		Provider: ProviderAdobeExpress, WorkspaceID: "workspace", UserID: "user",
		ExternalID: "urn:aaid:sc:VA6C2:abc", Pages: "1,3-5",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.FlipbookID == "" || result.JobID != "conversion-1" || refreshCalls != 1 {
		t.Fatalf("Adobe export or refresh failed: %+v, refresh calls=%d", result, refreshCalls)
	}
	stored := store.connections[connection.ID]
	accessToken, err := service.cipher.Open(connectionTokenContext(connection.ID, "access"), stored.AccessTokenEncrypted)
	if err != nil || accessToken != "refreshed-token" {
		t.Fatalf("refreshed access token was not stored: %q, %v", accessToken, err)
	}
	refreshToken, err := service.cipher.Open(connectionTokenContext(connection.ID, "refresh"), stored.RefreshTokenEncrypted)
	if err != nil || refreshToken != "rotated-refresh" {
		t.Fatalf("rotated refresh token was not stored: %q, %v", refreshToken, err)
	}
	statistics, err := service.Statistics(context.Background(), "workspace", "user", ProviderAdobeExpress, "urn:aaid:sc:VA6C2:abc")
	if err != nil {
		t.Fatal(err)
	}
	if statistics.Title != "Adobe brochure" || statistics.PageCount != 3 || statistics.UpdatedAt != "2026-01-02T03:04:05Z" {
		t.Fatalf("unexpected Adobe statistics: %+v", statistics)
	}
}

func TestProviderPermissionErrorsAreActionable(t *testing.T) {
	store := newFakeStore()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"code":"permission_denied","message":"Not allowed to access design"}`))
	}))
	defer server.Close()
	service := testService(t, store, &fakeQueue{}, ProviderCanva, server.URL)
	connection := seedConnection(t, service, store, ProviderCanva, false)
	_, err := service.ExportAndReupload(context.Background(), ExportRequest{
		Provider: ProviderCanva, WorkspaceID: "workspace", UserID: "user", ExternalID: "private-design",
	})
	if !errors.Is(err, ErrPermission) || HTTPStatus(err) != http.StatusForbidden {
		t.Fatalf("expected a permission error, got %v", err)
	}
	message := PublicMessage(err)
	if !strings.Contains(message, "app approval") || !strings.Contains(message, "OAuth scopes") || !strings.Contains(message, "Not allowed") {
		t.Fatalf("permission error is not actionable: %q", message)
	}
	stored := store.connections[connection.ID]
	if stored.Status != models.IntegrationError || !strings.Contains(stored.LastError, "permission_denied") {
		t.Fatalf("provider failure was not reflected in connection status: %+v", stored)
	}
}

func TestProviderConfigUsesSupportedAPIDefaults(t *testing.T) {
	cfg := &config.Config{}
	cfg.Providers.Canva = config.OAuthProviderConfig{APIBaseURL: "https://api.canva.com"}
	cfg.Providers.AdobeExpress = config.OAuthProviderConfig{APIBaseURL: "https://cc-api-behance.adobe.io"}
	options := OptionsFromConfig(cfg)
	if options.Providers[ProviderCanva].APIBaseURL != "https://api.canva.com/rest/v1" {
		t.Fatalf("unexpected Canva API base: %q", options.Providers[ProviderCanva].APIBaseURL)
	}
	if options.Providers[ProviderAdobeExpress].APIBaseURL != "https://express-api.adobe.io" {
		t.Fatalf("unexpected Adobe Express API base: %q", options.Providers[ProviderAdobeExpress].APIBaseURL)
	}
	if options.Providers[ProviderAdobeExpress].ExportPath != "/beta/export-rendition" || options.Providers[ProviderAdobeExpress].ExportStatusPath != "/status/{jobID}" {
		t.Fatalf("unexpected Adobe Express endpoint mapping: %+v", options.Providers[ProviderAdobeExpress])
	}
}

func TestQueueFailureRetainsRevisionSource(t *testing.T) {
	store := newFakeStore()
	queue := &fakeQueue{err: errors.New("queue unavailable")}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/exports":
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"job":{"id":"job-1","status":"in_progress"}}`))
		case "/exports/job-1":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(writer, `{"job":{"id":"job-1","status":"success","urls":[%q]}}`, server.URL+"/document.pdf")
		case "/document.pdf":
			_, _ = writer.Write(testPDF)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	service := testService(t, store, queue, ProviderCanva, server.URL)
	seedConnection(t, service, store, ProviderCanva, false)
	result, err := service.ExportAndReupload(context.Background(), ExportRequest{
		Provider: ProviderCanva, WorkspaceID: "workspace", UserID: "user", ExternalID: "design-queue",
	})
	if err == nil || result == nil || result.FlipbookID == "" || !strings.Contains(PublicMessage(err), "saved as a new revision") {
		t.Fatalf("queue failure did not preserve an actionable result: result=%+v err=%v", result, err)
	}
	revision := store.revisions[result.RevisionID]
	if revision == nil || revision.SourceChecksum == "" {
		t.Fatalf("revision was not retained after queue failure: %+v", revision)
	}
	if len(queue.jobs) != 0 {
		t.Fatalf("failed queue unexpectedly captured jobs: %+v", queue.jobs)
	}
	if _, err := os.Stat(service.assets.(*storage.Storage).FlipbookDir(result.FlipbookID)); err != nil {
		t.Fatalf("revision source directory was removed: %v", err)
	}
}

func TestStatisticsRequiresAConfiguredConnection(t *testing.T) {
	service := testService(t, newFakeStore(), &fakeQueue{}, "", "")
	if _, err := service.Statistics(context.Background(), "workspace", "user", ProviderCanva, "design-1"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("unconfigured statistics did not fail clearly: %v", err)
	}
}
