package store

import (
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"testing"
)

func TestPositionInitMigratesLegacyGlobalIdentityWithoutLosingHistory(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&TraderPosition{}); err != nil {
		t.Fatal(err)
	}
	if err = db.Exec("CREATE UNIQUE INDEX idx_positions_exchange_pos_unique ON trader_positions(exchange_id,exchange_position_id) WHERE exchange_position_id != ''").Error; err != nil {
		t.Fatal(err)
	}
	old := &TraderPosition{TraderID: "old-trader", ExchangeID: "paper", ExchangePositionID: "paper_SKHYUSDT_long_11", Symbol: "SKHYUSDT", Side: "long", Quantity: 1, EntryPrice: 100, EntryTime: 1, ExitTime: 2, RealizedPnL: 5, Status: "CLOSED"}
	if err = db.Create(old).Error; err != nil {
		t.Fatal(err)
	}
	s := NewPositionStore(db)
	for i := 0; i < 2; i++ {
		if err = s.InitTables(); err != nil {
			t.Fatal(err)
		}
	}
	newer := &TraderPosition{TraderID: "new-trader", ExchangeID: "paper", ExchangePositionID: old.ExchangePositionID, Symbol: old.Symbol, Side: "long", Quantity: 2, EntryPrice: 100, EntryTime: 3, ExitTime: 4, RealizedPnL: -1, Status: "CLOSED"}
	if err = s.RecordClosedTrade(newer); err != nil {
		t.Fatalf("different trader must not collide with old history: %v", err)
	}
	if err = s.RecordClosedTrade(newer); err != nil {
		t.Fatalf("same-trader replay must be idempotent: %v", err)
	}
	var rows []TraderPosition
	if err = db.Order("trader_id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("history rows=%d, want 2", len(rows))
	}
	var preserved TraderPosition
	if err = db.First(&preserved, old.ID).Error; err != nil {
		t.Fatal(err)
	}
	if preserved.TraderID != old.TraderID || preserved.RealizedPnL != 5 {
		t.Fatalf("old history changed: %+v", preserved)
	}
}
