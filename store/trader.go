package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TraderStore trader storage
type TraderStore struct {
	db *gorm.DB
}

// NewTraderStore creates a new trader store
func NewTraderStore(db *gorm.DB) *TraderStore {
	return &TraderStore{db: db}
}

// Trader trader configuration
type Trader struct {
	ID                  string    `gorm:"primaryKey" json:"id"`
	UserID              string    `gorm:"column:user_id;not null;default:default;index" json:"user_id"`
	Name                string    `gorm:"column:name;not null" json:"name"`
	AIModelID           string    `gorm:"column:ai_model_id;not null" json:"ai_model_id"`
	PrimaryModelName    string    `gorm:"column:primary_model_name;default:''" json:"primary_model_name,omitempty"`
	ExchangeID          string    `gorm:"column:exchange_id;not null" json:"exchange_id"`
	StrategyID          string    `gorm:"column:strategy_id;default:''" json:"strategy_id"`
	ExecutionMode       string    `gorm:"column:execution_mode;not null;default:paper" json:"execution_mode"`
	InitialBalance      float64   `gorm:"column:initial_balance;not null" json:"initial_balance"`
	ScanIntervalMinutes int       `gorm:"column:scan_interval_minutes;default:15" json:"scan_interval_minutes"`
	StartupDelayMinutes int       `gorm:"column:startup_delay_minutes;default:0" json:"startup_delay_minutes"`
	FallbackModelNames  string    `gorm:"column:fallback_model_names;default:''" json:"fallback_model_names,omitempty"`
	FallbackAIModelIDs  string    `gorm:"column:fallback_ai_model_ids;default:''" json:"fallback_ai_model_ids,omitempty"`
	IsRunning           bool      `gorm:"column:is_running;default:false" json:"is_running"`
	IsCrossMargin       bool      `gorm:"column:is_cross_margin;default:true" json:"is_cross_margin"`
	ShowInCompetition   bool      `gorm:"column:show_in_competition;default:true" json:"show_in_competition"`
	InvertSignals       bool      `gorm:"column:invert_signals;default:false" json:"invert_signals"`
	CreatedAt           time.Time `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt           time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`

	TradingSymbols       string `gorm:"column:trading_symbols;default:''" json:"trading_symbols,omitempty"`
	CustomPrompt         string `gorm:"column:custom_prompt;default:''" json:"custom_prompt,omitempty"`
	OverrideBasePrompt   bool   `gorm:"column:override_base_prompt;default:false" json:"override_base_prompt,omitempty"`
	SystemPromptTemplate string `gorm:"column:system_prompt_template;default:default" json:"system_prompt_template,omitempty"`
}

// EncodeStringList stores a user-configured ordered list as compact JSON.
// Empty values are stored as an empty string so older rows remain readable.
func EncodeStringList(values []string) string {
	normalizedValues := NormalizeStringList(values)
	if len(normalizedValues) == 0 {
		return ""
	}

	encodedValues, err := json.Marshal(normalizedValues)
	if err != nil {
		return ""
	}
	return string(encodedValues)
}

// DecodeStringList reads both the current JSON format and a legacy comma list.
func DecodeStringList(encodedValues string) []string {
	encodedValues = strings.TrimSpace(encodedValues)
	if encodedValues == "" {
		return nil
	}

	var values []string
	if json.Unmarshal([]byte(encodedValues), &values) == nil {
		return NormalizeStringList(values)
	}

	return NormalizeStringList(strings.Split(encodedValues, ","))
}

// NormalizeStringList trims, removes empty values, and preserves order.
func NormalizeStringList(values []string) []string {
	seenValues := make(map[string]struct{}, len(values))
	normalizedValues := make([]string, 0, len(values))
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			continue
		}
		if _, alreadySeen := seenValues[trimmedValue]; alreadySeen {
			continue
		}
		seenValues[trimmedValue] = struct{}{}
		normalizedValues = append(normalizedValues, trimmedValue)
	}
	return normalizedValues
}

// TableName returns the table name for Trader
func (Trader) TableName() string {
	return "traders"
}

// TraderFullConfig trader full configuration (includes AI model, exchange and strategy)
type TraderFullConfig struct {
	Trader   *Trader
	AIModel  *AIModel
	Exchange *Exchange
	Strategy *Strategy
}

