package publishing

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"

	"github.com/jonradoff/flipbook/internal/models"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/crypto/bcrypt"
)

type evaluation struct {
	flipbook   *models.Flipbook
	principal  Principal
	evidence   AccessEvidence
	shareToken *models.AccessToken
	resource   Resource
	decision   Decision
	revision   *models.Revision
	assetName  string
	assetURL   string
}

func (s *Service) AuthorizeViewer(ctx context.Context, workspaceID, flipbookID string, request AccessRequest) (*Authorization, error) {
	return s.authorize(ctx, workspaceID, flipbookID, ResourceViewer, request)
}

func (s *Service) AuthorizeEmbed(ctx context.Context, workspaceID, flipbookID string, request AccessRequest) (*Authorization, error) {
	request.ViaEmbed = true
	return s.authorize(ctx, workspaceID, flipbookID, ResourceEmbed, request)
}

func (s *Service) AuthorizeStatus(ctx context.Context, workspaceID, flipbookID string, request AccessRequest) (*Authorization, error) {
	return s.authorize(ctx, workspaceID, flipbookID, ResourceStatus, request)
}

func (s *Service) AuthorizeDownload(ctx context.Context, workspaceID, flipbookID string, kind DownloadKind, request AccessRequest) (*Authorization, error) {
	var resource Resource
	switch kind {
	case DownloadOriginal:
		resource = ResourceDownloadOriginal
	case DownloadPDF:
		resource = ResourceDownloadPDF
	case DownloadPrint:
		resource = ResourcePrint
	default:
		return nil, invalid("unsupported download kind")
	}
	return s.authorize(ctx, workspaceID, flipbookID, resource, request)
}

func (s *Service) AuthorizeShare(ctx context.Context, workspaceID, flipbookID string, channel ShareChannel, request AccessRequest) (*Authorization, error) {
	eval, err := s.evaluate(ctx, workspaceID, flipbookID, ResourceShare, request, channel)
	if err != nil {
		return nil, err
	}
	return authorizationFromEvaluation(eval), nil
}

func (s *Service) AuthorizeLinks(ctx context.Context, workspaceID, flipbookID string, request AccessRequest) (*Authorization, error) {
	return s.AuthorizeAsset(ctx, workspaceID, flipbookID, AssetLinks, "", request)
}

func (s *Service) AuthorizeAsset(ctx context.Context, workspaceID, flipbookID string, kind AssetKind, name string, request AccessRequest) (*Authorization, error) {
	resource, err := resourceForAsset(kind)
	if err != nil {
		return nil, err
	}
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	revision, err := s.resolveAssetRevision(ctx, fb, request.Revision)
	if err != nil {
		return nil, err
	}
	request.Revision = revision.Number
	name, direct, err := assetName(kind, name, request.Page)
	if err != nil {
		return nil, err
	}
	eval, err := s.evaluateFlipbook(ctx, fb, resource, request, ShareAny, revision.PageCount)
	if err != nil {
		return nil, err
	}
	eval.revision = revision
	eval.assetName = name
	if direct && name != "" && (eval.decision.FullAccess || kind == AssetPage || kind == AssetThumbnail || kind == AssetRendition) {
		assetURL := s.assets.RevisionAssetURL(fb.ID, revision.Number, name)
		if assetURL == "" {
			return nil, invalid("invalid revision asset path")
		}
		if request.Grant != "" {
			assetURL = urlWithGrant(assetURL, request.Grant)
		}
		eval.assetURL = assetURL
	}
	return authorizationFromEvaluation(eval), nil
}

func (s *Service) ResolveViewerSlug(ctx context.Context, workspaceID, slug string, request AccessRequest) (*Authorization, bool, error) {
	fb, redirected, err := s.store.GetFlipbookBySlugOrRedirect(ctx, workspaceID, models.NormalizeSlug(slug))
	if err != nil {
		return nil, false, repositoryError(err)
	}
	auth, err := s.authorizeFlipbook(ctx, fb, ResourceViewer, request)
	return auth, redirected, err
}

func (s *Service) ResolveShareToken(ctx context.Context, rawToken string, request AccessRequest) (*Authorization, error) {
	if rawToken == "" {
		return nil, unauthorized(CodeShareTokenRequired, "valid share token required")
	}
	fb, err := s.store.GetFlipbookByAccessToken(ctx, rawToken, s.timeNow())
	if err != nil {
		return nil, repositoryError(err)
	}
	request.ShareToken = rawToken
	return s.authorizeFlipbook(ctx, fb, ResourceViewer, request)
}

