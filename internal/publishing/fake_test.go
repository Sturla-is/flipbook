package publishing

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type fakeStore struct {
	flipbooks        map[string]*models.Flipbook
	workspaces       map[string]*models.Workspace
	memberships      map[string]*models.Membership
	revisions        map[string]*models.Revision
	redirects        map[string]string
	entitled         bool
	entitlementErr   error
	entitlementCalls int

	activateFailures int
	activateErr      error
	gridErr          error
	assetPurgeErr    error
	calls            []string
	activationCalls  []string
	deletedGridIDs   []string
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		flipbooks: make(map[string]*models.Flipbook), workspaces: make(map[string]*models.Workspace),
		memberships: make(map[string]*models.Membership), revisions: make(map[string]*models.Revision),
		redirects: make(map[string]string),
	}
}

func publicationKey(workspaceID, id string) string    { return workspaceID + "/" + id }
func membershipKey(workspaceID, userID string) string { return workspaceID + "/" + userID }
func redirectKey(workspaceID, slug string) string     { return workspaceID + "/" + slug }

func cloneFlipbook(fb *models.Flipbook) *models.Flipbook {
	if fb == nil {
		return nil
	}
	clone := *fb
	clone.AccessTokens = append([]models.AccessToken(nil), fb.AccessTokens...)
	clone.Tags = append([]string(nil), fb.Tags...)
	clone.CollectionIDs = append([]string(nil), fb.CollectionIDs...)
	return &clone
}

func cloneRevision(revision *models.Revision) *models.Revision {
	if revision == nil {
		return nil
	}
	clone := *revision
	return &clone
}

func (f *fakeStore) GetFlipbookInWorkspace(_ context.Context, workspaceID, id string) (*models.Flipbook, error) {
	fb := f.flipbooks[publicationKey(workspaceID, id)]
	if fb == nil || fb.DeletedAt != nil {
		return nil, mongo.ErrNoDocuments
	}
	return cloneFlipbook(fb), nil
}

func (f *fakeStore) GetWorkspace(_ context.Context, workspaceID string) (*models.Workspace, error) {
	workspace := f.workspaces[workspaceID]
	if workspace == nil {
		return nil, mongo.ErrNoDocuments
	}
	clone := *workspace
	return &clone, nil
}

func (f *fakeStore) GetMembership(_ context.Context, workspaceID, userID string) (*models.Membership, error) {
	membership := f.memberships[membershipKey(workspaceID, userID)]
	if membership == nil {
		return nil, mongo.ErrNoDocuments
	}
	clone := *membership
	return &clone, nil
}

func (f *fakeStore) HasEntitlement(_ context.Context, _, _, _, _ string, _ time.Time) (bool, error) {
	f.entitlementCalls++
	return f.entitled, f.entitlementErr
}

func (f *fakeStore) GetFlipbookByAccessToken(_ context.Context, rawToken string, now time.Time) (*models.Flipbook, error) {
	for _, fb := range f.flipbooks {
		if fb.DeletedAt != nil || fb.ArchivedAt != nil {
			continue
		}
		if findActiveToken(fb, rawToken, now) != nil {
			return cloneFlipbook(fb), nil
		}
	}
	return nil, mongo.ErrNoDocuments
}

func (f *fakeStore) CreateFlipbookAccessToken(_ context.Context, workspaceID, flipbookID string, token *models.AccessToken) error {
	fb := f.flipbooks[publicationKey(workspaceID, flipbookID)]
	if fb == nil {
		return mongo.ErrNoDocuments
	}
	fb.AccessTokens = append(fb.AccessTokens, *token)
	return nil
}

func (f *fakeStore) RevokeFlipbookAccessToken(_ context.Context, workspaceID, flipbookID, tokenID string) error {
	fb := f.flipbooks[publicationKey(workspaceID, flipbookID)]
	if fb == nil {
		return mongo.ErrNoDocuments
	}
	now := time.Now().UTC()
	for i := range fb.AccessTokens {
		if fb.AccessTokens[i].ID == tokenID {
			fb.AccessTokens[i].RevokedAt = &now
			return nil
		}
	}
	return mongo.ErrNoDocuments
}

