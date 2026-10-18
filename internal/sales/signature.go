package sales

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"
)

const DefaultWebhookTolerance = 5 * time.Minute

func VerifyWebhookSignature(payload []byte, header, secret string, now time.Time, tolerance time.Duration) error {
	if secret == "" {
		return ErrNotConfigured
	}
	if tolerance <= 0 {
		tolerance = DefaultWebhookTolerance
	}
	var timestamp int64
	var signatures [][]byte
	for _, part := range strings.Split(header, ",") {
		keyValue := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(keyValue) != 2 {
			continue
		}
		switch keyValue[0] {
		case "t":
			value, err := strconv.ParseInt(keyValue[1], 10, 64)
			if err != nil || value <= 0 {
				return ErrSignature
			}
			timestamp = value
		case "v1":
			value, err := hex.DecodeString(keyValue[1])
			if err != nil || len(value) != sha256.Size {
				return ErrSignature
			}
			signatures = append(signatures, value)
		}
	}
	if timestamp == 0 || len(signatures) == 0 {
		return ErrSignature
	}
	signedAt := time.Unix(timestamp, 0)
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if signedAt.Before(now.Add(-tolerance)) || signedAt.After(now.Add(tolerance)) {
		return ErrSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(payload)
	expected := mac.Sum(nil)
	for _, signature := range signatures {
		if hmac.Equal(signature, expected) {
			return nil
		}
	}
	return ErrSignature
}
