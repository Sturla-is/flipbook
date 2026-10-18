package engagement

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

func randomURLToken(reader io.Reader, size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(reader, buffer); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func (s *Service) unsubscribeToken(workspaceID, normalizedEmail string) string {
	mac := hmac.New(sha256.New, s.signingSecret)
	_, _ = mac.Write([]byte("subscriber-unsubscribe\x00"))
	_, _ = mac.Write([]byte(workspaceID))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(normalizedEmail))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func webhookSecretContext(subscriptionID string) string {
	return "integration-webhook/" + subscriptionID + "/signing-secret"
}

func (s *Service) sealWebhookSecret(subscriptionID, plaintext string) (string, error) {
	block, err := aes.NewCipher(webhookEncryptionKey(s.encryptionKey))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return "", fmt.Errorf("generate webhook nonce: %w", err)
	}
	ciphertext := gcm.Seal(nil, nonce, []byte(plaintext), []byte(webhookSecretContext(subscriptionID)))
	payload := append(nonce, ciphertext...)
	return "v1:" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (s *Service) openWebhookSecret(subscriptionID, encoded string) (string, error) {
	if !strings.HasPrefix(encoded, "v1:") {
		return "", errors.New("webhook secret has an unsupported format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, "v1:"))
	if err != nil {
		return "", errors.New("webhook secret ciphertext is invalid")
	}
	block, err := aes.NewCipher(webhookEncryptionKey(s.encryptionKey))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) <= gcm.NonceSize() {
		return "", errors.New("webhook secret ciphertext is invalid")
	}
	nonce, ciphertext := payload[:gcm.NonceSize()], payload[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, []byte(webhookSecretContext(subscriptionID)))
	if err != nil {
		return "", errors.New("webhook secret could not be decrypted")
	}
	return string(plaintext), nil
}

func webhookEncryptionKey(value []byte) []byte {
	digest := sha256.Sum256([]byte("flipbook-integrations/token-encryption/v1\x00" + string(value)))
	return digest[:]
}
