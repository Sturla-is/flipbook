package sales

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/metavert-io/flipbook/internal/database"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var (
	ErrNotConfigured     = errors.New("digital sales are not configured")
	ErrInvalid           = errors.New("invalid sales request")
	ErrNotFound          = errors.New("sales resource not found")
	ErrConflict          = errors.New("sales resource conflict")
	ErrProvider          = errors.New("payment provider request failed")
	ErrSignature         = errors.New("invalid webhook signature")
	ErrPaymentIncomplete = errors.New("payment is not complete")
	ErrRefundPending     = errors.New("refund is pending provider confirmation")
)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func normalizeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if errors.Is(err, database.ErrConflict) {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

func HTTPStatus(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrSignature):
		return http.StatusBadRequest
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrConflict), errors.Is(err, ErrPaymentIncomplete):
		return http.StatusConflict
	case errors.Is(err, ErrRefundPending):
		return http.StatusAccepted
	case errors.Is(err, ErrNotConfigured):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrProvider):
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

func PublicMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrSignature):
		return "invalid webhook signature"
	case errors.Is(err, ErrNotConfigured):
		return "digital sales are not configured"
	case errors.Is(err, ErrProvider):
		return "the payment provider could not complete the request"
	case errors.Is(err, ErrPaymentIncomplete):
		return "payment has not completed"
	case errors.Is(err, ErrRefundPending):
		return "refund started and is awaiting provider confirmation"
	case errors.Is(err, ErrNotFound):
		return "purchase or publication not found"
	case errors.Is(err, ErrConflict):
		return "the purchase state changed; refresh and try again"
	case errors.Is(err, ErrInvalid):
		return err.Error()
	default:
		return "the sales operation could not be completed"
	}
}
