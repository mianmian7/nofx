package provider

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"nofx/mcp"
)

const (
	DefaultGrokBaseURL = "https://api.x.ai/v1"
	DefaultGrokModel   = "grok-3-latest"
)

func init() {
	mcp.RegisterProvider(mcp.ProviderGrok, func(opts ...mcp.ClientOption) mcp.AIClient {
		return NewGrokClientWithOptions(opts...)
	})
}

type GrokClient struct {
	*mcp.Client
}

func (c *GrokClient) BaseClient() *mcp.Client { return c.Client }

// NewGrokClient creates Grok client (backward compatible)
func NewGrokClient() mcp.AIClient {
	return NewGrokClientWithOptions()
}

// NewGrokClientWithOptions creates Grok client (supports options pattern)
func NewGrokClientWithOptions(opts ...mcp.ClientOption) mcp.AIClient {
	grokOpts := []mcp.ClientOption{
		mcp.WithProvider(mcp.ProviderGrok),
		mcp.WithModel(DefaultGrokModel),
		mcp.WithBaseURL(DefaultGrokBaseURL),
	}

	// Optional provider-scoped budget for slow reasoning. The request loop still
	// bounds it by the parent cycle deadline minus its completion reserve.
	// Set idle and HTTP/attempt limits together: otherwise an older idle timer
	// cancels the request before the configured attempt budget can be used.
	seconds, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("AI_GROK_REQUEST_TIMEOUT_SECONDS")), 10, 32)
	if err == nil && seconds > 0 {
		timeout := time.Duration(seconds) * time.Second
		grokOpts = append(grokOpts, mcp.WithTimeout(timeout), func(cfg *mcp.Config) {
			cfg.StreamIdleTimeout = timeout
		})
	} else {
		// Grok reasoning routinely exceeds the generic per-request default
		// (AI_REQUEST_TIMEOUT_SECONDS), so keep its historical attempt cap.
		grokOpts = append(grokOpts, mcp.WithTimeout(mcp.DefaultTimeout))
	}

	allOpts := append(grokOpts, opts...)
	baseClient := mcp.NewClient(allOpts...).(*mcp.Client)

	grokClient := &GrokClient{
		Client: baseClient,
	}

	baseClient.Hooks = grokClient
	return grokClient
}

func (c *GrokClient) SetAPIKey(apiKey string, customURL string, customModel string) {
	c.APIKey = apiKey

	if len(apiKey) > 8 {
		c.Log.Infof("🔧 [MCP] Grok API Key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
	}
	if customURL != "" {
		c.BaseURL = customURL
		c.Log.Infof("🔧 [MCP] Grok using custom BaseURL: %s", customURL)
	} else {
		c.Log.Infof("🔧 [MCP] Grok using default BaseURL: %s", c.BaseURL)
	}
	if customModel != "" {
		c.Model = customModel
		c.Log.Infof("🔧 [MCP] Grok using custom Model: %s", customModel)
	} else {
		c.Log.Infof("🔧 [MCP] Grok using default Model: %s", c.Model)
	}
}

// Grok uses standard OpenAI-compatible API with Bearer auth
func (c *GrokClient) SetAuthHeader(reqHeaders http.Header) {
	c.Client.SetAuthHeader(reqHeaders)
}
