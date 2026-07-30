package trader

import (
	"fmt"
	"nofx/logger"
	"nofx/mcp"
	"sync"
	"time"
)

const (
	defaultModelCooldown        = 2 * time.Minute
	authenticationModelCooldown = 30 * time.Minute
)

type failoverCandidate struct {
	configuration AIModelCandidate
	client        mcp.AIClient
	upstreamKey   string
	cooldownUntil time.Time
}

// AIModelFailoverClient tries model variants in order. The primary model and
// same-upstream variants are placed before independent provider candidates by
// the manager, so a Sol failure naturally moves to Terra and then Luna.
type AIModelFailoverClient struct {
	mu            sync.RWMutex
	candidates    []failoverCandidate
	activeIndex   int
	lastFailure   mcp.ErrorKind
	fallbackSince time.Time
}

func newAIModelFailoverClient(candidates []failoverCandidate) (*AIModelFailoverClient, error) {
	if len(candidates) == 0 {
		return nil, fmt.Errorf("AI model failover chain is empty")
	}
	for index := range candidates {
		if candidates[index].upstreamKey == "" {
			candidates[index].upstreamKey = upstreamIdentity(candidates[index].configuration)
		}
	}
	return &AIModelFailoverClient{candidates: candidates}, nil
}

func newAIClientForCandidate(configuration AIModelCandidate) (mcp.AIClient, error) {
	var client mcp.AIClient
	if configuration.Provider == mcp.ProviderCustom {
		client = mcp.New()
	} else {
		client = mcp.NewAIClientByProvider(configuration.Provider)
	}
	if client == nil {
		return nil, fmt.Errorf("unsupported AI provider %q", configuration.Provider)
	}

	if configuration.Provider == mcp.ProviderClaw402 {
		client.SetAPIKey(configuration.APIKey, "", configuration.ModelName)
	} else {
		client.SetAPIKey(configuration.APIKey, configuration.CustomAPIURL, configuration.ModelName)
		if configuration.CustomAPIURL != "" {
			if configurator, ok := client.(mcp.CustomURLConfigurator); ok {
				if err := configurator.ConfigureCustomURL(configuration.CustomAPIURL); err != nil {
					return nil, fmt.Errorf("invalid custom model URL: %w", err)
				}
			}
		}
	}
	return client, nil
}

func (client *AIModelFailoverClient) SetAPIKey(apiKey, customURL, customModel string) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.candidates) == 0 {
		return
	}
	client.candidates[client.activeIndex].client.SetAPIKey(apiKey, customURL, customModel)
}

func (client *AIModelFailoverClient) SetTimeout(timeout time.Duration) {
	client.mu.RLock()
	defer client.mu.RUnlock()
	for _, candidate := range client.candidates {
		candidate.client.SetTimeout(timeout)
	}
}

func (client *AIModelFailoverClient) CallWithMessages(systemPrompt, userPrompt string) (string, error) {
	return client.tryCandidates(func(candidate mcp.AIClient) (string, error) {
		return candidate.CallWithMessages(systemPrompt, userPrompt)
	})
}

func (client *AIModelFailoverClient) CallWithRequest(request *mcp.Request) (string, error) {
	return client.tryCandidatesWithRequest(request, func(candidate mcp.AIClient, candidateRequest *mcp.Request) (string, error) {
		return candidate.CallWithRequest(candidateRequest)
	})
}

func (client *AIModelFailoverClient) CallWithRequestFull(request *mcp.Request) (*mcp.LLMResponse, error) {
	result, err := client.tryCandidatesFullWithRequest(request, func(candidate mcp.AIClient, candidateRequest *mcp.Request) (*mcp.LLMResponse, error) {
		return candidate.CallWithRequestFull(candidateRequest)
	})
	return result, err
}

func (client *AIModelFailoverClient) CallWithRequestStream(request *mcp.Request, onChunk func(string)) (string, error) {
	client.mu.RLock()
	candidateIndex := client.activeIndex
	candidate := client.candidates[candidateIndex]
	client.mu.RUnlock()

	return candidate.client.CallWithRequestStream(cloneRequestForCandidate(request, candidate.configuration), onChunk)
}

