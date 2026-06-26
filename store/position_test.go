package store

import (
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestGetOpenPositionBySymbolMatchesSideCaseInsensitively(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}

	positions := NewPositionStore(db)
	if err := positions.InitTables(); err != nil {
		t.Fatalf("init position table: %v", err)
	}

	entryTime := time.Now().Add(-5 * time.Minute).UnixMilli()
	if err := positions.Create(&TraderPosition{
		TraderID:   "trader-1",
		Symbol:     "AAVEUSDT",
		Side:       "LONG",
		Quantity:   0.27,
		EntryPrice: 88.519,
		EntryTime:  entryTime,
	}); err != nil {
		t.Fatalf("create position: %v", err)
	}

	got, err := positions.GetOpenPositionBySymbol("trader-1", "AAVEUSDT", "long")
	if err != nil {
		t.Fatalf("get open position: %v", err)
	}
	if got == nil {
		t.Fatal("expected open position")
	}
	if got.EntryTime != entryTime {
		t.Fatalf("entry time mismatch: got %d want %d", got.EntryTime, entryTime)
	}
}
