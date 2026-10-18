package publishing

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jonradoff/flipbook/internal/database"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type ErrorCode string

const (
	CodeInvalid            ErrorCode = "invalid_request"
	CodeNotFound           ErrorCode = "not_found"
	CodeExpired            ErrorCode = "publication_expired"
	CodeNotReady           ErrorCode = "not_ready"
	CodeUnauthorized       ErrorCode = "authentication_required"
	CodeForbidden          ErrorCode = "access_denied"
	CodePasswordRequired   ErrorCode = "password_required"
	CodeShareTokenRequired ErrorCode = "share_token_required"
	CodeInvalidGrant       ErrorCode = "invalid_grant"
	CodeLeadRequired       ErrorCode = "lead_required"
	CodePurchaseRequired   ErrorCode = "purchase_required"
	CodeEmbedDisabled      ErrorCode = "embed_disabled"
	CodeEmbedDomain        ErrorCode = "embed_domain_denied"
	CodeDownloadDisabled   ErrorCode = "download_disabled"
	CodeShareDisabled      ErrorCode = "share_disabled"
	CodeReviewRequired     ErrorCode = "review_required"
	CodeReviewState        ErrorCode = "invalid_review_state"
	CodeConflict           ErrorCode = "conflict"
	CodeInternal           ErrorCode = "internal_error"
)

type HTTPError struct {
	Status  int
	Code    ErrorCode
	Message string
	Err     error
}

func (e *HTTPError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *HTTPError) Unwrap() error { return e.Err }

func newHTTPError(status int, code ErrorCode, message string, cause error) *HTTPError {
	return &HTTPError{Status: status, Code: code, Message: message, Err: cause}
}

func invalid(message string) *HTTPError {
	return newHTTPError(http.StatusBadRequest, CodeInvalid, message, nil)
}

func forbidden(code ErrorCode, message string) *HTTPError {
	return newHTTPError(http.StatusForbidden, code, message, nil)
}

func unauthorized(code ErrorCode, message string) *HTTPError {
	return newHTTPError(http.StatusUnauthorized, code, message, nil)
}

func notFound() *HTTPError {
	return newHTTPError(http.StatusNotFound, CodeNotFound, "publication not found", nil)
}

func conflict(message string, cause error) *HTTPError {
	return newHTTPError(http.StatusConflict, CodeConflict, message, cause)
}

func internal(err error) error {
	if err == nil {
		return nil
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return err
	}
	return newHTTPError(http.StatusInternalServerError, CodeInternal, "publishing operation failed", err)
}

func repositoryError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return notFound()
	}
	if errors.Is(err, database.ErrConflict) {
		return conflict("publication conflict", err)
	}
	if errors.Is(err, database.ErrForbidden) {
		return forbidden(CodeForbidden, "insufficient workspace permissions")
	}
	return internal(err)
}

func HTTPStatus(err error) int {
	if err == nil {
		return http.StatusOK
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status
	}
	return http.StatusInternalServerError
}

func ErrorCodeOf(err error) ErrorCode {
	if err == nil {
		return ""
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Code
	}
	return CodeInternal
}

func PublicMessage(err error) string {
	if err == nil {
		return ""
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Message
	}
	return "publishing operation failed"
}

func decisionError(d Decision) error {
	code := d.Reason
	if code == "" {
		code = CodeForbidden
	}
	status := d.Status
	if status == 0 {
		status = http.StatusForbidden
	}
	messages := map[ErrorCode]string{
		CodeNotFound:           "publication not found",
		CodeExpired:            "publication has expired",
		CodeNotReady:           "publication is not ready",
		CodeUnauthorized:       "authentication required",
		CodeForbidden:          "access denied",
		CodePasswordRequired:   "publication password required",
		CodeShareTokenRequired: "valid share token required",
		CodeLeadRequired:       "lead submission required",
		CodePurchaseRequired:   "purchase required",
		CodeEmbedDisabled:      "embedding is disabled",
		CodeEmbedDomain:        "embedding is not allowed from this domain",
		CodeDownloadDisabled:   "download is disabled",
		CodeShareDisabled:      "sharing is disabled",
		CodeInvalid:            "invalid access request",
	}
	message := messages[code]
	if message == "" {
		message = "access denied"
	}
	return newHTTPError(status, code, message, nil)
}