func (f *fakeStore) UpdateFlipbookMetadata(_ context.Context, fb *models.Flipbook) error {
	stored := f.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)]
	if stored == nil || stored.DeletedAt != nil {
		return mongo.ErrNoDocuments
	}
	state, status := stored.PublicationState, stored.Status
	publishedAt, unpublishedAt := stored.PublishedAt, stored.UnpublishedAt
	currentRevision, assetVersion := stored.CurrentRevisionID, stored.AssetVersion
	createdAt, convertedAt := stored.CreatedAt, stored.ConvertedAt
	archivedAt, archiveReason := stored.ArchivedAt, stored.ArchiveReason
	deletedAt, purgeAfter := stored.DeletedAt, stored.PurgeAfter
	gridID := stored.GridFSFileID
	*stored = *cloneFlipbook(fb)
	stored.PublicationState, stored.Status = state, status
	stored.PublishedAt, stored.UnpublishedAt = publishedAt, unpublishedAt
	stored.CurrentRevisionID, stored.AssetVersion = currentRevision, assetVersion
	stored.CreatedAt, stored.ConvertedAt = createdAt, convertedAt
	stored.ArchivedAt, stored.ArchiveReason = archivedAt, archiveReason
	stored.DeletedAt, stored.PurgeAfter = deletedAt, purgeAfter
	stored.GridFSFileID = gridID
	stored.IsPublic = fb.PublicationState == models.PublicationPublished &&
		(stored.Visibility == models.VisibilityPublic || stored.Visibility == models.VisibilityUnlisted)
	return nil
}

func (f *fakeStore) SetPublicationState(_ context.Context, workspaceID, id string, state models.PublicationState, visibility models.Visibility, now time.Time) error {
	fb := f.flipbooks[publicationKey(workspaceID, id)]
	if fb == nil || fb.DeletedAt != nil {
		return mongo.ErrNoDocuments
	}
	fb.PublicationState, fb.Visibility, fb.UpdatedAt = state, visibility, now
	fb.IsPublic = state == models.PublicationPublished &&
		(visibility == models.VisibilityPublic || visibility == models.VisibilityUnlisted)
	if state == models.PublicationPublished {
		fb.PublishedAt = &now
		fb.UnpublishedAt = nil
	} else {
		fb.UnpublishedAt = &now
	}
	return nil
}

