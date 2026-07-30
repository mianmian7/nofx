package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"nofx/config"
	"nofx/crypto"
	"nofx/mcp"
	"nofx/security"
	"nofx/store"

	"github.com/gin-gonic/gin"
)

type modelCatalogResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

type modelDiscoveryRequest struct {
	ModelID      string `json:"model_id"`
	Provider     string `json:"provider"`
	APIKey       string `json:"api_key"`
	CustomAPIURL string `json:"custom_api_url"`
}

func modelCatalogURL(rawBaseURL string) (string, error) {
	cleanURL := strings.TrimSpace(rawBaseURL)
	useFullURL := strings.HasSuffix(cleanURL, "#")
	cleanURL = strings.TrimRight(strings.TrimSuffix(cleanURL, "#"), "/")
	if cleanURL == "" {
		return "", fmt.Errorf("model API URL is missing")
	}
	if useFullURL {
		const chatCompletionsSuffix = "/chat/completions"
		if !strings.HasSuffix(cleanURL, chatCompletionsSuffix) {
			return "", fmt.Errorf("cannot derive a model catalog URL from the configured full request URL")
		}
		cleanURL = strings.TrimSuffix(cleanURL, chatCompletionsSuffix)
	}
	return cleanURL + "/models", nil
}

func parseModelCatalog(body []byte) ([]string, error) {
	var catalog modelCatalogResponse
	if err := json.Unmarshal(body, &catalog); err != nil {
		return nil, fmt.Errorf("invalid model catalog response: %w", err)
	}
	modelNames := make([]string, 0, len(catalog.Data))
	for _, item := range catalog.Data {
		if name := strings.TrimSpace(item.ID); name != "" {
			modelNames = append(modelNames, name)
		}
	}
	modelNames = store.NormalizeStringList(modelNames)
	if len(modelNames) == 0 {
		return nil, fmt.Errorf("model catalog returned no model IDs")
	}
	sort.Strings(modelNames)
	return modelNames, nil
}

func discoverConfiguredAIModelNames(ctx context.Context, model *store.AIModel) ([]string, error) {
	if model == nil {
		return nil, fmt.Errorf("AI model configuration is missing")
	}
	baseURL := strings.TrimSpace(model.CustomAPIURL)
	if baseURL == "" {
		defaultURLs := map[string]string{
			"openai":   "https://api.openai.com/v1",
			"deepseek": "https://api.deepseek.com",
			"qwen":     "https://dashscope.aliyuncs.com/compatible-mode/v1",
			"claude":   "https://api.anthropic.com/v1",
			"gemini":   "https://generativelanguage.googleapis.com/v1beta/openai",
			"grok":     "https://api.x.ai/v1",
			"kimi":     "https://api.moonshot.ai/v1",
			"minimax":  "https://api.minimax.io/v1",
		}
		baseURL = defaultURLs[strings.ToLower(strings.TrimSpace(model.Provider))]
		if baseURL == "" {
			return nil, fmt.Errorf("this provider does not expose an OpenAI-compatible model catalog")
		}
	}
	apiKey := store.ResolveAIModelAPIKey(model)
	if apiKey == "" {
		return nil, fmt.Errorf("AI model credentials are missing")
	}

	catalogURL, err := modelCatalogURL(baseURL)
	if err != nil {
		return nil, err
	}
	httpClient, err := security.SafeHTTPClientForModelURL(catalogURL, 15*time.Second)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, catalogURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if strings.EqualFold(strings.TrimSpace(model.Provider), "claude") {
		request.Header.Set("x-api-key", apiKey)
		request.Header.Set("anthropic-version", "2023-06-01")
	} else {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return nil, fmt.Errorf("failed to query model catalog: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("failed to read model catalog: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, mcp.NewAPIError(response.StatusCode, string(body))
	}
	return parseModelCatalog(body)
}

func validateSameAPIFallbackModels(_ context.Context, primaryModel *store.AIModel, fallbackModelNames []string) ([]string, error) {
	normalizedNames := store.NormalizeStringList(fallbackModelNames)
	if len(normalizedNames) == 0 {
		return nil, nil
	}
	availableNames := store.DecodeStringList(primaryModel.ModelNames)
	if len(availableNames) == 0 {
		// Legacy model configs predate the saved catalog. Preserve their
		// existing trader chains until the model config is edited and verified.
		return normalizedNames, nil
	}
	return validateFallbackModelNamesAgainstCatalog(primaryModel.CustomModelName, normalizedNames, availableNames)
}

func validateFallbackModelNamesAgainstCatalog(primaryModelName string, fallbackModelNames, availableNames []string) ([]string, error) {
	normalizedNames := store.NormalizeStringList(fallbackModelNames)
	available := make(map[string]struct{}, len(availableNames))
	for _, name := range availableNames {
		available[name] = struct{}{}
	}
	validated := make([]string, 0, len(normalizedNames))
	for _, name := range normalizedNames {
		if name == primaryModelName {
			continue
		}
		if _, exists := available[name]; !exists {
			return nil, fmt.Errorf("fallback model %q is not available from the configured API", name)
		}
		validated = append(validated, name)
	}
	return validated, nil
}

func (s *Server) handleGetAvailableAIModels(c *gin.Context) {
	userID := c.GetString("user_id")
	model, err := s.store.AIModel().Get(userID, c.Param("id"))
	if err != nil {
		SafeNotFound(c, "AI model config")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"models":        store.DecodeStringList(model.ModelNames),
		"primary_model": model.CustomModelName,
	})
}

