// Package provider — JevClient implements TypeSafe AI's System One API (Jev model).
//
// Jev is a fast, structured evaluation / classification model that evaluates
// a structured `state` against typed questions (`choice`, `noul`, `score`)
// and returns calibrated probabilities and confidence scores.
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"nofx/mcp"
)

func init() {
	mcp.RegisterProvider(mcp.ProviderJev, func(opts ...mcp.ClientOption) mcp.AIClient {
		return NewJevClientWithOptions(opts...)
	})
}

// JevQuestion defines a question for Jev evaluation
type JevQuestion struct {
	Type         string      `json:"type"`                   // "choice", "noul", "score"
	Instructions string      `json:"instructions"`           // Prompt instructions for evaluation
	Criteria     interface{} `json:"criteria,omitempty"`     // Map[string]string for choice/noul or []string for score
}

// JevRequest is the request payload sent to the Jev System One endpoint
type JevRequest struct {
	Model     string                 `json:"model,omitempty"`
	State     interface{}            `json:"state"`
	Questions map[string]JevQuestion `json:"questions"`
}

// JevAnswer represents a single question's calibrated evaluation answer
type JevAnswer struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Noul          float64            `json:"noul,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
}

// JevResponse represents the top-level response from Jev
type JevResponse struct {
	Model   string               `json:"model"`
	Answers map[string]JevAnswer `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// JevClient wraps the base Client and provides typed evaluation methods
type JevClient struct {
	*mcp.Client
}

func (c *JevClient) BaseClient() *mcp.Client { return c.Client }

// NewJevClient creates a JevClient with default settings.
func NewJevClient() mcp.AIClient {
	return NewJevClientWithOptions()
}

// NewJevClientWithOptions creates a JevClient with options.
func NewJevClientWithOptions(opts ...mcp.ClientOption) mcp.AIClient {
	baseClient := mcp.NewClient(append([]mcp.ClientOption{
		mcp.WithProvider(mcp.ProviderJev),
		mcp.WithModel(mcp.DefaultJevModel),
		mcp.WithBaseURL(mcp.DefaultJevBaseURL),
		mcp.WithUseFullURL(true),
	}, opts...)...).(*mcp.Client)

	c := &JevClient{Client: baseClient}
	baseClient.Hooks = c
	return c
}

// SetAPIKey stores credentials and optional custom endpoint / model.
func (c *JevClient) SetAPIKey(apiKey, customURL, customModel string) {
	c.APIKey = apiKey
	if len(apiKey) > 8 {
		c.Log.Infof("🔧 [MCP] Jev API Key: %s...%s", apiKey[:4], apiKey[len(apiKey)-4:])
	}
	if customURL != "" {
		c.BaseURL = customURL
		c.Log.Infof("🔧 [MCP] Jev using custom BaseURL: %s", customURL)
	} else {
		c.BaseURL = mcp.DefaultJevBaseURL
		c.Log.Infof("🔧 [MCP] Jev using default BaseURL: %s", c.BaseURL)
	}
	if customModel != "" {
		c.Model = customModel
		c.Log.Infof("🔧 [MCP] Jev using custom Model: %s", customModel)
	} else {
		c.Model = mcp.DefaultJevModel
		c.Log.Infof("🔧 [MCP] Jev using default Model: %s", c.Model)
	}
}

// SetAuthHeader sets the standard Bearer token authorization header
func (c *JevClient) SetAuthHeader(reqHeaders http.Header) {
	if c.APIKey != "" {
		reqHeaders.Set("Authorization", "Bearer "+c.APIKey)
	}
}

