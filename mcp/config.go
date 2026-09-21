package mcp

import (
	"net/http"
	"os"
	"strconv"
	"time"

	"nofx/logger"
	"nofx/security"
)

// Config client configuration (centralized management of all configurations)
type Config struct {
	// Provider configuration
	Provider string
	APIKey   string
	BaseURL  string
	Model    string

	// Behavior configuration
	MaxTokens   int
	MaxContext  int // Model's max context window in tokens (0 = no limit)
	Temperature float64
	UseFullURL  bool
	Stream      bool // Whether to use streaming response

	// Retry configuration
	// MaxRetries is retained for compatibility and represents the total number
	// of attempts, not the number of additional retries.
	MaxRetries int
	// MaxAttempts optionally overrides MaxRetries and also represents total
	// attempts. A zero value falls back to MaxRetries.
	MaxAttempts     int
	RetryWaitBase   time.Duration
	RetryableErrors []string

	// Timeout configuration
	Timeout            time.Duration
	StreamIdleTimeout  time.Duration
	AdmissionTimeout   time.Duration
	CompletionReserve  time.Duration
	MinAttemptBudget   time.Duration
	RateLimitCooldown  time.Duration
	UsageLimitCooldown time.Duration

	// Dependency injection
	Logger     Logger
	HTTPClient *http.Client
}

// DefaultConfig returns default configuration
func DefaultConfig() *Config {
	return &Config{
		// Default values
		MaxTokens:          getEnvInt("AI_MAX_TOKENS", 2000),
		Temperature:        MCPClientTemperature,
		MaxRetries:         MaxRetryTimes,
		RetryWaitBase:      2 * time.Second,
		Timeout:            DefaultTimeout,
		StreamIdleTimeout:  time.Duration(getEnvInt("AI_STREAM_IDLE_TIMEOUT_SECONDS", 60)) * time.Second,
		AdmissionTimeout:   time.Duration(getEnvInt("AI_REQUEST_ADMISSION_TIMEOUT_SECONDS", 60)) * time.Second,
		CompletionReserve:  time.Duration(getEnvInt("AI_CYCLE_COMPLETION_RESERVE_SECONDS", 30)) * time.Second,
		MinAttemptBudget:   time.Duration(getEnvInt("AI_MIN_ATTEMPT_BUDGET_SECONDS", 10)) * time.Second,
		RateLimitCooldown:  time.Duration(getEnvInt("AI_RATE_LIMIT_COOLDOWN_SECONDS", 30)) * time.Second,
		UsageLimitCooldown: time.Duration(getEnvInt("AI_USAGE_LIMIT_COOLDOWN_SECONDS", 900)) * time.Second,
		RetryableErrors:    retryableErrors,
		Stream:             getEnvBool("AI_STREAM_ENABLED", true),

		// Default dependencies (use global logger)
		Logger:     logger.NewMCPLogger(),
		HTTPClient: security.SafeHTTPClient(DefaultTimeout),
	}
}

// getEnvInt reads integer from environment variable, returns default value if failed
func getEnvInt(key string, defaultValue int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			return parsed
		}
	}
	return defaultValue
}

// getEnvBool reads boolean from environment variable, returns default value if failed
func getEnvBool(key string, defaultValue bool) bool {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.ParseBool(val); err == nil {
			return parsed
		}
	}
	return defaultValue
}

// getEnvString reads string from environment variable, returns default value if empty
func getEnvString(key string, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}