func (s *Server) decodeModelDiscoveryRequest(c *gin.Context) (*modelDiscoveryRequest, error) {
	body, err := c.GetRawData()
	if err != nil {
		return nil, err
	}
	var request modelDiscoveryRequest
	if !config.Get().TransportEncryption {
		if err := json.Unmarshal(body, &request); err != nil {
			return nil, err
		}
		return &request, nil
	}

	var encryptedPayload crypto.EncryptedPayload
	if err := json.Unmarshal(body, &encryptedPayload); err != nil {
		return nil, err
	}
	if encryptedPayload.WrappedKey == "" {
		return nil, fmt.Errorf("encrypted transmission is required")
	}
	decrypted, err := s.cryptoHandler.cryptoService.DecryptSensitiveData(&encryptedPayload)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(decrypted), &request); err != nil {
		return nil, err
	}
	return &request, nil
}

func (s *Server) handleDiscoverAIModels(c *gin.Context) {
	userID := c.GetString("user_id")
	request, err := s.decodeModelDiscoveryRequest(c)
	if err != nil {
		SafeBadRequest(c, "Invalid model discovery request")
		return
	}

	provider := strings.TrimSpace(request.Provider)
	apiKey := strings.TrimSpace(request.APIKey)
	baseURL := strings.TrimSpace(request.CustomAPIURL)
	if savedModel, getErr := s.store.AIModel().Get(userID, request.ModelID); getErr == nil {
		if provider == "" {
			provider = savedModel.Provider
		}
		if apiKey == "" {
			apiKey = store.ResolveAIModelAPIKey(savedModel)
		}
		if baseURL == "" {
			baseURL = savedModel.CustomAPIURL
		}
	}
	if provider == "" {
		provider = strings.TrimSpace(request.ModelID)
	}

	modelNames, err := discoverConfiguredAIModelNames(c.Request.Context(), &store.AIModel{
		ID:           request.ModelID,
		UserID:       userID,
		Provider:     provider,
		Enabled:      true,
		APIKey:       crypto.EncryptedString(apiKey),
		CustomAPIURL: baseURL,
	})
	if err != nil {
		SafeError(c, http.StatusBadGateway, "Unable to load models from the configured API", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"models": modelNames})
}

func (s *Server) validateModelConfigCatalogSelection(ctx context.Context, userID, modelID string, update ModelConfigUpdate) ([]string, error) {
	if update.ModelNames == nil {
		return nil, nil
	}
	if len(update.ModelNames) == 0 {
		return []string{}, nil
	}
	selectedNames := store.NormalizeStringList(update.ModelNames)
	if primaryName := strings.TrimSpace(update.CustomModelName); primaryName != "" {
		selectedNames = store.NormalizeStringList(append([]string{primaryName}, selectedNames...))
	}
	if len(selectedNames) == 0 {
		return nil, nil
	}

	var savedModel *store.AIModel
	models, err := s.store.AIModel().List(userID)
	if err != nil {
		return nil, err
	}
	for _, model := range models {
		if model.ID == modelID || strings.EqualFold(model.Provider, modelID) {
			savedModel = model
			break
		}
	}
	provider := modelID
	apiKey := strings.TrimSpace(update.APIKey)
	baseURL := strings.TrimSpace(update.CustomAPIURL)
	if savedModel != nil {
		provider = savedModel.Provider
		if apiKey == "" {
			apiKey = store.ResolveAIModelAPIKey(savedModel)
		}
		if baseURL == "" {
			baseURL = savedModel.CustomAPIURL
		}
	}

	availableNames, err := discoverConfiguredAIModelNames(ctx, &store.AIModel{
		Provider:     provider,
		Enabled:      true,
		APIKey:       crypto.EncryptedString(apiKey),
		CustomAPIURL: baseURL,
	})
	if err != nil {
		return nil, err
	}
	return validateFallbackModelNamesAgainstCatalog("", selectedNames, availableNames)
}
