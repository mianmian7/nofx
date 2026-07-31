package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"nofx/crypto"
	"nofx/logger"
	"nofx/security"
	"nofx/store"
	"nofx/wallet"

	"github.com/gin-gonic/gin"
)

type ModelConfig struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Provider     string `json:"provider"`
	Enabled      bool   `json:"enabled"`
	APIKey       string `json:"apiKey,omitempty"`
	CustomAPIURL string `json:"customApiUrl,omitempty"`
}

// SafeModelConfig Safe model configuration structure (does not contain sensitive information)
type SafeModelConfig struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Provider        string   `json:"provider"`
	Enabled         bool     `json:"enabled"`
	HasAPIKey       bool     `json:"has_api_key"`
	CustomAPIURL    string   `json:"customApiUrl"`    // Custom API URL (usually not sensitive)
	CustomModelName string   `json:"customModelName"` // Custom model name (not sensitive)
	ModelNames      []string `json:"modelNames,omitempty"`
	WalletAddress   string   `json:"walletAddress,omitempty"`
	BalanceUSDC     string   `json:"balanceUsdc,omitempty"`
}

// ModelConfigUpdate is a single model's update payload. It is a named type
// (rather than an inline anonymous struct) so the log-sanitizer in utils.go is
// guaranteed to stay in sync with this shape — a mismatch there is what let
// plaintext credentials reach the logs previously.
type ModelConfigUpdate struct {
	Enabled         bool     `json:"enabled"`
	APIKey          string   `json:"api_key"`
	CustomAPIURL    string   `json:"custom_api_url"`
	CustomModelName string   `json:"custom_model_name"`
	ModelNames      []string `json:"model_names"`
}

type UpdateModelConfigRequest struct {
	Models map[string]ModelConfigUpdate `json:"models"`
}

// handleGetModelConfigs Get AI model configurations
func (s *Server) handleGetModelConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	logger.Infof("🔍 Querying AI model configs for user %s", userID)
	models, err := s.store.AIModel().List(userID)
	if err != nil {
		logger.Infof("❌ Failed to get AI model configs: %v", err)
		SafeInternalError(c, "Failed to get AI model configs", err)
		return
	}

	// If no models in database, return default models
	if len(models) == 0 {
		logger.Infof("⚠️ No AI models in database, returning defaults")
		defaultModels := []SafeModelConfig{
			{ID: "deepseek", Name: "DeepSeek AI", Provider: "deepseek", Enabled: false, HasAPIKey: false},
			{ID: "qwen", Name: "Qwen AI", Provider: "qwen", Enabled: false, HasAPIKey: false},
			{ID: "openai", Name: "OpenAI", Provider: "openai", Enabled: false, HasAPIKey: false},
			{ID: "claude", Name: "Claude AI", Provider: "claude", Enabled: false, HasAPIKey: false},
			{ID: "gemini", Name: "Gemini AI", Provider: "gemini", Enabled: false, HasAPIKey: false},
			{ID: "grok", Name: "Grok AI", Provider: "grok", Enabled: false, HasAPIKey: false},
			{ID: "kimi", Name: "Kimi AI", Provider: "kimi", Enabled: false, HasAPIKey: false},
			{ID: "minimax", Name: "MiniMax AI", Provider: "minimax", Enabled: false, HasAPIKey: false},
		}
		c.JSON(http.StatusOK, defaultModels)
		return
	}

	logger.Infof("✅ Found %d AI model configs", len(models))

	// Convert to safe response structure, remove sensitive information
	safeModels := make([]SafeModelConfig, 0, len(models))
	for _, model := range models {
		if !store.IsVisibleAIModel(model) {
			continue
		}
		safeModel := SafeModelConfig{
			ID:              model.ID,
			Name:            model.Name,
			Provider:        model.Provider,
			Enabled:         model.Enabled,
			HasAPIKey:       model.APIKey != "",
			CustomAPIURL:    model.CustomAPIURL,
			CustomModelName: model.CustomModelName,
			ModelNames:      store.DecodeStringList(model.ModelNames),
		}

		if model.Provider == "claw402" {
			if privateKey := strings.TrimSpace(model.APIKey.String()); privateKey != "" {
				if walletAddress, addrErr := walletAddressFromPrivateKey(privateKey); addrErr == nil {
					safeModel.WalletAddress = walletAddress
					safeModel.BalanceUSDC = wallet.QueryUSDCBalanceStr(walletAddress)
				} else {
					logger.Warnf("⚠️ Failed to derive claw402 wallet address for model %s: %v", model.ID, addrErr)
				}
			}
		}

		safeModels = append(safeModels, safeModel)
	}

	if len(safeModels) == 0 {
		logger.Infof("⚠️ No visible AI models in database, returning defaults")
		defaultModels := []SafeModelConfig{
			{ID: "deepseek", Name: "DeepSeek AI", Provider: "deepseek", Enabled: false, HasAPIKey: false},
			{ID: "qwen", Name: "Qwen AI", Provider: "qwen", Enabled: false, HasAPIKey: false},
			{ID: "openai", Name: "OpenAI", Provider: "openai", Enabled: false, HasAPIKey: false},
			{ID: "claude", Name: "Claude AI", Provider: "claude", Enabled: false, HasAPIKey: false},
			{ID: "gemini", Name: "Gemini AI", Provider: "gemini", Enabled: false, HasAPIKey: false},
			{ID: "grok", Name: "Grok AI", Provider: "grok", Enabled: false, HasAPIKey: false},
			{ID: "kimi", Name: "Kimi AI", Provider: "kimi", Enabled: false, HasAPIKey: false},
			{ID: "minimax", Name: "MiniMax AI", Provider: "minimax", Enabled: false, HasAPIKey: false},
		}
		c.JSON(http.StatusOK, defaultModels)
		return
	}

	c.JSON(http.StatusOK, safeModels)
}

