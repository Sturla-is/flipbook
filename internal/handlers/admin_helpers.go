package handlers

import (
	"fmt"
	"net/mail"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type adminInputError struct {
	message string
}

func (e *adminInputError) Error() string { return e.message }

func adminInputf(format string, arguments ...any) error {
	return &adminInputError{message: fmt.Sprintf(format, arguments...)}
}

func adminFormBool(r formValues, name string) bool {
	value := strings.ToLower(strings.TrimSpace(r.FormValue(name)))
	return value == "on" || value == "true" || value == "1" || value == "yes"
}

type formValues interface {
	FormValue(string) string
}

func splitAdminValues(value string) []string {
	return uniqueAdminStrings(strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}))
}

func uniqueAdminStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func cloneAdminStrings(values []string) []string {
	return append([]string(nil), values...)
}

func normalizedAdminTags(value string) ([]string, error) {
	tags := splitAdminValues(value)
	if len(tags) > 50 {
		return nil, adminInputf("Use at most 50 tags.")
	}
	for index, tag := range tags {
		if len(tag) > 64 {
			return nil, adminInputf("Each tag must be at most 64 characters.")
		}
		tags[index] = tag
	}
	return tags, nil
}

func parseAdminInt(value, field string, minimum, maximum int) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return minimum, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, adminInputf("%s must be between %d and %d.", field, minimum, maximum)
	}
	return parsed, nil
}

func parseAdminTime(value, field string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed, nil
		}
	}
	return nil, adminInputf("%s must be a valid date and time in UTC.", field)
}

func parseAdminPriceCents(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, nil
	}
	if strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, adminInputf("Price must be a non-negative amount.")
	}
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, adminInputf("Price must use a numeric amount such as 12.50.")
	}
	for _, part := range parts {
		for _, character := range part {
			if character < '0' || character > '9' {
				return 0, adminInputf("Price must use a numeric amount such as 12.50.")
			}
		}
	}
	if len(parts) == 2 && (len(parts[1]) < 1 || len(parts[1]) > 2) {
		return 0, adminInputf("Price can have at most two decimal places.")
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || whole > 10000000000 {
		return 0, adminInputf("Price is too large.")
	}
	cents := whole * 100
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
		parsed, _ := strconv.ParseInt(fraction, 10, 64)
		cents += parsed
	}
	return cents, nil
}

var (
	adminLanguagePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)
	adminColorPattern    = regexp.MustCompile(`^#[0-9A-Fa-f]{3}(?:[0-9A-Fa-f]{3})?$`)
	adminCurrencyPattern = regexp.MustCompile(`^[A-Za-z]{3}$`)
	adminDomainPattern   = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9.-]+(:[0-9]{1,5})?$`)
)

func validateAdminLanguage(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "en", nil
	}
	if len(value) > 35 || !adminLanguagePattern.MatchString(value) {
		return "", adminInputf("Language must be a valid BCP 47 tag such as en or pt-BR.")
	}
	return value, nil
}

func validateAdminColor(value, field string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if !adminColorPattern.MatchString(value) {
		return "", adminInputf("%s must be a three- or six-digit hex color.", field)
	}
	return strings.ToUpper(value), nil
}

func validateAdminURL(value, field string, allowRelative bool) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") {
		return "", adminInputf("%s is not a valid URL.", field)
	}
	if allowRelative && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") {
		parsed, err := url.Parse(value)
		if err == nil && parsed.Host == "" {
			return value, nil
		}
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" || parsed.User != nil {
		return "", adminInputf("%s must be an HTTP or HTTPS URL.", field)
	}
	return parsed.String(), nil
}

func validateAdminDomains(value string) ([]string, error) {
	domains := splitAdminValues(value)
	if len(domains) > 100 {
		return nil, adminInputf("Use at most 100 allowed embed domains.")
	}
	for index, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if strings.Contains(domain, "://") {
			parsed, err := url.Parse(domain)
			if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
				return nil, adminInputf("Allowed embed domain %q is invalid.", domain)
			}
			domain = parsed.Scheme + "://" + parsed.Host
		} else if !adminDomainPattern.MatchString(domain) || strings.Contains(domain, "..") {
			return nil, adminInputf("Allowed embed domain %q is invalid.", domain)
		}
		domains[index] = domain
	}
	return uniqueAdminStrings(domains), nil
}

func validateAdminEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))
	address, err := mail.ParseAddress(value)
	if err != nil || address.Address != value || len(value) > 320 {
		return "", adminInputf("Enter a valid email address.")
	}
	return value, nil
}

func safeAdminReturn(value, fallback string) string {
	if fallback == "" {
		fallback = "/admin"
	}
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\\\r\n\x00") || !strings.HasPrefix(value, "/admin") || strings.HasPrefix(value, "//") {
		return fallback
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return fallback
	}
	return value
}

func joinAdminValues(values []string) string {
	return strings.Join(values, ", ")
}
