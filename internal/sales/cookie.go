package sales

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/publishing"
)

const purchaserCookieVersion = 1

type PurchaserClaims struct {
	Version     int    `json:"v"`
	WorkspaceID string `json:"wid"`
	FlipbookID  string `json:"fid"`
	PurchaseID  string `json:"pid"`
	EmailHash   string `json:"eh"`
	IssuedAt    int64  `json:"iat"`
	ExpiresAt   int64  `json:"exp"`
}

func purchaserIdentity(purchaseID, email string) string {
	return models.HashToken("purchaser\x00" + purchaseID + "\x00" + strings.ToLower(strings.TrimSpace(email)))
}

func (s *Service) purchaserCookieName(flipbookID string) string {
	digest := sha256.Sum256([]byte(flipbookID))
	return s.cookieName + "_" + hex.EncodeToString(digest[:8])
}

func (s *Service) issuePurchaserCookie(purchase *models.Purchase) (*http.Cookie, error) {
	if len(s.secret) < 32 {
		return nil, ErrNotConfigured
	}
	if purchase == nil || purchase.Status != models.PurchasePaid || purchase.EmailHash == "" {
		return nil, ErrPaymentIncomplete
	}
	now := s.timeNow()
	expires := now.Add(s.purchaserTTL)
	claims := PurchaserClaims{
		Version: purchaserCookieVersion, WorkspaceID: purchase.WorkspaceID,
		FlipbookID: purchase.FlipbookID, PurchaseID: purchase.ID, EmailHash: purchase.EmailHash,
		IssuedAt: now.Unix(), ExpiresAt: expires.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return nil, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	signature := s.purchaserSignature(encoded)
	token := encoded + "." + base64.RawURLEncoding.EncodeToString(signature)
	return &http.Cookie{
		Name: s.purchaserCookieName(purchase.FlipbookID), Value: token, Path: "/",
		Expires: expires, MaxAge: int(s.purchaserTTL.Seconds()), HttpOnly: true,
		Secure: s.cookieSecure, SameSite: s.cookieSameSite, Domain: s.cookieDomain,
	}, nil
}

func (s *Service) parsePurchaserToken(token, workspaceID, flipbookID string) (*PurchaserClaims, error) {
	if len(s.secret) < 32 || len(token) == 0 || len(token) > 16384 {
		return nil, ErrInvalid
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return nil, ErrInvalid
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(provided, s.purchaserSignature(parts[0])) {
		return nil, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrInvalid
	}
	var claims PurchaserClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrInvalid
	}
	hash, err := hex.DecodeString(claims.EmailHash)
	now := s.timeNow()
	if claims.Version != purchaserCookieVersion || claims.WorkspaceID != workspaceID ||
		claims.FlipbookID != flipbookID || claims.PurchaseID == "" || err != nil || len(hash) != sha256.Size ||
		claims.IssuedAt > now.Add(5*time.Minute).Unix() || claims.ExpiresAt <= now.Unix() {
		return nil, ErrInvalid
	}
	return &claims, nil
}

func (s *Service) purchaserSignature(payload string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = io.WriteString(mac, "purchaser-v1\x00"+payload)
	return mac.Sum(nil)
}

func (s *Service) ReaderFromRequest(ctx context.Context, request *http.Request, workspaceID, flipbookID string) (publishing.Reader, error) {
	reader := publishing.Reader{}
	if request == nil {
		return reader, nil
	}
	cookie, err := request.Cookie(s.purchaserCookieName(flipbookID))
	if err != nil || cookie.Value == "" {
		return reader, nil
	}
	claims, err := s.parsePurchaserToken(cookie.Value, workspaceID, flipbookID)
	if err != nil {
		return reader, nil
	}
	active, err := s.store.HasEntitlement(ctx, workspaceID, flipbookID, "", claims.EmailHash, s.timeNow())
	if err != nil {
		return reader, err
	}
	if active {
		reader.EmailHash = claims.EmailHash
	}
	return reader, nil
}

func (s *Service) ClearPurchaserCookie(flipbookID string) *http.Cookie {
	return &http.Cookie{
		Name: s.purchaserCookieName(flipbookID), Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(1, 0), HttpOnly: true, Secure: s.cookieSecure,
		SameSite: s.cookieSameSite, Domain: s.cookieDomain,
	}
}

func randomID(reader io.Reader) (string, error) {
	if reader == nil {
		reader = rand.Reader
	}
	buffer := make([]byte, 18)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", fmt.Errorf("generate purchase id: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func validatePurchaseID(value string) error {
	if value == "" || len(value) > 128 || strings.ContainsAny(value, "/?#\\\r\n") {
		return invalid("invalid purchase id")
	}
	return nil
}
