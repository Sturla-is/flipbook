package publishing

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/metavert-io/flipbook/internal/database"
	"github.com/metavert-io/flipbook/internal/models"
	"github.com/metavert-io/flipbook/internal/storage"
)

type Store interface {
	GetFlipbookInWorkspace(ctx context.Context, workspaceID, id string) (*models.Flipbook, error)
	GetWorkspace(ctx context.Context, workspaceID string) (*models.Workspace, error)
	GetMembership(ctx context.Context, workspaceID, userID string) (*models.Membership, error)
	HasEntitlement(ctx context.Context, workspaceID, flipbookID, userID, emailHash string, now time.Time) (bool, error)
	GetFlipbookByAccessToken(ctx context.Context, rawToken string, now time.Time) (*models.Flipbook, error)
	CreateFlipbookAccessToken(ctx context.Context, workspaceID, flipbookID string, token *models.AccessToken) error
	RevokeFlipbookAccessToken(ctx context.Context, workspaceID, flipbookID, tokenID string) error
	UpdateFlipbookMetadata(ctx context.Context, fb *models.Flipbook) error
	SetPublicationState(ctx context.Context, workspaceID, id string, state models.PublicationState, visibility models.Visibility, now time.Time) error
	ListDuePublications(ctx context.Context, now time.Time) ([]*models.Flipbook, error)
	ArchiveFlipbook(ctx context.Context, workspaceID, id, reason string) error
	RestoreFlipbook(ctx context.Context, workspaceID, id string) error
	SoftDeleteFlipbook(ctx context.Context, workspaceID, id string, purgeDelay time.Duration) error
	ListFlipbooksPendingPurge(ctx context.Context, now time.Time) ([]*models.Flipbook, error)
	PurgeFlipbook(ctx context.Context, workspaceID, id string) error
	DeleteFromGridFS(ctx context.Context, fileIDHex string) error
	ChangeFlipbookSlug(ctx context.Context, workspaceID, flipbookID, newSlug string) error
	GetFlipbookBySlugOrRedirect(ctx context.Context, workspaceID, slug string) (*models.Flipbook, bool, error)
	GetRevision(ctx context.Context, workspaceID, revisionID string) (*models.Revision, error)
	GetCurrentRevision(ctx context.Context, workspaceID, flipbookID string) (*models.Revision, error)
	ListRevisions(ctx context.Context, workspaceID, flipbookID string) ([]*models.Revision, error)
	ActivateRevision(ctx context.Context, workspaceID, flipbookID, revisionID string) error
}

type AssetStore interface {
	RevisionAssetURL(id string, revision int, name string) string
	ActivateRevision(id string, revision int) error
	DeleteFlipbookContext(ctx context.Context, id string) error
}

type Options struct {
	SigningSecret  string
	GrantTTL       time.Duration
	PurgeDelay     time.Duration
	CookieName     string
	CookieDomain   string
	CookieSecure   bool
	CookieSameSite http.SameSite
	Now            func() time.Time
	Random         io.Reader
}

type Service struct {
	store          Store
	assets         AssetStore
	secret         []byte
	grantTTL       time.Duration
	purgeDelay     time.Duration
	cookieName     string
	cookieDomain   string
	cookieSecure   bool
	cookieSameSite http.SameSite
	now            func() time.Time
	random         io.Reader
}

var _ Store = (*database.DB)(nil)
var _ AssetStore = (*storage.Storage)(nil)

func New(db *database.DB, assets *storage.Storage, options Options) (*Service, error) {
	return NewService(db, assets, options)
}

func NewService(store Store, assets AssetStore, options Options) (*Service, error) {
	if store == nil || assets == nil {
		return nil, errors.New("publishing store and asset store are required")
	}
	if len(options.SigningSecret) < 32 {
		return nil, errors.New("publishing signing secret must contain at least 32 bytes")
	}
	if options.GrantTTL == 0 {
		options.GrantTTL = 24 * time.Hour
	}
	if options.GrantTTL < time.Minute || options.GrantTTL > 30*24*time.Hour {
		return nil, errors.New("publishing grant TTL must be between one minute and 30 days")
	}
	if options.PurgeDelay == 0 {
		options.PurgeDelay = 30 * 24 * time.Hour
	}
	if options.PurgeDelay < 0 {
		return nil, errors.New("publishing purge delay must not be negative")
	}
	if options.CookieName == "" {
		options.CookieName = "flipbook_access"
	}
	if options.CookieSameSite == 0 || options.CookieSameSite == http.SameSiteDefaultMode {
		options.CookieSameSite = http.SameSiteLaxMode
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.Now().UTC() }
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	return &Service{
		store: store, assets: assets, secret: []byte(options.SigningSecret),
		grantTTL: options.GrantTTL, purgeDelay: options.PurgeDelay,
		cookieName: options.CookieName, cookieDomain: options.CookieDomain,
		cookieSecure: options.CookieSecure, cookieSameSite: options.CookieSameSite,
		now: options.Now, random: options.Random,
	}, nil
}

func (s *Service) timeNow() time.Time { return s.now().UTC() }

func publicFlipbook(fb *models.Flipbook) models.Flipbook {
	if fb == nil {
		return models.Flipbook{}
	}
	clone := *fb
	clone.PasswordHash = ""
	clone.AccessTokens = nil
	return clone
}

func publicRevision(revision *models.Revision) *models.Revision {
	if revision == nil {
		return nil
	}
	clone := *revision
	return &clone
}