func (f *fakeStore) ListDuePublications(_ context.Context, now time.Time) ([]*models.Flipbook, error) {
	var result []*models.Flipbook
	for _, fb := range f.flipbooks {
		if fb.DeletedAt != nil || fb.ArchivedAt != nil {
			continue
		}
		duePublish := fb.PublicationState == models.PublicationDraft && fb.PublishAt != nil && !fb.PublishAt.After(now)
		dueExpiry := fb.PublicationState == models.PublicationPublished && fb.ExpiresAt != nil && !fb.ExpiresAt.After(now)
		if duePublish || dueExpiry {
			result = append(result, cloneFlipbook(fb))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (f *fakeStore) ArchiveFlipbook(_ context.Context, workspaceID, id, reason string) error {
	fb := f.flipbooks[publicationKey(workspaceID, id)]
	if fb == nil || fb.DeletedAt != nil {
		return mongo.ErrNoDocuments
	}
	now := time.Now().UTC()
	fb.ArchivedAt, fb.ArchiveReason, fb.IsPublic = &now, reason, false
	return nil
}

func (f *fakeStore) RestoreFlipbook(_ context.Context, workspaceID, id string) error {
	fb := f.flipbooks[publicationKey(workspaceID, id)]
	if fb == nil {
		return mongo.ErrNoDocuments
	}
	fb.ArchivedAt, fb.ArchiveReason, fb.DeletedAt, fb.PurgeAfter = nil, "", nil, nil
	fb.IsPublic = fb.PublicationState == models.PublicationPublished &&
		(fb.Visibility == models.VisibilityPublic || fb.Visibility == models.VisibilityUnlisted)
	return nil
}

func (f *fakeStore) SoftDeleteFlipbook(_ context.Context, workspaceID, id string, delay time.Duration) error {
	fb := f.flipbooks[publicationKey(workspaceID, id)]
	if fb == nil || fb.DeletedAt != nil {
		return mongo.ErrNoDocuments
	}
	now := time.Now().UTC()
	purge := now.Add(delay)
	fb.DeletedAt, fb.PurgeAfter, fb.IsPublic = &now, &purge, false
	return nil
}

func (f *fakeStore) ListFlipbooksPendingPurge(_ context.Context, now time.Time) ([]*models.Flipbook, error) {
	var result []*models.Flipbook
	for _, fb := range f.flipbooks {
		if fb.DeletedAt != nil && fb.PurgeAfter != nil && !fb.PurgeAfter.After(now) {
			result = append(result, cloneFlipbook(fb))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (f *fakeStore) PurgeFlipbook(_ context.Context, workspaceID, id string) error {
	key := publicationKey(workspaceID, id)
	if f.flipbooks[key] == nil {
		return mongo.ErrNoDocuments
	}
	delete(f.flipbooks, key)
	for revisionID, revision := range f.revisions {
		if revision.FlipbookID == id && revision.WorkspaceID == workspaceID {
			delete(f.revisions, revisionID)
		}
	}
	f.calls = append(f.calls, "purge:"+id)
	return nil
}

func (f *fakeStore) DeleteFromGridFS(_ context.Context, id string) error {
	if f.gridErr != nil {
		return f.gridErr
	}
	f.deletedGridIDs = append(f.deletedGridIDs, id)
	f.calls = append(f.calls, "grid:"+id)
	return nil
}

func (f *fakeStore) ChangeFlipbookSlug(_ context.Context, workspaceID, flipbookID, newSlug string) error {
	fb := f.flipbooks[publicationKey(workspaceID, flipbookID)]
	if fb == nil {
		return mongo.ErrNoDocuments
	}
	newSlug = models.NormalizeSlug(newSlug)
	for _, other := range f.flipbooks {
		if other.ID != fb.ID && other.WorkspaceID == workspaceID && other.Slug == newSlug && other.DeletedAt == nil {
			return errors.New("resource conflict")
		}
	}
	if _, exists := f.redirects[redirectKey(workspaceID, newSlug)]; exists {
		return errors.New("resource conflict")
	}
	if fb.Slug == newSlug {
		return nil
	}
	f.redirects[redirectKey(workspaceID, fb.Slug)] = fb.ID
	fb.Slug = newSlug
	return nil
}

func (f *fakeStore) GetFlipbookBySlugOrRedirect(_ context.Context, workspaceID, slug string) (*models.Flipbook, bool, error) {
	for _, fb := range f.flipbooks {
		if fb.WorkspaceID == workspaceID && fb.Slug == slug && fb.DeletedAt == nil {
			return cloneFlipbook(fb), false, nil
		}
	}
	id, ok := f.redirects[redirectKey(workspaceID, slug)]
	if !ok {
		return nil, false, mongo.ErrNoDocuments
	}
	fb, err := f.GetFlipbookInWorkspace(context.Background(), workspaceID, id)
	return fb, true, err
}

func (f *fakeStore) GetRevision(_ context.Context, workspaceID, revisionID string) (*models.Revision, error) {
	revision := f.revisions[revisionID]
	if revision == nil || revision.WorkspaceID != workspaceID || revision.DeletedAt != nil {
		return nil, mongo.ErrNoDocuments
	}
	return cloneRevision(revision), nil
}

func (f *fakeStore) GetCurrentRevision(ctx context.Context, workspaceID, flipbookID string) (*models.Revision, error) {
	fb, err := f.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, err
	}
	return f.GetRevision(ctx, workspaceID, fb.CurrentRevisionID)
}

func (f *fakeStore) ListRevisions(_ context.Context, workspaceID, flipbookID string) ([]*models.Revision, error) {
	var result []*models.Revision
	for _, revision := range f.revisions {
		if revision.WorkspaceID == workspaceID && revision.FlipbookID == flipbookID && revision.DeletedAt == nil {
			result = append(result, cloneRevision(revision))
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Number > result[j].Number })
	return result, nil
}

func (f *fakeStore) ActivateRevision(_ context.Context, workspaceID, flipbookID, revisionID string) error {
	f.activationCalls = append(f.activationCalls, revisionID)
	if f.activateFailures > 0 {
		f.activateFailures--
		if f.activateErr != nil {
			return f.activateErr
		}
		return errors.New("activation failed")
	}
	fb := f.flipbooks[publicationKey(workspaceID, flipbookID)]
	target := f.revisions[revisionID]
	if fb == nil || target == nil || target.FlipbookID != flipbookID {
		return mongo.ErrNoDocuments
	}
	for _, revision := range f.revisions {
		if revision.FlipbookID == flipbookID && revision.Status == models.RevisionActive {
			revision.Status = models.RevisionSuperseded
		}
	}
	target.Status = models.RevisionActive
	fb.CurrentRevisionID, fb.AssetVersion, fb.PageCount, fb.Status = target.ID, target.AssetVersion, target.PageCount, models.StatusReady
	return nil
}

type fakeAssets struct {
	active          map[string]int
	activationCalls []string
	deleteCalls     []string
	activateErr     error
	deleteErr       error
}

func newFakeAssets() *fakeAssets { return &fakeAssets{active: make(map[string]int)} }

func (f *fakeAssets) RevisionAssetURL(id string, revision int, name string) string {
	if id == "" || revision < 1 || strings.Contains(name, "..") {
		return ""
	}
	return fmt.Sprintf("/data/flipbooks/%s/revisions/%d/%s", id, revision, name)
}

func (f *fakeAssets) ActivateRevision(id string, revision int) error {
	f.activationCalls = append(f.activationCalls, fmt.Sprintf("%s:%d", id, revision))
	if f.activateErr != nil {
		return f.activateErr
	}
	f.active[id] = revision
	return nil
}

func (f *fakeAssets) DeleteFlipbookContext(_ context.Context, id string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCalls = append(f.deleteCalls, id)
	return nil
}

func testService(t *testing.T, store *fakeStore, assets *fakeAssets, now time.Time) *Service {
	t.Helper()
	service, err := NewService(store, assets, Options{
		SigningSecret: "0123456789abcdef0123456789abcdef",
		Now:           func() time.Time { return now },
		Random:        strings.NewReader(strings.Repeat("random-test-data", 32)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func testPublication(now time.Time) (*models.Flipbook, *models.Revision) {
	validated := now.Add(-time.Hour)
	fb := &models.Flipbook{
		ID: "book", WorkspaceID: "workspace", OwnerID: "owner", Title: "Report", Slug: "report",
		Status: models.StatusReady, PublicationState: models.PublicationPublished,
		Visibility: models.VisibilityPublic, ReviewState: models.ReviewNotRequired,
		PageCount: 5, CurrentRevisionID: "revision-1", AssetVersion: 1,
		Download:  models.DownloadSettings{Enabled: true, AllowOriginal: true, AllowPDF: true, AllowPrint: true},
		Embed:     models.EmbedSettings{Enabled: true, ShowDownload: true, ShowShare: true},
		Share:     models.ShareSettings{Enabled: true, Native: true, Email: true, Facebook: true, LinkedIn: true, X: true},
		CreatedAt: now.Add(-24 * time.Hour), UpdatedAt: now.Add(-time.Hour),
	}
	fb.IsPublic = true
	revision := &models.Revision{
		ID: "revision-1", WorkspaceID: fb.WorkspaceID, FlipbookID: fb.ID,
		Number: 1, AssetVersion: 1, PageCount: 5, Status: models.RevisionActive,
		ValidatedAt: &validated, CreatedAt: validated, UpdatedAt: validated,
	}
	return fb, revision
}

func installTestPublication(store *fakeStore, assets *fakeAssets, now time.Time) (*models.Flipbook, *models.Revision) {
	fb, revision := testPublication(now)
	store.flipbooks[publicationKey(fb.WorkspaceID, fb.ID)] = fb
	store.revisions[revision.ID] = revision
	store.workspaces[fb.WorkspaceID] = &models.Workspace{ID: fb.WorkspaceID, OwnerID: fb.OwnerID}
	assets.active[fb.ID] = revision.Number
	return fb, revision
}