func (client *AIModelFailoverClient) tryCandidates(call func(mcp.AIClient) (string, error)) (string, error) {
	client.mu.Lock()
	candidateOrder := client.buildCandidateOrderLocked()
	cooldownError := client.allCandidatesCoolingDownErrorLocked(candidateOrder)
	client.mu.Unlock()
	if cooldownError != nil {
		return "", cooldownError
	}

	var lastError error
	for _, candidateIndex := range candidateOrder {
		client.mu.RLock()
		candidate := client.candidates[candidateIndex]
		candidateCoolingDown := candidate.cooldownUntil.After(time.Now())
		client.mu.RUnlock()
		if candidateCoolingDown {
			continue
		}

		result, err := call(candidate.client)
		if err == nil {
			client.markCandidateHealthy(candidateIndex)
			return result, nil
		}
		lastError = err
		if !mcp.IsFailoverEligible(err) {
			return "", err
		}

		client.markCandidateUnavailable(candidateIndex, err)
	}

	return "", lastError
}

func (client *AIModelFailoverClient) tryCandidatesWithRequest(request *mcp.Request, call func(mcp.AIClient, *mcp.Request) (string, error)) (string, error) {
	client.mu.Lock()
	candidateOrder := client.buildCandidateOrderLocked()
	cooldownError := client.allCandidatesCoolingDownErrorLocked(candidateOrder)
	client.mu.Unlock()
	if cooldownError != nil {
		return "", cooldownError
	}

	var lastError error
	for _, candidateIndex := range candidateOrder {
		client.mu.RLock()
		candidate := client.candidates[candidateIndex]
		candidateCoolingDown := candidate.cooldownUntil.After(time.Now())
		client.mu.RUnlock()
		if candidateCoolingDown {
			continue
		}

		result, err := call(candidate.client, cloneRequestForCandidate(request, candidate.configuration))
		if err == nil {
			client.markCandidateHealthy(candidateIndex)
			return result, nil
		}
		lastError = err
		if !mcp.IsFailoverEligible(err) {
			return "", err
		}

		client.markCandidateUnavailable(candidateIndex, err)
	}

	return "", lastError
}

func (client *AIModelFailoverClient) tryCandidatesFullWithRequest(request *mcp.Request, call func(mcp.AIClient, *mcp.Request) (*mcp.LLMResponse, error)) (*mcp.LLMResponse, error) {
	client.mu.Lock()
	candidateOrder := client.buildCandidateOrderLocked()
	cooldownError := client.allCandidatesCoolingDownErrorLocked(candidateOrder)
	client.mu.Unlock()
	if cooldownError != nil {
		return nil, cooldownError
	}

	var lastError error
	for _, candidateIndex := range candidateOrder {
		client.mu.RLock()
		candidate := client.candidates[candidateIndex]
		candidateCoolingDown := candidate.cooldownUntil.After(time.Now())
		client.mu.RUnlock()
		if candidateCoolingDown {
			continue
		}

		result, err := call(candidate.client, cloneRequestForCandidate(request, candidate.configuration))
		if err == nil {
			client.markCandidateHealthy(candidateIndex)
			return result, nil
		}
		lastError = err
		if !mcp.IsFailoverEligible(err) {
			return nil, err
		}

		client.markCandidateUnavailable(candidateIndex, err)
	}

	return nil, lastError
}

func (client *AIModelFailoverClient) buildCandidateOrderLocked() []int {
	now := time.Now()
	order := make([]int, 0, len(client.candidates))
	seenIndices := make(map[int]struct{}, len(client.candidates))
	if client.activeIndex != 0 && !client.candidates[0].cooldownUntil.After(now) {
		order = append(order, 0)
		seenIndices[0] = struct{}{}
	}
	for offset := 0; offset < len(client.candidates); offset++ {
		candidateIndex := (client.activeIndex + offset) % len(client.candidates)
		if _, alreadyAdded := seenIndices[candidateIndex]; alreadyAdded {
			continue
		}
		candidate := client.candidates[candidateIndex]
		if candidate.cooldownUntil.After(now) {
			continue
		}
		order = append(order, candidateIndex)
		seenIndices[candidateIndex] = struct{}{}
	}

	return order
}