func (s *Service) authorize(ctx context.Context, workspaceID, flipbookID string, resource Resource, request AccessRequest) (*Authorization, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	return s.authorizeFlipbook(ctx, fb, resource, request)
}

func (s *Service) authorizeFlipbook(ctx context.Context, fb *models.Flipbook, resource Resource, request AccessRequest) (*Authorization, error) {
	eval, err := s.evaluateFlipbook(ctx, fb, resource, request, ShareAny)
	if err != nil {
		return nil, err
	}
	return authorizationFromEvaluation(eval), nil
}

func (s *Service) evaluate(ctx context.Context, workspaceID, flipbookID string, resource Resource, request AccessRequest, channel ShareChannel) (*evaluation, error) {
	fb, err := s.store.GetFlipbookInWorkspace(ctx, workspaceID, flipbookID)
	if err != nil {
		return nil, repositoryError(err)
	}
	return s.evaluateFlipbook(ctx, fb, resource, request, channel)
}

func (s *Service) evaluateFlipbook(ctx context.Context, fb *models.Flipbook, resource Resource, request AccessRequest, channel ShareChannel, pageCounts ...int) (*evaluation, error) {
	principal, err := s.resolvePrincipal(ctx, fb, request.Reader)
	if err != nil {
		return nil, err
	}
	evidence, shareToken, err := s.resolveEvidence(ctx, fb, request, principal)
	if err != nil {
		return nil, err
	}
	pageCount := fb.PageCount
	if len(pageCounts) > 0 {
		pageCount = pageCounts[0]
	}
	decision := EvaluateAccess(PolicyInput{
		Flipbook: *fb, Principal: principal, Evidence: evidence,
		Resource: resource, Page: request.Page, PageCount: pageCount,
		Revision: request.Revision, Now: s.timeNow(), EmbedHost: request.EmbedHost,
		ViaEmbed: request.ViaEmbed, ShareChannel: channel,
	})
	if !decision.Allowed {
		return nil, decisionError(decision)
	}
	return &evaluation{
		flipbook: fb, principal: principal, evidence: evidence,
		shareToken: shareToken, resource: resource, decision: decision,
	}, nil
}

func authorizationFromEvaluation(eval *evaluation) *Authorization {
	return &Authorization{
		Flipbook: publicFlipbook(eval.flipbook), Revision: publicRevision(eval.revision),
		Resource: eval.resource, Decision: eval.decision,
		AssetName: eval.assetName, AssetURL: eval.assetURL,
	}
}

func (s *Service) resolvePrincipal(ctx context.Context, fb *models.Flipbook, reader Reader) (Principal, error) {
	principal := Principal{UserID: reader.UserID, WorkspaceID: fb.WorkspaceID}
	if reader.UserID == "" {
		return principal, nil
	}
	if reader.UserID == fb.OwnerID {
		principal.Member = true
		principal.Role = models.RoleOwner
		return principal, nil
	}
	membership, err := s.store.GetMembership(ctx, fb.WorkspaceID, reader.UserID)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return principal, nil
	}
	if err != nil {
		return principal, internal(err)
	}
	if membership.WorkspaceID != fb.WorkspaceID || membership.UserID != reader.UserID {
		return principal, nil
	}
	principal.Member = true
	principal.Role = membership.Role
	return principal, nil
}

func (s *Service) resolveEvidence(ctx context.Context, fb *models.Flipbook, request AccessRequest, principal Principal) (AccessEvidence, *models.AccessToken, error) {
	var evidence AccessEvidence
	var activeShare *models.AccessToken
	now := s.timeNow()
	if request.Grant != "" {
		grant, err := s.parseGrant(request.Grant)
		if err != nil {
			return evidence, nil, err
		}
		if grant.WorkspaceID == fb.WorkspaceID && grant.FlipbookID == fb.ID {
			if fb.PasswordHash != "" && grant.PasswordProof == credentialFingerprint(fb.PasswordHash) {
				evidence.Password = true
			}
			if grant.ShareTokenID != "" {
				if token := findGrantToken(fb, grant, now); token != nil {
					evidence.ShareToken = true
					activeShare = token
				}
			}
			evidence.LeadCaptured = grant.LeadCaptured
			evidence.LeadDismissed = grant.LeadDismissed
		}
	}
	if request.Password != "" && fb.PasswordHash != "" &&
		bcrypt.CompareHashAndPassword([]byte(fb.PasswordHash), []byte(request.Password)) == nil {
		evidence.Password = true
	}
	if request.ShareToken != "" {
		if token := findActiveToken(fb, request.ShareToken, now); token != nil {
			evidence.ShareToken = true
			activeShare = token
		}
	}
	staff := principal.Member && principal.Role.CanEdit()
	if fb.Sales.Enabled && !staff && (request.Reader.UserID != "" || request.Reader.EmailHash != "") {
		entitled, err := s.store.HasEntitlement(ctx, fb.WorkspaceID, fb.ID, request.Reader.UserID, request.Reader.EmailHash, now)
		if err != nil {
			return evidence, nil, internal(err)
		}
		evidence.Entitled = entitled
	}
	return evidence, activeShare, nil
}

