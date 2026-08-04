package mcp

import (
	"net/url"
	"strconv"
	"strings"
	"unicode"

	"github.com/google/uuid"
)

// CallMetadata carries correlation fields for one logical AI call. It is
// deliberately kept outside the provider request payload and is only used for
// diagnostics and telemetry.
type CallMetadata struct {
	CallID     string `json:"-"`
	TraderID   string `json:"-"`
	StrategyID string `json:"-"`
	Provider   string `json:"-"`
	Model      string `json:"-"`
}

// normalizeCallMetadata preserves caller-supplied correlation and generates a
// call ID when an upper layer has not supplied one.
func normalizeCallMetadata(metadata CallMetadata) CallMetadata {
	if strings.TrimSpace(metadata.CallID) == "" {
		metadata.CallID = uuid.NewString()
	}
	return metadata
}

// boundedLogValue removes control characters and caps values before they reach
// structured logs. The limit is measured in runes to avoid cutting UTF-8.
func boundedLogValue(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

// safeErrorSummary returns a bounded, credential-redacted error summary for
// diagnostic logs. The original typed error remains available through wrapping.
func safeErrorSummary(err error) string {
	if err == nil {
		return ""
	}
	summary := boundedLogValue(redactSensitiveText(err.Error()), 1024)
	if summary == "" {
		return "unknown error"
	}
	return summary
}

func formatHTTPStatus(statusCode int) string {
	if statusCode <= 0 {
		return "none"
	}
	return strconv.Itoa(statusCode)
}

// sanitizeURL keeps useful endpoint diagnostics while removing credentials and
// sensitive query parameters. It also bounds malformed or unexpectedly long
// values before they are logged.
func sanitizeURL(rawURL string) string {
	if strings.TrimSpace(rawURL) == "" {
		return ""
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return boundedLogValue(redactSensitiveText(rawURL), 512)
	}
	if parsed.User != nil {
		username := parsed.User.Username()
		if _, hasPassword := parsed.User.Password(); hasPassword {
			parsed.User = url.UserPassword(username, "[REDACTED]")
		}
	}
	query := parsed.Query()
	for key, values := range query {
		lowerKey := strings.ToLower(key)
		if strings.Contains(lowerKey, "key") ||
			strings.Contains(lowerKey, "token") ||
			strings.Contains(lowerKey, "secret") ||
			strings.Contains(lowerKey, "password") ||
			strings.Contains(lowerKey, "auth") ||
			strings.Contains(lowerKey, "signature") {
			for i := range values {
				values[i] = "[REDACTED]"
			}
			query[key] = values
		}
	}
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return boundedLogValue(redactSensitiveText(parsed.String()), 512)
}
