package publishing

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"golang.org/x/crypto/bcrypt"
)

func TestPasswordGrantLifecycleAndCookie(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	hash, err := bcrypt.GenerateFromPassword([]byte("correct horse"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	fb.Visibility, fb.PasswordHash = models.VisibilityPassword, string(hash)
	service := testService(t, store, assets, now)
	service.cookieSecure = true

	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{}); ErrorCodeOf(err) != CodePasswordRequired {
		t.Fatalf("anonymous password access error = %v", err)
	}
	if _, err := service.IssuePasswordGrant(ctx, fb.WorkspaceID, fb.ID, "wrong", AccessRequest{}); ErrorCodeOf(err) != CodePasswordRequired {
		t.Fatalf("wrong password error = %v", err)
	}
	grant, err := service.IssuePasswordGrant(ctx, fb.WorkspaceID, fb.ID, "correct horse", AccessRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if grant.Token == "" || grant.Cookie == nil || !grant.Cookie.HttpOnly || !grant.Cookie.Secure || grant.Cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("invalid grant cookie: %+v", grant)
	}
	if grant.ExpiresAt != now.Add(24*time.Hour) {
		t.Fatalf("grant expiry = %v", grant.ExpiresAt)
	}
	auth, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{Grant: grant.Token})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Resource != ResourceViewer || auth.Flipbook.PasswordHash != "" || auth.Flipbook.AccessTokens != nil {
		t.Fatalf("authorization leaked secrets or wrong resource: %+v", auth)
	}
	request := &http.Request{Header: make(http.Header)}
	request.AddCookie(grant.Cookie)
	if got := service.GrantFromRequest(request, fb.ID); got != grant.Token {
		t.Fatalf("cookie grant did not round trip")
	}
	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{Grant: grant.Token + "tampered"}); ErrorCodeOf(err) != CodeInvalidGrant {
		t.Fatalf("tampered grant error = %v", err)
	}
	newHash, _ := bcrypt.GenerateFromPassword([]byte("new password"), bcrypt.MinCost)
	fb.PasswordHash = string(newHash)
	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{Grant: grant.Token}); ErrorCodeOf(err) != CodePasswordRequired {
		t.Fatalf("grant survived password change: %v", err)
	}
	if cleared := service.ClearAccessCookie(fb.ID); cleared.MaxAge != -1 || cleared.Value != "" {
		t.Fatalf("clear cookie = %+v", cleared)
	}
}

func TestShareTokenCreationResolutionAndRevocation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	fb.Visibility = models.VisibilityToken
	service := testService(t, store, assets, now)

	created, err := service.CreateShareToken(ctx, Actor{UserID: "owner"}, fb.WorkspaceID, fb.ID, "Reader link", nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.Token == "" || created.ID == "" || strings.Contains(created.Token, ".") {
		t.Fatalf("invalid raw share token: %+v", created)
	}
	if len(fb.AccessTokens) != 1 || fb.AccessTokens[0].TokenHash == created.Token || fb.AccessTokens[0].TokenHash != models.HashToken(created.Token) {
		t.Fatalf("raw token persisted incorrectly: %+v", fb.AccessTokens)
	}
	if _, err := service.ResolveShareToken(ctx, created.Token, AccessRequest{}); err != nil {
		t.Fatal(err)
	}
	grant, err := service.IssueShareGrant(ctx, fb.WorkspaceID, fb.ID, created.Token, AccessRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeShareToken(ctx, Actor{UserID: "owner"}, fb.WorkspaceID, fb.ID, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{ShareToken: created.Token}); ErrorCodeOf(err) != CodeShareTokenRequired {
		t.Fatalf("revoked raw token still works: %v", err)
	}
	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{Grant: grant.Token}); ErrorCodeOf(err) != CodeShareTokenRequired {
		t.Fatalf("revoked signed token still works: %v", err)
	}
	if _, err := service.ResolveShareToken(ctx, created.Token, AccessRequest{}); HTTPStatus(err) != http.StatusNotFound {
		t.Fatalf("revoked token resolution status = %d (%v)", HTTPStatus(err), err)
	}
}

func TestLeadGrantAndDismissal(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	fb.LeadGate = models.LeadGateSettings{Enabled: true, Mandatory: true, PreviewPages: 1, GateReading: true}
	service := testService(t, store, assets, now)

	grant, err := service.IssueLeadGrant(ctx, fb.WorkspaceID, fb.ID, true, false, AccessRequest{})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetPage, "", AccessRequest{Grant: grant.Token, Page: 5})
	if err != nil || !auth.Decision.FullAccess {
		t.Fatalf("captured lead did not unlock asset: %+v, %v", auth, err)
	}
	if _, err := service.IssueLeadGrant(ctx, fb.WorkspaceID, fb.ID, false, true, AccessRequest{}); ErrorCodeOf(err) != CodeLeadRequired {
		t.Fatalf("mandatory gate dismissal error = %v", err)
	}
	fb.LeadGate.Mandatory, fb.LeadGate.Dismissible = false, true
	dismissed, err := service.IssueLeadGrant(ctx, fb.WorkspaceID, fb.ID, false, true, AccessRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AuthorizeViewer(ctx, fb.WorkspaceID, fb.ID, AccessRequest{Grant: dismissed.Token}); err != nil {
		t.Fatal(err)
	}
}

