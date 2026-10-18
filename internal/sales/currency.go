package sales

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var currencyPattern = regexp.MustCompile(`^[A-Za-z]{3}$`)

var zeroDecimalCurrencies = map[string]bool{
	"BIF": true, "CLP": true, "DJF": true, "GNF": true, "JPY": true,
	"KMF": true, "KRW": true, "MGA": true, "PYG": true, "RWF": true,
	"UGX": true, "VND": true, "VUV": true, "XAF": true, "XOF": true, "XPF": true,
}

func NormalizeCurrency(value string) (string, error) {
	currency := strings.ToUpper(strings.TrimSpace(value))
	if currency == "" {
		currency = "USD"
	}
	if !currencyPattern.MatchString(currency) {
		return "", invalid("currency must be a three-letter ISO code")
	}
	return currency, nil
}

func currencyDigits(currency string) int {
	if zeroDecimalCurrencies[strings.ToUpper(currency)] {
		return 0
	}
	return 2
}

func FormatAmount(amount int64, currency string) string {
	digits := currencyDigits(currency)
	negative := amount < 0
	if negative {
		amount = -amount
	}
	divisor := int64(1)
	for i := 0; i < digits; i++ {
		divisor *= 10
	}
	whole, fraction := amount/divisor, amount%divisor
	result := strconv.FormatInt(whole, 10)
	if digits > 0 {
		result += fmt.Sprintf(".%0*d", digits, fraction)
	}
	if negative {
		result = "-" + result
	}
	return result
}

func ParseAmount(value, currency string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") || strings.HasPrefix(value, "+") {
		return 0, invalid("price must be a non-negative amount")
	}
	digits := currencyDigits(currency)
	parts := strings.Split(value, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, invalid("price has an invalid decimal format")
	}
	if digits == 0 && len(parts) == 2 {
		return 0, invalid("%s prices must use whole units", strings.ToUpper(currency))
	}
	if len(parts) == 2 && (len(parts[1]) > digits || (len(parts[1]) > 0 && !allDigits(parts[1]))) {
		return 0, invalid("price has too many decimal places")
	}
	if !allDigits(parts[0]) {
		return 0, invalid("price must contain digits only")
	}
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	for len(fraction) < digits {
		fraction += "0"
	}
	whole, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, invalid("price is too large")
	}
	for i := 0; i < digits; i++ {
		if whole > (1<<62)/10 {
			return 0, invalid("price is too large")
		}
		whole *= 10
	}
	if fraction != "" {
		minor, err := strconv.ParseInt(fraction, 10, 64)
		if err != nil || whole > (1<<62)-minor {
			return 0, invalid("price is too large")
		}
		whole += minor
	}
	return whole, nil
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
