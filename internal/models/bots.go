package models

import "strings"

func IsObviousBot(userAgent string) bool {
	ua := strings.ToLower(strings.TrimSpace(userAgent))
	if ua == "" {
		return true
	}
	markers := []string{
		"bot", "crawler", "spider", "slurp", "headless", "pingdom",
		"uptime", "monitoring", "curl/", "wget/", "python-requests", "go-http-client",
	}
	for _, marker := range markers {
		if strings.Contains(ua, marker) {
			return true
		}
	}
	return false
}