func TestMembershipEntitlementAndWorkspaceIsolation(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	fb.Sales = models.SalesSettings{Enabled: true, PriceCents: 100, Currency: "USD", PreviewPages: 1}
	store.memberships[membershipKey(fb.WorkspaceID, "reader")] = &models.Membership{
		WorkspaceID: fb.WorkspaceID, UserID: "reader", Role: models.RoleReader,
	}
	service := testService(t, store, assets, now)
	request := AccessRequest{Reader: Reader{UserID: "reader"}, Page: 3}
	if _, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetPage, "", request); ErrorCodeOf(err) != CodePurchaseRequired {
		t.Fatalf("missing entitlement error = %v", err)
	}
	store.entitled = true
	if _, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetPage, "", request); err != nil {
		t.Fatal(err)
	}
	store.entitlementErr = errors.New("database unavailable")
	if _, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetPage, "", request); HTTPStatus(err) != http.StatusInternalServerError {
		t.Fatalf("entitlement failure did not fail closed: %v", err)
	}
	if _, err := service.AuthorizeViewer(ctx, "other-workspace", fb.ID, AccessRequest{}); HTTPStatus(err) != http.StatusNotFound {
		t.Fatalf("cross-workspace lookup status = %d", HTTPStatus(err))
	}
}

func TestAssetAuthorizationRevisionPathsAndRanges(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, revision := installTestPublication(store, assets, now)
	service := testService(t, store, assets, now)

	auth, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetPage, "", AccessRequest{Page: 2})
	if err != nil {
		t.Fatal(err)
	}
	if auth.Resource != ResourcePage || auth.AssetName != "pages/page-2.png" ||
		auth.AssetURL != "/data/flipbooks/book/revisions/1/pages/page-2.png" || auth.Revision.ID != revision.ID {
		t.Fatalf("page asset authorization = %+v", auth)
	}
	if _, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetPage, "", AccessRequest{Page: 6}); HTTPStatus(err) != http.StatusNotFound {
		t.Fatalf("out-of-range page status = %d (%v)", HTTPStatus(err), err)
	}
	if _, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetPage, "../converted.pdf", AccessRequest{Page: 1}); ErrorCodeOf(err) != CodeInvalid {
		t.Fatalf("path traversal error = %v", err)
	}
	text, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetText, "", AccessRequest{Page: 1})
	if err != nil || text.AssetURL != "" || text.Resource != ResourceText {
		t.Fatalf("page text should require filtered serving: %+v, %v", text, err)
	}

	fb.Sales = models.SalesSettings{Enabled: true, PriceCents: 100, Currency: "USD", PreviewPages: 1}
	manifest, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetManifest, "", AccessRequest{})
	if err != nil || manifest.AssetURL != "" || manifest.Decision.FullAccess {
		t.Fatalf("paid manifest must be filtered: %+v, %v", manifest, err)
	}

	validated := now
	store.revisions["revision-2"] = &models.Revision{
		ID: "revision-2", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID, Number: 2,
		AssetVersion: 2, PageCount: 2, Status: models.RevisionReady, ValidatedAt: &validated,
	}
	if _, err := service.AuthorizeAsset(ctx, fb.WorkspaceID, fb.ID, AssetPage, "", AccessRequest{Page: 1, Revision: 2}); HTTPStatus(err) != http.StatusNotFound {
		t.Fatalf("inactive revision was publicly readable: %v", err)
	}
}

func TestGrantValidationAndConstructor(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	store, assets := newFakeStore(), newFakeAssets()
	fb, _ := installTestPublication(store, assets, now)
	service := testService(t, store, assets, now)
	grant, err := service.signGrant(AccessGrant{
		Version: grantVersion, WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		IssuedAt: now.Unix(), ExpiresAt: now.Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := service.ValidateGrant(grant, fb.WorkspaceID, fb.ID)
	if err != nil || claims.FlipbookID != fb.ID {
		t.Fatalf("valid grant rejected: %+v, %v", claims, err)
	}
	if _, err := service.ValidateGrant(grant, fb.WorkspaceID, "other"); ErrorCodeOf(err) != CodeInvalidGrant {
		t.Fatalf("cross-publication grant error = %v", err)
	}
	expired, _ := service.signGrant(AccessGrant{
		Version: grantVersion, WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		IssuedAt: now.Add(-2 * time.Hour).Unix(), ExpiresAt: now.Add(-time.Hour).Unix(),
	})
	if _, err := service.ValidateGrant(expired, fb.WorkspaceID, fb.ID); ErrorCodeOf(err) != CodeInvalidGrant {
		t.Fatalf("expired grant error = %v", err)
	}
	if _, err := NewService(store, assets, Options{SigningSecret: "short"}); err == nil {
		t.Fatal("short signing secret accepted")
	}
}
