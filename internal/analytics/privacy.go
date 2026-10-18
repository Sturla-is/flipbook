package analytics

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/metavert-io/flipbook/internal/models"
)

var countryPattern = regexp.MustCompile(`^[A-Za-z]{2}$`)

// CollectionDecision explains why a batch was or was not collected. Filtered
// batches receive a successful response so readers are not repeatedly retried.
type CollectionDecision struct {
	Collect bool
	Reason  string
}

func DecideCollection(options Options, context RequestContext) CollectionDecision {
	options = options.normalized()
	switch {
	case !options.Enabled:
		return CollectionDecision{Reason: "disabled"}
	case context.OptedOut, context.DoNotTrack:
		return CollectionDecision{Reason: "opted_out"}
	case models.IsObviousBot(context.UserAgent):
		return CollectionDecision{Reason: "bot"}
	case options.IgnoreAdminPreviews && context.AdminPreview:
		return CollectionDecision{Reason: "admin_preview"}
	default:
		return CollectionDecision{Collect: true}
	}
}

func IdentifiersAllowed(options Options, context RequestContext) bool {
	options = options.normalized()
	if context.OptedOut || context.DoNotTrack || options.ConsentPolicy == ConsentDisabled {
		return false
	}
	return options.ConsentPolicy == ConsentAnonymous || context.Consented
}

func ConsentState(r *http.Request) (state string, consented, optedOut bool) {
	cookie, err := r.Cookie(ConsentCookieName)
	if err != nil {
		return "unset", false, false
	}
	switch cookie.Value {
	case consentGranted:
		return consentGranted, true, false
	case consentDenied:
		return consentDenied, false, true
	default:
		return "unset", false, false
	}
}

func RequestCountry(r *http.Request, enabled bool) string {
	if !enabled || r == nil {
		return ""
	}
	for _, header := range []string{"CF-IPCountry", "X-Country-Code", "X-Appengine-Country"} {
		value := strings.ToUpper(strings.TrimSpace(r.Header.Get(header)))
		if countryPattern.MatchString(value) && value != "XX" && value != "ZZ" && value != "T1" {
			return value
		}
	}
	return ""
}

func IdentifierHash(pepper, workspaceID, kind, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(pepper))
	_, _ = mac.Write([]byte("flipbook-analytics-v1\x00"))
	_, _ = mac.Write([]byte(workspaceID))
	_, _ = mac.Write([]byte("\x00"))
	_, _ = mac.Write([]byte(kind))
	_, _ = mac.Write([]byte("\x00"))
	_, _ = mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}

func ValidIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 200 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

func Cookie(name, value string, expires time.Time, secure bool) *http.Cookie {
	cookie := &http.Cookie{
		Name: name, Value: value, Path: "/", HttpOnly: true,
		Secure: secure, SameSite: http.SameSiteLaxMode,
	}
	if !expires.IsZero() {
		cookie.Expires = expires
		cookie.MaxAge = max(1, int(time.Until(expires).Seconds()))
	}
	return cookie
}

func ClearCookie(name string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name: name, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	}
}