func (s *Service) resolveAssetRevision(ctx context.Context, fb *models.Flipbook, number int) (*models.Revision, error) {
	current, err := s.store.GetCurrentRevision(ctx, fb.WorkspaceID, fb.ID)
	if err != nil {
		return nil, repositoryError(err)
	}
	if number == 0 || number == current.Number {
		return current, nil
	}
	if number < 1 {
		return nil, invalid("asset revision must be positive")
	}
	revisions, err := s.store.ListRevisions(ctx, fb.WorkspaceID, fb.ID)
	if err != nil {
		return nil, repositoryError(err)
	}
	for _, revision := range revisions {
		if revision.Number == number && (revision.Status == models.RevisionActive || revision.Status == models.RevisionSuperseded) {
			return revision, nil
		}
	}
	return nil, notFound()
}

func resourceForAsset(kind AssetKind) (Resource, error) {
	switch kind {
	case AssetPage:
		return ResourcePage, nil
	case AssetThumbnail:
		return ResourceThumbnail, nil
	case AssetRendition:
		return ResourceRendition, nil
	case AssetText:
		return ResourceText, nil
	case AssetManifest:
		return ResourceManifest, nil
	case AssetLinks:
		return ResourceLinks, nil
	default:
		return "", invalid("unsupported asset kind")
	}
}

func assetName(kind AssetKind, name string, pageNumber int) (string, bool, error) {
	requirePage := kind == AssetPage || kind == AssetThumbnail || kind == AssetRendition || kind == AssetText || kind == AssetLinks
	if requirePage && pageNumber < 1 {
		return "", false, invalid("asset page must be positive")
	}
	if pageNumber < 0 {
		return "", false, invalid("asset page must not be negative")
	}
	if name == "" {
		switch kind {
		case AssetPage:
			name = fmt.Sprintf("pages/page-%d.png", pageNumber)
		case AssetThumbnail:
			name = fmt.Sprintf("thumbs/page-%d.png", pageNumber)
		case AssetText:
			name = "text.json"
		case AssetManifest:
			name = "manifest.json"
		}
	}
	if kind == AssetLinks {
		return "", false, nil
	}
	if err := validateAssetName(kind, name); err != nil {
		return "", false, err
	}
	direct := true
	if (kind == AssetText || kind == AssetManifest) && pageNumber > 0 {
		direct = false
	}
	return name, direct, nil
}

func validateAssetName(kind AssetKind, name string) error {
	if name == "" || strings.Contains(name, "\\") || strings.ContainsAny(name, "?#") ||
		strings.HasPrefix(name, "/") || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") {
		return invalid("invalid asset path")
	}
	validImage := strings.HasSuffix(name, ".png") || strings.HasSuffix(name, ".jpg") ||
		strings.HasSuffix(name, ".jpeg") || strings.HasSuffix(name, ".webp")
	switch kind {
	case AssetPage:
		if !strings.HasPrefix(name, "pages/") || !validImage {
			return invalid("page asset must be an image in pages/")
		}
	case AssetThumbnail:
		if !strings.HasPrefix(name, "thumbs/") || !validImage {
			return invalid("thumbnail asset must be an image in thumbs/")
		}
	case AssetRendition:
		if !strings.HasPrefix(name, "renditions/") || !validImage {
			return invalid("rendition must be an image in renditions/")
		}
	case AssetText:
		if name != "text.json" {
			return invalid("text asset must be text.json")
		}
	case AssetManifest:
		if name != "manifest.json" {
			return invalid("manifest asset must be manifest.json")
		}
	}
	return nil
}

func urlWithGrant(rawURL, grant string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	query.Set("access_grant", grant)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
