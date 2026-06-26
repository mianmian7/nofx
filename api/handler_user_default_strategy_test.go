package api

import (
	"testing"

	"github.com/google/uuid"
	"nofx/store"
)

func TestCreateDefaultStrategiesUsesOneReadyToRunClaw402Preset(t *testing.T) {
	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	s := &Server{store: st}
	userID := "user-us-stock-presets"
	if err := s.createDefaultStrategies(userID, "zh"); err != nil {
		t.Fatalf("createDefaultStrategies failed: %v", err)
	}

	strategies, err := st.Strategy().List(userID)
	if err != nil {
		t.Fatalf("List strategies failed: %v", err)
	}
	if len(strategies) != 1 {
		t.Fatalf("expected 1 default strategy, got %d", len(strategies))
	}

	byName := map[string]*store.Strategy{}
	activeCount := 0
	for _, strategy := range strategies {
		byName[strategy.Name] = strategy
		if strategy.IsActive {
			activeCount++
		}
		if strategy.Name == "均衡策略" || strategy.Name == "稳健策略" || strategy.Name == "积极策略" {
			t.Fatalf("legacy crypto-style default strategy still present: %s", strategy.Name)
		}
	}
	if activeCount != 1 {
		t.Fatalf("expected exactly one active strategy, got %d", activeCount)
	}

	defaultStrategy := byName["NOFX Claw402 自动策略"]
	if defaultStrategy == nil || !defaultStrategy.IsActive {
		t.Fatalf("NOFX Claw402 自动策略 should exist and be active")
	}
	trendCfg, err := defaultStrategy.ParseConfig()
	if err != nil {
		t.Fatalf("default ParseConfig failed: %v", err)
	}
	if trendCfg.CoinSource.SourceType != "vergex_signal" || trendCfg.CoinSource.VergexLimit != 10 || trendCfg.CoinSource.VergexMarketType != "all" {
		t.Fatalf("default strategy should use the Claw402/Vergex all-market signal ranking, got %+v", trendCfg.CoinSource)
	}
	if trendCfg.CoinSource.UseAI500 || trendCfg.RiskControl.MaxPositions > 2 || trendCfg.RiskControl.MaxMarginUsage > 0.45 {
		t.Fatalf("default strategy should be low-risk Claw402/Vergex native, got coin=%+v risk=%+v", trendCfg.CoinSource, trendCfg.RiskControl)
	}
	if trendCfg.RiskControl.BTCETHMaxLeverage != 10 || trendCfg.RiskControl.AltcoinMaxLeverage != 10 {
		t.Fatalf("default strategy should use 10x leverage for all Claw402 opens, got risk=%+v", trendCfg.RiskControl)
	}
}

func TestCreateDefaultStrategiesMigratesLegacyPresetsWithoutOverridingActiveCustom(t *testing.T) {
	st, err := store.New(t.TempDir() + "/nofx.db")
	if err != nil {
		t.Fatalf("store.New failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	userID := "user-existing-custom"
	legacyCfg := store.GetDefaultStrategyConfig("zh")
	legacy := &store.Strategy{
		ID:          uuid.New().String(),
		UserID:      userID,
		Name:        "均衡策略",
		Description: "legacy",
		IsActive:    false,
	}
	if err := legacy.SetConfig(&legacyCfg); err != nil {
		t.Fatalf("legacy SetConfig failed: %v", err)
	}
	if err := st.Strategy().Create(legacy); err != nil {
		t.Fatalf("create legacy failed: %v", err)
	}

	custom := &store.Strategy{
		ID:          uuid.New().String(),
		UserID:      userID,
		Name:        "aa",
		Description: "user custom active strategy",
		IsActive:    true,
	}
	if err := custom.SetConfig(&legacyCfg); err != nil {
		t.Fatalf("custom SetConfig failed: %v", err)
	}
	if err := st.Strategy().Create(custom); err != nil {
		t.Fatalf("create custom failed: %v", err)
	}

	s := &Server{store: st}
	if err := s.createDefaultStrategies(userID, "zh"); err != nil {
		t.Fatalf("createDefaultStrategies failed: %v", err)
	}
	if err := s.createDefaultStrategies(userID, "zh"); err != nil {
		t.Fatalf("second createDefaultStrategies should be idempotent: %v", err)
	}

	strategies, err := st.Strategy().List(userID)
	if err != nil {
		t.Fatalf("List strategies failed: %v", err)
	}
	byName := map[string]int{}
	activeNames := []string{}
	for _, strategy := range strategies {
		byName[strategy.Name]++
		if strategy.IsActive {
			activeNames = append(activeNames, strategy.Name)
		}
	}
	if byName["均衡策略"] != 0 {
		t.Fatalf("legacy preset should be removed, got names=%+v", byName)
	}
	if byName["NOFX Claw402 自动策略"] != 1 {
		t.Fatalf("expected exactly one NOFX Claw402 自动策略, got names=%+v", byName)
	}
	if len(activeNames) != 1 || activeNames[0] != "aa" {
		t.Fatalf("existing active custom strategy should stay the only active one, got %+v", activeNames)
	}
}