func (s *TraderStore) initTables() error {
	// For PostgreSQL with existing table, skip AutoMigrate
	if s.db.Dialector.Name() == "postgres" {
		var tableExists int64
		s.db.Raw(`SELECT COUNT(*) FROM information_schema.tables WHERE table_name = 'traders'`).Scan(&tableExists)
		if tableExists > 0 {
			if err := s.ensureTraderConfigurationColumns(); err != nil {
				return fmt.Errorf("failed to migrate trader configuration columns: %w", err)
			}
			return s.dropLegacyTraderColumns()
		}
	}
	// Use GORM AutoMigrate
	if err := s.db.AutoMigrate(&Trader{}); err != nil {
		return fmt.Errorf("failed to migrate traders table: %w", err)
	}
	return s.dropLegacyTraderColumns()
}

func (s *TraderStore) ensureTraderConfigurationColumns() error {
	for _, columnName := range []string{
		"InvertSignals",
		"StartupDelayMinutes",
		"FallbackModelNames",
		"FallbackAIModelIDs",
		"PrimaryModelName",
	} {
		if s.db.Migrator().HasColumn(&Trader{}, columnName) {
			continue
		}
		if err := s.db.Migrator().AddColumn(&Trader{}, columnName); err != nil {
			return fmt.Errorf("failed to add %s: %w", columnName, err)
		}
	}
	return nil
}

// ensureInvertSignalsColumn remains for migration tests and older callers.
func (s *TraderStore) ensureInvertSignalsColumn() error {
	return s.ensureTraderConfigurationColumns()
}

func (s *TraderStore) dropLegacyTraderColumns() error {
	for _, columnName := range []string{"btc_eth_leverage", "altcoin_leverage", "use_coin_pool", "use_oi_top"} {
		if !s.db.Migrator().HasColumn("traders", columnName) {
			continue
		}
		statement := fmt.Sprintf("ALTER TABLE traders DROP COLUMN %s", columnName)
		if err := s.db.Exec(statement).Error; err != nil {
			return fmt.Errorf("failed to drop legacy trader column %s: %w", columnName, err)
		}
	}
	return nil
}

// Create creates trader
func (s *TraderStore) Create(trader *Trader) error {
	return s.db.Create(trader).Error
}

// List gets user's trader list
func (s *TraderStore) List(userID string) ([]*Trader, error) {
	var traders []*Trader
	err := s.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Find(&traders).Error
	if err != nil {
		return nil, err
	}
	return traders, nil
}

