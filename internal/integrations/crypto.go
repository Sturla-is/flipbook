package integrations

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
)

const tokenCiphertextVersion = "v1:"

type TokenCipher struct {
	aead   cipher.AEAD
	random io.Reader
}

func NewTokenCipher(secret string) (*TokenCipher, error) {
	return newTokenCipher(secret, rand.Reader)
}

func newTokenCipher(secret string, random io.Reader) (*TokenCipher, error) {
	if len(secret) < 32 {
		return nil, integrationError(ErrNotConfigured, "", "encryption", "Token encryption requires a configured secret of at least 32 bytes.")
	}
	if random == nil {
		return nil, errors.New("token cipher random source is required")
	}
	key := sha256.Sum256([]byte("flipbook-integrations/token-encryption/v1\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &TokenCipher{aead: aead, random: random}, nil
}

func (c *TokenCipher) Seal(context, plaintext string) (string, error) {
	if c == nil {
		return "", integrationError(ErrNotConfigured, "", "encryption", "Token encryption is not configured. Set security.token_encryption_key before connecting a provider.")
	}
	if plaintext == "" {
		return "", integrationError(ErrInvalidInput, "", "encryption", "Cannot encrypt an empty integration secret.")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(c.random, nonce); err != nil {
		return "", err
	}
	ciphertext := c.aead.Seal(nil, nonce, []byte(plaintext), []byte(context))
	payload := append(nonce, ciphertext...)
	return tokenCiphertextVersion + base64.RawURLEncoding.EncodeToString(payload), nil
}

func (c *TokenCipher) Open(context, encoded string) (string, error) {
	if c == nil {
		return "", integrationError(ErrNotConfigured, "", "encryption", "Token encryption is not configured. Set security.token_encryption_key and reconnect the provider.")
	}
	if encoded == "" {
		return "", nil
	}
	if !strings.HasPrefix(encoded, tokenCiphertextVersion) {
		return "", integrationError(ErrNotConnected, "", "encryption", "The stored integration credential uses an unsupported format. Reconnect the provider.")
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encoded, tokenCiphertextVersion))
	if err != nil || len(payload) < c.aead.NonceSize()+c.aead.Overhead() {
		return "", integrationError(ErrNotConnected, "", "encryption", "The stored integration credential is invalid. Reconnect the provider.")
	}
	nonce := payload[:c.aead.NonceSize()]
	plaintext, err := c.aead.Open(nil, nonce, payload[c.aead.NonceSize():], []byte(context))
	if err != nil {
		return "", integrationError(ErrNotConnected, "", "encryption", "The stored integration credential cannot be decrypted. Check the encryption key or reconnect the provider.")
	}
	return string(plaintext), nil
}

func connectionTokenContext(connectionID, tokenType string) string {
	return "integration-connection/" + connectionID + "/" + tokenType
}

func webhookSecretContext(subscriptionID string) string {
	return "integration-webhook/" + subscriptionID + "/signing-secret"
}
