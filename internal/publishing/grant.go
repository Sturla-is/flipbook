package publishing

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

const grantVersion = 1

type AccessGrant struct {
	Version       int    `json:"v"`
	WorkspaceID   string `json:"wid"`
	FlipbookID    string `json:"fid"`
	IssuedAt      int64  `json:"iat"`
	ExpiresAt     int64  `json:"exp"`
	PasswordProof string `json:"pp,omitempty"`
	ShareTokenID  string `json:"st,omitempty"`
	ShareProof    string `json:"sp,omitempty"`
	LeadCaptured  bool   `json:"lc,omitempty"`
	LeadDismissed bool   `json:"ld,omitempty"`
}

func (s *Service) IssuePasswordGrant(ctx context.Context, workspaceID, flipbookID, password string, request AccessRequest) (*GrantResult, error) {
	if password == "" {
		return nil, unauthorized(CodePasswordRequired, "publication password required")
	}
	request.Password = password
	return s.issueGrant(ctx, workspaceID, flipbookID, request, false, false)
}

func (s *Service) IssueShareGrant(ctx context.Context, workspaceID, flipbookID, rawToken string, request AccessRequest) (*GrantResult, error) {
	if rawToken == "" {
		return nil, unauthorized(CodeShareTokenRequired, "valid share token required")
	}
	request.ShareToken = rawToken
	return s.issueGrant(ctx, workspaceID, flipbookID, request, false, false)
}

func (s *Service) IssueLeadGrant(ctx context.Context, workspaceID, flipbookID string, captured, dismissed bool, request AccessRequest) (*GrantResult, error) {
	if captured == dismissed {
		return nil, invalid("exactly one of lead captured or dismissed is required")
	}
	if dismissed {
		fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
		if err != nil {
			return nil, repositoryError(err)
		}
		if !fb.LeadGate.Enabled || !fb.LeadGate.Dismissible || fb.LeadGate.Mandatory {
			return nil, forbidden(CodeLeadRequired, "this lead gate cannot be dismissed")
		}
	}
	return s.issueGrant(ctx, workspaceID, flipbookID, request, captured, dismissed)
}

func (s *Service) issueGrant(ctx context.Context, workspaceID, flipbookID string, request AccessRequest, captured, dismissed bool) (*GrantResult, error) {
	eval, err := s.evaluate(ctx, workspaceID, flipbookID, ResourceViewer, request, ShareAny)
	if err != nil {
		return nil, err
	}
	fb := eval.flipbook
	if (captured || dismissed) && !fb.LeadGate.Enabled {
		return nil, invalid("lead capture is not enabled for this publication")
	}
	now := s.timeNow()
	expires := now.Add(s.grantTTL)
	grant := AccessGrant{
		Version: grantVersion, WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		IssuedAt: now.Unix(), ExpiresAt: expires.Unix(),
		LeadCaptured:  captured || eval.evidence.LeadCaptured,
		LeadDismissed: dismissed || eval.evidence.LeadDismissed,
	}
	if eval.evidence.Password && fb.PasswordHash != "" {
		grant.PasswordProof = credentialFingerprint(fb.PasswordHash)
	}
	if eval.shareToken != nil {
		grant.ShareTokenID = eval.shareToken.ID
		grant.ShareProof = credentialFingerprint(eval.shareToken.TokenHash)
		if eval.shareToken.ExpiresAt != nil && eval.shareToken.ExpiresAt.Before(expires) {
			expires = *eval.shareToken.ExpiresAt
			grant.ExpiresAt = expires.Unix()
		}
	}
	token, err := s.signGrant(grant)
	if err != nil {
		return nil, internal(err)
	}
	return &GrantResult{Token: token, Cookie: s.AccessCookie(fb.ID, token, expires), ExpiresAt: expires}, nil
}

func (s *Service) ValidateGrant(token, workspaceID, flipbookID string) (*AccessGrant, error) {
	grant, err := s.parseGrant(token)
	if err != nil {
		return nil, err
	}
	if grant.WorkspaceID != workspaceID || grant.FlipbookID != flipbookID {
		return nil, unauthorized(CodeInvalidGrant, "access grant does not match this publication")
	}
	return grant, nil
}