// UpdateStatus updates trader running status
func (s *TraderStore) UpdateStatus(userID, id string, isRunning bool) error {
	return s.db.Model(&Trader{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("is_running", isRunning).Error
}

// UpdateShowInCompetition updates trader competition visibility
func (s *TraderStore) UpdateShowInCompetition(userID, id string, showInCompetition bool) error {
	return s.db.Model(&Trader{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("show_in_competition", showInCompetition).Error
}

// Update updates trader configuration
func (s *TraderStore) Update(trader *Trader) error {
	fmt.Printf("📝 TraderStore.Update: ID=%s, Name=%s, AIModelID=%s, StrategyID=%s\n",
		trader.ID, trader.Name, trader.AIModelID, trader.StrategyID)

	updates := map[string]interface{}{
		"name":                   trader.Name,
		"ai_model_id":            trader.AIModelID,
		"primary_model_name":     trader.PrimaryModelName,
		"exchange_id":            trader.ExchangeID,
		"strategy_id":            trader.StrategyID,
		"execution_mode":         trader.ExecutionMode,
		"is_cross_margin":        trader.IsCrossMargin,
		"show_in_competition":    trader.ShowInCompetition,
		"invert_signals":         trader.InvertSignals,
		"trading_symbols":        trader.TradingSymbols,
		"custom_prompt":          trader.CustomPrompt,
		"override_base_prompt":   trader.OverrideBasePrompt,
		"system_prompt_template": trader.SystemPromptTemplate,
		"startup_delay_minutes":  trader.StartupDelayMinutes,
		"fallback_model_names":   trader.FallbackModelNames,
		"fallback_ai_model_ids":  trader.FallbackAIModelIDs,
	}

	// Only update these if > 0
	if trader.InitialBalance > 0 {
		updates["initial_balance"] = trader.InitialBalance
	}
	if trader.ScanIntervalMinutes > 0 {
		updates["scan_interval_minutes"] = trader.ScanIntervalMinutes
		fmt.Printf("📊 TraderStore.Update: scan_interval_minutes=%d will be saved\n", trader.ScanIntervalMinutes)
	} else {
		fmt.Printf("⚠️ TraderStore.Update: scan_interval_minutes=%d (<=0, NOT updating)\n", trader.ScanIntervalMinutes)
	}

	return s.db.Model(&Trader{}).
		Where("id = ? AND user_id = ?", trader.ID, trader.UserID).
		Updates(updates).Error
}

// UpdateInitialBalance updates initial balance
func (s *TraderStore) UpdateInitialBalance(userID, id string, newBalance float64) error {
	return s.db.Model(&Trader{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("initial_balance", newBalance).Error
}

// UpdateCustomPrompt updates custom prompt
func (s *TraderStore) UpdateCustomPrompt(userID, id string, customPrompt string, overrideBase bool) error {
	return s.db.Model(&Trader{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"custom_prompt":        customPrompt,
			"override_base_prompt": overrideBase,
		}).Error
}

// Delete deletes trader and associated data
func (s *TraderStore) Delete(userID, id string) error {
	// Delete associated equity snapshots first
	s.db.Where("trader_id = ?", id).Delete(&EquitySnapshot{})

	// Delete the trader
	return s.db.Where("id = ? AND user_id = ?", id, userID).Delete(&Trader{}).Error
}

// GetFullConfig gets trader full configuration
func (s *TraderStore) GetFullConfig(userID, traderID string) (*TraderFullConfig, error) {
	var trader Trader
	err := s.db.Where("id = ? AND user_id = ?", traderID, userID).First(&trader).Error
	if err != nil {
		return nil, err
	}

	// Get AI model
	var aiModel AIModel
	err = s.db.Where("id = ? AND user_id = ?", trader.AIModelID, userID).First(&aiModel).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get AI model: %w", err)
	}

	// Get exchange
	var exchange Exchange
	err = s.db.Where("id = ? AND user_id = ?", trader.ExchangeID, userID).First(&exchange).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get exchange: %w", err)
	}

	// Load associated strategy
	var strategy *Strategy
	if trader.StrategyID != "" {
		strategy, _ = s.getStrategyByID(userID, trader.StrategyID)
	}
	// If no associated strategy, get user's active strategy or default strategy
	if strategy == nil {
		strategy, _ = s.getActiveOrDefaultStrategy(userID)
	}

	return &TraderFullConfig{
		Trader:   &trader,
		AIModel:  &aiModel,
		Exchange: &exchange,
		Strategy: strategy,
	}, nil
}

// getStrategyByID internal method: gets strategy by ID
func (s *TraderStore) getStrategyByID(userID, strategyID string) (*Strategy, error) {
	var strategy Strategy
	err := s.db.Where("id = ? AND (user_id = ? OR is_default = ?)", strategyID, userID, true).
		First(&strategy).Error
	if err != nil {
		return nil, err
	}
	return &strategy, nil
}

// getActiveOrDefaultStrategy internal method: gets user's active strategy or system default strategy
func (s *TraderStore) getActiveOrDefaultStrategy(userID string) (*Strategy, error) {
	var strategy Strategy

	// First try to get user's active strategy
	err := s.db.Where("user_id = ? AND is_active = ?", userID, true).First(&strategy).Error
	if err == nil {
		return &strategy, nil
	}

	// Fallback to system default strategy
	err = s.db.Where("is_default = ?", true).First(&strategy).Error
	if err != nil {
		return nil, err
	}
	return &strategy, nil
}

// GetByID gets a trader by ID without requiring userID (for public APIs)
func (s *TraderStore) GetByID(traderID string) (*Trader, error) {
	var trader Trader
	err := s.db.Where("id = ?", traderID).First(&trader).Error
	if err != nil {
		return nil, err
	}
	return &trader, nil
}

// ListAll gets all traders
func (s *TraderStore) ListAll() ([]*Trader, error) {
	var traders []*Trader
	err := s.db.Order("created_at DESC").Find(&traders).Error
	if err != nil {
		return nil, err
	}
	return traders, nil
}

// ListByExchangeID gets traders that use a specific exchange
func (s *TraderStore) ListByExchangeID(userID, exchangeID string) ([]*Trader, error) {
	var traders []*Trader
	err := s.db.Where("user_id = ? AND exchange_id = ?", userID, exchangeID).Find(&traders).Error
	if err != nil {
		return nil, err
	}
	return traders, nil
}

// ListByAIModelID gets traders that use a specific AI model
func (s *TraderStore) ListByAIModelID(userID, aiModelID string) ([]*Trader, error) {
	var traders []*Trader
	err := s.db.Where("user_id = ? AND ai_model_id = ?", userID, aiModelID).Find(&traders).Error
	if err != nil {
		return nil, err
	}
	return traders, nil
}
