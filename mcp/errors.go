package mcp

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// ErrorKind identifies the operational scope of an AI request failure.
type ErrorKind string

const (
	ErrorKindUnknown             ErrorKind = "unknown"
	ErrorKindAuthUnavailable     ErrorKind = "auth_unavailable"
	ErrorKindInvalidCredentials  ErrorKind = "invalid_credentials"
	ErrorKindRateLimited         ErrorKind = "rate_limited"
	ErrorKindProviderUnavailable ErrorKind = "provider_unavailable"
	ErrorKindNetworkUnavailable  ErrorKind = "network_unavailable"
	ErrorKindModelNotFound       ErrorKind = "model_not_found"
	ErrorKindContextLimit        ErrorKind = "context_limit_exceeded"
)

// APIError preserves the HTTP status and a bounded response summary so callers
// can choose a safe fallback without parsing arbitrary provider text.
type APIError struct {
	StatusCode int
	Kind       ErrorKind
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("API returned error (status %d): %s", e.StatusCode, e.Message)
}

// ErrorKindOf returns the most specific known classification for an error.
func ErrorKindOf(err error) ErrorKind {
	if err == nil {
		return ErrorKindUnknown
	}

	var apiError *APIError
	if errors.As(err, &apiError) {
		return apiError.Kind
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
	message := strings.TrimSpace(responseBody)
	if len(message) > 1024 {
		message = message[:1024] + "..."
	}

	return &APIError{
		StatusCode: statusCode,
		Kind:       classifyAPIError(statusCode, message),
		Message:    message,
	}
}

func classifyAPIError(statusCode int, message string) ErrorKind {
	lowerMessage := strings.ToLower(message)

	if strings.Contains(lowerMessage, "auth_unavailable") ||
		strings.Contains(lowerMessage, "authentication unavailable") ||
		strings.Contains(lowerMessage, "authentication pool") {
		return ErrorKindAuthUnavailable
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
	case http.StatusBadGateway, http.StatusServiceUnavailable, 520, 524:
		return ErrorKindProviderUnavailable
	}

	return ErrorKindUnknown
}

// IsFailoverEligible reports whether another configured model should be tried.
// Permanent credential errors and malformed requests are deliberately excluded.
func IsFailoverEligible(err error) bool {
	switch ErrorKindOf(err) {
	case ErrorKindAuthUnavailable,
		ErrorKindRateLimited,
		ErrorKindProviderUnavailable,
		ErrorKindNetworkUnavailable,
		ErrorKindModelNotFound:
		return true
	default:
		return false
	}
}