// handleUpdateModelConfigs updates AI model configurations using encrypted transport only.
func (s *Server) handleUpdateModelConfigs(c *gin.Context) {
	userID := c.GetString("user_id")
	if !requireTransportEncryption(c) {
		return
	}

	// Read raw request body
	bodyBytes, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	var req UpdateModelConfigRequest
	var encryptedPayload crypto.EncryptedPayload
	if err := json.Unmarshal(bodyBytes, &encryptedPayload); err != nil {
		logger.Infof("❌ Failed to parse encrypted payload: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request format, encrypted transmission required"})
		return
	}

	// Verify encrypted data
	if encryptedPayload.WrappedKey == "" {
		logger.Infof("❌ Detected unencrypted request (UserID: %s)", userID)
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "This endpoint only supports encrypted transmission, please use encrypted client",
			"code":    "ENCRYPTION_REQUIRED",
			"message": "Encrypted transmission is required for security reasons",
		})
		return
	}

	// Decrypt data
	decrypted, err := s.cryptoHandler.cryptoService.DecryptSensitiveData(&encryptedPayload)
	if err != nil {
		logger.Infof("❌ Failed to decrypt model config (UserID: %s): %v", userID, err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to decrypt data"})
		return
	}

	// Parse decrypted data
	if err := json.Unmarshal([]byte(decrypted), &req); err != nil {
		logger.Infof("❌ Failed to parse decrypted data: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse decrypted data"})
		return
	}
	logger.Infof("🔓 Decrypted model config data (UserID: %s)", userID)

	// Update each model's configuration and track traders that need reload
	tradersToReload := make(map[string]bool)
	for modelID, modelData := range req.Models {
		// SSRF protection: validate custom_api_url before storing
		if modelData.CustomAPIURL != "" {
			cleanURL := strings.TrimSuffix(modelData.CustomAPIURL, "#")
			if err := security.ValidateModelURL(cleanURL); err != nil {
				logger.Warnf("Invalid custom_api_url for model %s: %v", modelID, err)
				c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Invalid custom_api_url for model %s: URL must be a valid HTTP(S) endpoint or an internal origin explicitly trusted by the operator", modelID)})
				return
			}
		}

		validatedModelNames, validationErr := s.validateModelConfigCatalogSelection(
			c.Request.Context(),
			userID,
			modelID,
			modelData,
		)
		if validationErr != nil {
			SafeBadRequestWithDetails(c, "The selected models could not be verified against the configured API", "model.config.invalid_model_selection", nil)
			return
		}
		if modelData.ModelNames != nil {
			modelData.ModelNames = validatedModelNames
		}

		// Find traders using this AI model BEFORE updating
		traders, _ := s.store.Trader().ListByAIModelID(userID, modelID)
		for _, t := range traders {
			tradersToReload[t.ID] = true
		}

		err := s.store.AIModel().UpdateWithModelNames(
			userID,
			modelID,
			"",
			modelData.Enabled,
			modelData.APIKey,
			modelData.CustomAPIURL,
			modelData.CustomModelName,
			modelData.ModelNames,
		)
		if err != nil {
			SafeInternalError(c, fmt.Sprintf("Update model %s", modelID), err)
			return
		}
	}

	// Remove affected traders from memory BEFORE reloading to pick up new config
	for traderID := range tradersToReload {
		logger.Infof("🔄 Removing trader %s from memory to reload with new AI model config", traderID)
		s.traderManager.RemoveTrader(traderID)
	}

	// Reload all traders for this user to make new config take effect immediately
	err = s.traderManager.LoadUserTradersFromStore(s.store, userID)
	if err != nil {
		logger.Infof("⚠️ Failed to reload user traders into memory: %v", err)
		// Don't return error here since model config was successfully updated to database
	}

	logger.Infof("✓ AI model config updated: %+v", SanitizeModelConfigForLog(req.Models))
	c.JSON(http.StatusOK, gin.H{"message": "Model configuration updated"})
}

// handleGetSupportedModels Get list of AI models supported by the system
func (s *Server) handleGetSupportedModels(c *gin.Context) {
	// Return static list of supported AI models with default versions
	supportedModels := []map[string]interface{}{
		{"id": "deepseek", "name": "DeepSeek", "provider": "deepseek", "defaultModel": "deepseek-chat"},
		{"id": "qwen", "name": "Qwen", "provider": "qwen", "defaultModel": "qwen3-max"},
		{"id": "openai", "name": "OpenAI", "provider": "openai", "defaultModel": "gpt-5.1"},
		{"id": "claude", "name": "Claude", "provider": "claude", "defaultModel": "claude-opus-4-6"},
		{"id": "gemini", "name": "Google Gemini", "provider": "gemini", "defaultModel": "gemini-3-pro-preview"},
		{"id": "grok", "name": "Grok (xAI)", "provider": "grok", "defaultModel": "grok-3-latest"},
		{"id": "kimi", "name": "Kimi (Moonshot)", "provider": "kimi", "defaultModel": "moonshot-v1-auto"},
		{"id": "minimax", "name": "MiniMax", "provider": "minimax", "defaultModel": "MiniMax-M2.7"},
		{"id": "blockrun-base", "name": "BlockRun (Base Wallet)", "provider": "blockrun-base", "defaultModel": "auto"},
		{"id": "blockrun-sol", "name": "BlockRun (Solana Wallet)", "provider": "blockrun-sol", "defaultModel": "auto"},
		{"id": "claw402", "name": "Claw402 (Base USDC)", "provider": "claw402", "defaultModel": "deepseek-v4-flash"},
	}

	c.JSON(http.StatusOK, supportedModels)
}
