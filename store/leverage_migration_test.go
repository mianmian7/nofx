package store

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRiskControlConfigMigratesLegacyLeverageConservatively(t *testing.T) {
	var riskControl RiskControlConfig
	legacyJSON := `{"btc_eth_max_leverage":10,"altcoin_max_leverage":5}`
	if err := json.Unmarshal([]byte(legacyJSON), &riskControl); err != nil {
		t.Fatalf("unmarshal legacy risk control: %v", err)
	}
	if riskControl.MaxLeverage != 5 {
		t.Fatalf("migrated max leverage = %d, want conservative 5", riskControl.MaxLeverage)
	}

	serialized, err := json.Marshal(riskControl)
	if err != nil {
		t.Fatalf("marshal migrated risk control: %v", err)
	}
	serializedJSON := string(serialized)
	if !strings.Contains(serializedJSON, `"max_leverage":5`) {
		t.Fatalf("serialized risk control is missing max_leverage: %s", serializedJSON)
	}
	if strings.Contains(serializedJSON, "btc_eth_max_leverage") || strings.Contains(serializedJSON, "altcoin_max_leverage") {
		t.Fatalf("serialized risk control still contains legacy leverage fields: %s", serializedJSON)
	}
}

func TestRiskControlConfigPrefersExplicitUnifiedLeverage(t *testing.T) {
	var riskControl RiskControlConfig
	rawJSON := `{"max_leverage":20,"btc_eth_max_leverage":10,"altcoin_max_leverage":5}`
	if err := json.Unmarshal([]byte(rawJSON), &riskControl); err != nil {
		t.Fatalf("unmarshal mixed risk control: %v", err)
	}
	if riskControl.MaxLeverage != 20 {
		t.Fatalf("explicit max leverage = %d, want 20", riskControl.MaxLeverage)
	}
}

func TestStrategyStoreMigratesPersistedLegacyLeverageJSON(t *testing.T) {
	database := openTraderTestDatabase(t)
	if err := database.AutoMigrate(&Strategy{}); err != nil {
		t.Fatalf("migrate strategy table: %v", err)
	}
	legacyConfig := `{"strategy_type":"ai_trading","ai_config":{"risk_control":{"btc_eth_max_leverage":10,"altcoin_max_leverage":5}}}`
	strategy := Strategy{ID: "legacy-strategy", UserID: "user-1", Name: "legacy", Config: legacyConfig}
	if err := database.Create(&strategy).Error; err != nil {
		t.Fatalf("create legacy strategy: %v", err)
	}

	if err := NewStrategyStore(database).migrateUnifiedLeverageConfig(); err != nil {
		t.Fatalf("migrate persisted strategy: %v", err)
	}
	var persisted Strategy
	if err := database.First(&persisted, "id = ?", strategy.ID).Error; err != nil {
		t.Fatalf("load migrated strategy: %v", err)
	}
	if !strings.Contains(persisted.Config, `"max_leverage":5`) {
		t.Fatalf("migrated strategy is missing max_leverage: %s", persisted.Config)
	}
	if strings.Contains(persisted.Config, "btc_eth_max_leverage") || strings.Contains(persisted.Config, "altcoin_max_leverage") {
		t.Fatalf("migrated strategy still contains legacy leverage fields: %s", persisted.Config)
	}
}
