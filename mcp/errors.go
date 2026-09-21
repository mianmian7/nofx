package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrorKind identifies the operational scope of an AI request failure.
type ErrorKind string

const (
	ErrorKindUnknown             ErrorKind = "unknown"
	ErrorKindAuthUnavailable     ErrorKind = "auth_unavailable"
	ErrorKindInvalidCredentials  ErrorKind = "invalid_credentials"
	ErrorKindRateLimited         ErrorKind = "rate_limited"
	ErrorKindUsageLimit          ErrorKind = "usage_limit_reached"
	ErrorKindProviderUnavailable ErrorKind = "provider_unavailable"
	ErrorKindNetworkUnavailable  ErrorKind = "network_unavailable"
	ErrorKindModelNotFound       ErrorKind = "model_not_found"
	ErrorKindContextLimit        ErrorKind = "context_limit_exceeded"
	ErrorKindInvalidRequest      ErrorKind = "invalid_request"
)

// APIError preserves the HTTP status and a bounded response summary so callers
// can choose a safe fallback without parsing arbitrary provider text.
type APIError struct {
	StatusCode int
	Kind       ErrorKind
	Message    string
	RetryAfter time.Duration
}

// ModelCooldownError reports that a shared upstream identity is cooling down.
// Its kind allows an independent failover candidate to be considered without
// retrying the same exhausted identity.
type ModelCooldownError struct {
	Kind      ErrorKind
	Remaining time.Duration
}

func (e *ModelCooldownError) Error() string {
	return fmt.Sprintf("shared model cooldown (%s) has %s remaining", e.Kind, e.Remaining.Round(time.Millisecond))
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API returned error (status %d): %s", e.StatusCode, e.Message)
}

// ErrorKindOf returns the most specific known classification for an error.
func ErrorKindOf(err error) ErrorKind {
	if err == nil {
		return ErrorKindUnknown
	}

	var cooldownError *ModelCooldownError
	if errors.As(err, &cooldownError) {
		return cooldownError.Kind
	}
	var apiError *APIError
	if errors.As(err, &apiError) {
		return apiError.Kind
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ErrorKindNetworkUnavailable
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return ErrorKindNetworkUnavailable
	}
	if errors.Is(err, io.EOF) {
		return ErrorKindNetworkUnavailable
	}

	lowerMessage := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lowerMessage, "context limit"),
		strings.Contains(lowerMessage, "maximum context"):
		return ErrorKindContextLimit
	case strings.Contains(lowerMessage, "no such host"),
		strings.Contains(lowerMessage, "connection refused"),
		strings.Contains(lowerMessage, "connection reset"),
		strings.Contains(lowerMessage, "timeout"),
		strings.Contains(lowerMessage, "eof"):
		return ErrorKindNetworkUnavailable
	default:
		return ErrorKindUnknown
	}
}

// NewAPIError creates a typed error from an unsuccessful provider response.
func NewAPIError(statusCode int, responseBody string) *APIError {
	return NewAPIErrorWithHeaders(statusCode, responseBody, nil, time.Now())
}

// NewAPIErrorWithHeaders preserves a bounded Retry-After hint. Both delta
// seconds and HTTP-date forms are supported.
func NewAPIErrorWithHeaders(statusCode int, responseBody string, headers http.Header, now time.Time) *APIError {
	message := redactSensitiveText(strings.TrimSpace(responseBody))
	if len(message) > 1024 {
		message = message[:1024] + "..."
	}

	return &APIError{
		StatusCode: statusCode,
		Kind:       classifyAPIError(statusCode, message),
		Message:    message,
		RetryAfter: parseRetryAfter(headers, now),
	}
}

func parseRetryAfter(headers http.Header, now time.Time) time.Duration {
	if headers == nil {
		return 0
	}
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if delay := when.Sub(now); delay > 0 {
		return delay
	}
	return 0
}

func classifyAPIError(statusCode int, message string) ErrorKind {
	lowerMessage := strings.ToLower(message)
	if strings.Contains(lowerMessage, "context limit") ||
		strings.Contains(lowerMessage, "context_length") ||
		strings.Contains(lowerMessage, "context window") ||
		strings.Contains(lowerMessage, "maximum context") {
		return ErrorKindContextLimit
	}

	if strings.Contains(lowerMessage, "auth_unavailable") ||
		strings.Contains(lowerMessage, "authentication unavailable") ||
		strings.Contains(lowerMessage, "authentication pool") {
		return ErrorKindAuthUnavailable
	}
	if strings.Contains(lowerMessage, "usage_limit_reached") ||
		strings.Contains(lowerMessage, "usage limit has been reached") ||
		strings.Contains(lowerMessage, "quota exceeded") {
		return ErrorKindUsageLimit
	}

	switch statusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrorKindInvalidCredentials
	case http.StatusNotFound:
		if strings.Contains(lowerMessage, "model") {
			return ErrorKindModelNotFound
		}
	case http.StatusTooManyRequests:
		return ErrorKindRateLimited
	case http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout,
		522, 520, 524:
		return ErrorKindProviderUnavailable
	}
	if statusCode >= 400 && statusCode < 500 {
		return ErrorKindInvalidRequest
	}

	return ErrorKindUnknown
}

// APIStatusCodeOf returns the HTTP status carried by an APIError, if any.
// It deliberately uses errors.As so wrapped provider errors remain inspectable.
func APIStatusCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var apiError *APIError
	if errors.As(err, &apiError) {
		return apiError.StatusCode
	}
	return 0
}

// RetryAfterOf returns the provider's Retry-After hint when available.
func RetryAfterOf(err error) time.Duration {
	var apiError *APIError
	if errors.As(err, &apiError) {
		return apiError.RetryAfter
	}
	return 0
}

var sensitiveTextPattern = regexp.MustCompile(`(?i)("?(?:api[_ -]?key|authorization|x-api-key|token|secret|password|access[_ -]?token|refresh[_ -]?token)"?\s*[:=]\s*)["']?[^\s,"'}]+`)

// redactSensitiveText bounds the amount of provider text that can reach logs
// or wrapped errors and removes common credential-shaped fields. It is not a
// parser for arbitrary provider payloads; callers must still avoid logging
// prompts and response bodies directly.
func redactSensitiveText(message string) string {
	message = sensitiveTextPattern.ReplaceAllString(message, `${1}[REDACTED]`)
	message = strings.ReplaceAll(message, "Bearer ", "Bearer [REDACTED]")
	message = strings.ReplaceAll(message, "bearer ", "bearer [REDACTED]")
	return message
}

// IsFailoverEligible reports whether another configured model should be tried.
// Permanent credential errors and malformed requests are deliberately excluded.
func IsFailoverEligible(err error) bool {
	switch ErrorKindOf(err) {
	case ErrorKindAuthUnavailable,
		ErrorKindRateLimited,
		ErrorKindUsageLimit,
		ErrorKindProviderUnavailable,
		ErrorKindNetworkUnavailable,
		ErrorKindModelNotFound:
		return true
	default:
		return false
	}
}
