package store

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openTraderTestDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	database, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	return database
}

func TestTraderStoreUpdatePersistsInvertSignals(t *testing.T) {
	database := openTraderTestDatabase(t)
	if err := database.AutoMigrate(&Trader{}); err != nil {
		t.Fatalf("migrate trader table: %v", err)
	}
	traderStore := NewTraderStore(database)
	trader := &Trader{
		ID:             "trader-1",
		UserID:         "user-1",
		Name:           "Inverse trader",
		AIModelID:      "model-1",
		ExchangeID:     "exchange-1",
		ExecutionMode:  "paper",
		InitialBalance: 100,
	}
	if err := traderStore.Create(trader); err != nil {
		t.Fatalf("create trader: %v", err)
	}

	trader.InvertSignals = true
	if err := traderStore.Update(trader); err != nil {
		t.Fatalf("update trader: %v", err)
	}

	var persisted Trader
	if err := database.First(&persisted, "id = ?", trader.ID).Error; err != nil {
		t.Fatalf("load trader: %v", err)
	}
	if !persisted.InvertSignals {
		t.Fatal("invert_signals was not persisted")
	}
}

func TestEnsureInvertSignalsColumnMigratesExistingTraderTable(t *testing.T) {
	database := openTraderTestDatabase(t)
	if err := database.Exec(`CREATE TABLE traders (id TEXT PRIMARY KEY, user_id TEXT NOT NULL)`).Error; err != nil {
		t.Fatalf("create legacy trader table: %v", err)
	}
	traderStore := NewTraderStore(database)

	if err := traderStore.ensureInvertSignalsColumn(); err != nil {
		t.Fatalf("add invert_signals column: %v", err)
	}
	if !database.Migrator().HasColumn(&Trader{}, "InvertSignals") {
		t.Fatal("invert_signals column is still missing after migration")
	}
}

func TestDropLegacyLeverageColumns(t *testing.T) {
	database := openTraderTestDatabase(t)
	if err := database.Exec(`CREATE TABLE traders (
		id TEXT PRIMARY KEY,
		user_id TEXT NOT NULL,
		btc_eth_leverage INTEGER,
		altcoin_leverage INTEGER
	)`).Error; err != nil {
		t.Fatalf("create legacy trader table: %v", err)
	}
	if err := NewTraderStore(database).dropLegacyLeverageColumns(); err != nil {
		t.Fatalf("drop legacy leverage columns: %v", err)
	}
	if database.Migrator().HasColumn("traders", "btc_eth_leverage") {
		t.Fatal("btc_eth_leverage column still exists")
	}
	if database.Migrator().HasColumn("traders", "altcoin_leverage") {
		t.Fatal("altcoin_leverage column still exists")
	}
}