// Evaluate sends a typed state & questions evaluation directly to Jev
func (c *JevClient) Evaluate(ctx context.Context, req *JevRequest) (*JevResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("jev request is nil")
	}
	if req.Model == "" {
		req.Model = c.Model
	}
	if req.Model == "" {
		req.Model = mcp.DefaultJevModel
	}

	reqBody, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal jev request: %w", err)
	}

	endpoint := c.BaseURL
	if endpoint == "" {
		endpoint = mcp.DefaultJevBaseURL
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	c.SetAuthHeader(httpReq.Header)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	start := time.Now()
	resp, err := client.Do(httpReq)
	duration := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("jev api call error (%v): %w", duration, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read jev response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jev api returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var jevResp JevResponse
	if err := json.Unmarshal(respBody, &jevResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal jev response: %w", err)
	}

	if mcp.TokenUsageCallback != nil && (jevResp.Usage.InputTokens > 0 || jevResp.Usage.OutputTokens > 0) {
		mcp.TokenUsageCallback(mcp.TokenUsage{
			Provider:         mcp.ProviderJev,
			Model:            jevResp.Model,
			PromptTokens:     jevResp.Usage.InputTokens,
			CompletionTokens: jevResp.Usage.OutputTokens,
			TotalTokens:      jevResp.Usage.InputTokens + jevResp.Usage.OutputTokens,
		})
	}

	return &jevResp, nil
}

func (c *JevClient) getTimeout() time.Duration {
	if c.Cfg != nil && c.Cfg.Timeout > 0 {
		return c.Cfg.Timeout
	}
	if c.HTTPClient != nil && c.HTTPClient.Timeout > 0 {
		return c.HTTPClient.Timeout
	}
	return mcp.DefaultTimeout
}

// CallWithMessages adapts Jev to the standard AIClient interface.
// If the prompt contains a JSON-formatted JevRequest, it evaluates it directly.
// Otherwise, it wraps userPrompt into state and evaluates standard trading questions.
func (c *JevClient) CallWithMessages(systemPrompt, userPrompt string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.getTimeout())
	defer cancel()

	var req JevRequest
	trimmed := strings.TrimSpace(userPrompt)
	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		if err := json.Unmarshal([]byte(trimmed), &req); err == nil && len(req.Questions) > 0 {
			resp, err := c.Evaluate(ctx, &req)
			if err != nil {
				return "", err
			}
			out, _ := json.Marshal(resp)
			return string(out), nil
		}
	}

	// Generic text fallback: evaluate user state
	stateMap := map[string]string{
		"system_context": systemPrompt,
		"market_context": userPrompt,
	}

	questions := map[string]JevQuestion{
		"signal": {
			Type:         "choice",
			Instructions: "Based on the market context, what is the best trading action?",
			Criteria: map[string]string{
				"open_long":  "High probability upward setup with indicator confirmation",
				"open_short": "High probability downward setup with indicator confirmation",
				"wait":       "No clear edge, range-bound chop, or conflicting signals",
			},
		},
		"market_regime": {
			Type:         "choice",
			Instructions: "What is the dominant market regime?",
			Criteria: map[string]string{
				"trending": "Clear trend with momentum",
				"ranging":  "Chop, oscillation, sideways or low volume",
			},
		},
	}

	req = JevRequest{
		Model:     c.Model,
		State:     stateMap,
		Questions: questions,
	}

	resp, err := c.Evaluate(ctx, &req)
	if err != nil {
		return "", err
	}

	signalAns := resp.Answers["signal"]
	regimeAns := resp.Answers["regime"]
	conf := int(signalAns.Confidence * 100)
	if conf <= 0 && signalAns.Probabilities != nil {
		if p, ok := signalAns.Probabilities[signalAns.Choice]; ok {
			conf = int(p * 100)
		}
	}
	if conf == 0 {
		conf = 70
	}

	choice := signalAns.Choice
	if choice == "" {
		choice = "wait"
	}

	// Synthesize standard XML reasoning and decision JSON
	reasoning := fmt.Sprintf("Jev System 1 evaluation: choice=%s, confidence=%d%%, regime=%s, probabilities=%v",
		choice, conf, regimeAns.Choice, signalAns.Probabilities)

	decisionJSON := fmt.Sprintf(`[{"symbol":"BTCUSDT","action":"%s","confidence":%d,"reasoning":"%s"}]`,
		choice, conf, reasoning)

	formatted := fmt.Sprintf("<reasoning>\n%s\n</reasoning>\n<decision>\n```json\n%s\n```\n</decision>", reasoning, decisionJSON)
	return formatted, nil
}
