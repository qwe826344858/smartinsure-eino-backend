package platform

import "strings"

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func joinFirst(values []string, n int) string {
	if n <= 0 || len(values) == 0 {
		return ""
	}
	if len(values) < n {
		n = len(values)
	}
	return strings.Join(values[:n], "；")
}