func (client *AIModelFailoverClient) allCandidatesCoolingDownErrorLocked(candidateOrder []int) error {
	if len(candidateOrder) > 0 {
		return nil
	}

	nextAvailableAt := time.Time{}
	for _, candidate := range client.candidates {
		if nextAvailableAt.IsZero() || candidate.cooldownUntil.Before(nextAvailableAt) {
			nextAvailableAt = candidate.cooldownUntil
		}
	}
	if nextAvailableAt.IsZero() {
		return fmt.Errorf("all AI model candidates are unavailable")
	}
	return fmt.Errorf("all AI model candidates are cooling down until %s", nextAvailableAt.Format(time.RFC3339))
}

func (client *AIModelFailoverClient) markCandidateHealthy(candidateIndex int) {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.candidates[candidateIndex].cooldownUntil = time.Time{}
	client.activeIndex = candidateIndex
	if candidateIndex == 0 {
		client.lastFailure = mcp.ErrorKindUnknown
		client.fallbackSince = time.Time{}
	} else if client.fallbackSince.IsZero() {
		client.fallbackSince = time.Now()
	}
}

func (client *AIModelFailoverClient) markCandidateUnavailable(candidateIndex int, err error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	errorKind := mcp.ErrorKindOf(err)
	cooldownDuration := defaultModelCooldown
	if errorKind == mcp.ErrorKindAuthUnavailable {
		cooldownDuration = authenticationModelCooldown
	}
	cooldownUntil := time.Now().Add(cooldownDuration)
	client.candidates[candidateIndex].cooldownUntil = cooldownUntil
	if errorKind == mcp.ErrorKindAuthUnavailable {
		upstreamKey := client.candidates[candidateIndex].upstreamKey
		for index := range client.candidates {
			if client.candidates[index].upstreamKey == upstreamKey {
				client.candidates[index].cooldownUntil = cooldownUntil
			}
		}
	}
	client.lastFailure = errorKind
	logger.Warnf("AI model %s/%s is cooling down after %s: %v",
		client.candidates[candidateIndex].configuration.Provider,
		modelDisplayName(client.candidates[candidateIndex].configuration),
		mcp.ErrorKindOf(err), err)
}

// BaseClient exposes the currently active model to context-limit checks.
func (client *AIModelFailoverClient) BaseClient() *mcp.Client {
	client.mu.RLock()
	defer client.mu.RUnlock()
	if len(client.candidates) == 0 {
		return nil
	}
	if embedder, ok := client.candidates[client.activeIndex].client.(mcp.ClientEmbedder); ok {
		return embedder.BaseClient()
	}
	return nil
}

func (client *AIModelFailoverClient) ActiveModel() (provider, model, modelID string) {
	client.mu.RLock()
	defer client.mu.RUnlock()
	if len(client.candidates) == 0 {
		return "", "", ""
	}
	activeConfiguration := client.candidates[client.activeIndex].configuration
	return activeConfiguration.Provider, modelDisplayName(activeConfiguration), activeConfiguration.ID
}

func (client *AIModelFailoverClient) RuntimeState() (provider, model, modelID string, fallbackReason mcp.ErrorKind, fallbackSince time.Time) {
	client.mu.RLock()
	defer client.mu.RUnlock()
	if len(client.candidates) == 0 {
		return "", "", "", mcp.ErrorKindUnknown, time.Time{}
	}
	activeConfiguration := client.candidates[client.activeIndex].configuration
	return activeConfiguration.Provider,
		modelDisplayName(activeConfiguration),
		activeConfiguration.ID,
		client.lastFailure,
		client.fallbackSince
}

func upstreamIdentity(configuration AIModelCandidate) string {
	return configuration.Provider + "\x00" + configuration.CustomAPIURL + "\x00" + configuration.APIKey
}

func modelDisplayName(configuration AIModelCandidate) string {
	if configuration.ModelName != "" {
		return configuration.ModelName
	}
	return configuration.Provider
}

func cloneRequestForCandidate(request *mcp.Request, configuration AIModelCandidate) *mcp.Request {
	if request == nil {
		return nil
	}
	clonedRequest := *request
	if configuration.ModelName != "" {
		clonedRequest.Model = configuration.ModelName
	}
	return &clonedRequest
}