func (s *Service) signGrant(grant AccessGrant) (string, error) {
	payload, err := json.Marshal(grant)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := s.grantSignature(encoded)
	return encoded + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (s *Service) parseGrant(token string) (*AccessGrant, error) {
	if len(token) == 0 || len(token) > 16384 {
		return nil, unauthorized(CodeInvalidGrant, "invalid access grant")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, unauthorized(CodeInvalidGrant, "invalid access grant")
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || subtle.ConstantTimeCompare(provided, s.grantSignature(parts[0])) != 1 {
		return nil, unauthorized(CodeInvalidGrant, "invalid access grant")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, unauthorized(CodeInvalidGrant, "invalid access grant")
	}
	var grant AccessGrant
	if err := json.Unmarshal(payload, &grant); err != nil {
		return nil, unauthorized(CodeInvalidGrant, "invalid access grant")
	}
	now := s.timeNow()
	if grant.Version != grantVersion || grant.WorkspaceID == "" || grant.FlipbookID == "" ||
		grant.IssuedAt > now.Add(5*time.Minute).Unix() || grant.ExpiresAt <= now.Unix() {
		return nil, unauthorized(CodeInvalidGrant, "access grant is invalid or expired")
	}
	return &grant, nil
}

func (s *Service) grantSignature(payload string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = io.WriteString(mac, payload)
	return mac.Sum(nil)
}

func credentialFingerprint(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func findActiveToken(fb *models.Flipbook, rawToken string, now time.Time) *models.AccessToken {
	hash := models.HashToken(rawToken)
	for i := range fb.AccessTokens {
		token := &fb.AccessTokens[i]
		if subtle.ConstantTimeCompare([]byte(token.TokenHash), []byte(hash)) == 1 && token.Active(now) {
			return token
		}
	}
	return nil
}

func findGrantToken(fb *models.Flipbook, grant *AccessGrant, now time.Time) *models.AccessToken {
	for i := range fb.AccessTokens {
		token := &fb.AccessTokens[i]
		if token.ID == grant.ShareTokenID && token.Active(now) &&
			subtle.ConstantTimeCompare([]byte(credentialFingerprint(token.TokenHash)), []byte(grant.ShareProof)) == 1 {
			return token
		}
	}
	return nil
}

func (s *Service) CreateShareToken(ctx context.Context, actor Actor, workspaceID, flipbookID, name string, expiresAt *time.Time) (*ShareTokenResult, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, models.RolePublisher); err != nil {
		return nil, err
	}
	if len(name) > 200 {
		return nil, invalid("share token name is too long")
	}
	if expiresAt != nil && !expiresAt.After(s.timeNow()) {
		return nil, invalid("share token expiry must be in the future")
	}
	if len(fb.AccessTokens) >= 100 {
		return nil, conflict("publication has too many share tokens", nil)
	}
	raw, err := randomURLToken(s.random, 32)
	if err != nil {
		return nil, internal(err)
	}
	id, err := randomURLToken(s.random, 16)
	if err != nil {
		return nil, internal(err)
	}
	token := &models.AccessToken{
		ID: id, Name: strings.TrimSpace(name), TokenHash: models.HashToken(raw),
		CreatedAt: s.timeNow(), ExpiresAt: expiresAt,
	}
	if err := s.store.CreateFlipbookAccessToken(ctx, workspaceID, flipbookID, token); err != nil {
		return nil, repositoryError(err)
	}
	return &ShareTokenResult{ID: token.ID, Token: raw, ExpiresAt: expiresAt}, nil
}

func (s *Service) RevokeShareToken(ctx context.Context, actor Actor, workspaceID, flipbookID, tokenID string) error {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return repositoryError(err)
	}
	if err := s.requireRole(ctx, fb, actor, models.RolePublisher); err != nil {
		return err
	}
	if tokenID == "" {
		return invalid("share token id is required")
	}
	return repositoryError(s.store.RevokeFlipbookAccessToken(ctx, workspaceID, flipbookID, tokenID))
}

func randomURLToken(reader io.Reader, size int) (string, error) {
	if reader == nil {
		reader = rand.Reader
	}
	buffer := make([]byte, size)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", fmt.Errorf("generate access token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (s *Service) AccessCookieName(flipbookID string) string {
	digest := sha256.Sum256([]byte(flipbookID))
	return s.cookieName + "_" + hex.EncodeToString(digest[:8])
}

func (s *Service) AccessCookie(flipbookID, token string, expiresAt time.Time) *http.Cookie {
	maxAge := int(expiresAt.Sub(s.timeNow()).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	return &http.Cookie{
		Name: s.AccessCookieName(flipbookID), Value: token, Path: "/",
		Expires: expiresAt, MaxAge: maxAge, HttpOnly: true, Secure: s.cookieSecure,
		SameSite: s.cookieSameSite, Domain: s.cookieDomain,
	}
}

func (s *Service) ClearAccessCookie(flipbookID string) *http.Cookie {
	return &http.Cookie{
		Name: s.AccessCookieName(flipbookID), Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: s.cookieSecure,
		SameSite: s.cookieSameSite, Domain: s.cookieDomain,
	}
}

func (s *Service) GrantFromRequest(request *http.Request, flipbookID string) string {
	if request == nil {
		return ""
	}
	cookie, err := request.Cookie(s.AccessCookieName(flipbookID))
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (s *Service) RequireRole(ctx context.Context, actor Actor, workspaceID, flipbookID string, role models.Role) error {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return repositoryError(err)
	}
	return s.requireRole(ctx, fb, actor, role)
}

func (s *Service) requireRole(ctx context.Context, fb *models.Flipbook, actor Actor, required models.Role) error {
	if actor.UserID == "" {
		return unauthorized(CodeUnauthorized, "authentication required")
	}
	if actor.UserID == fb.OwnerID {
		return nil
	}
	membership, err := s.store.GetMembership(ctx, fb.WorkspaceID, actor.UserID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return forbidden(CodeForbidden, "insufficient workspace permissions")
	}
	if err != nil {
		return internal(err)
	}
	if membership.WorkspaceID != fb.WorkspaceID || membership.UserID != actor.UserID || !membership.Role.AtLeast(required) {
		return forbidden(CodeForbidden, "insufficient workspace permissions")
	}
	return nil
}
