package auth

import (
	"encoding/base64"
	"io"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

func randomToken(random io.Reader, size int) (string, error) {
	buffer := make([]byte, size)
	if _, err := io.ReadFull(random, buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func validatePassword(password string) error {
	length := len([]byte(password))
	if length < 12 || length > 72 || strings.TrimSpace(password) == "" {
		return ErrInvalidPassword
	}
	return nil
}

func hashPassword(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	return string(hash), err
}

func passwordMatches(hash, password string) bool {
	if hash == "" || len([]byte(password)) > 72 {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
