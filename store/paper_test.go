package store

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestResetTraderAccountOnlyClearsOwnedPaperTrader(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&Trader{}, &PaperAccountState{}, &DecisionRecordDB{}, &EquitySnapshot{},
		&TraderOrder{}, &TraderFill{}, &TraderPosition{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}

	traders := []Trader{
		{ID: "target", UserID: "owner", Name: "target", ExecutionMode: "paper", InitialBalance: 2000},
		{ID: "other", UserID: "owner", Name: "other", ExecutionMode: "paper", InitialBalance: 3000},
		{ID: "live", UserID: "owner", Name: "live", ExecutionMode: "live", InitialBalance: 4000},
	}
	if err := db.Create(&traders).Error; err != nil {
		t.Fatal(err)
	}
	for _, traderID := range []string{"target", "other"} {
		if err := db.Create(&PaperAccountState{TraderID: traderID, StateJSON: []byte(`{"balance":1}`)}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&DecisionRecordDB{TraderID: traderID}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&EquitySnapshot{TraderID: traderID}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&TraderOrder{TraderID: traderID, ExchangeOrderID: traderID + "-order"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&TraderFill{TraderID: traderID, ExchangeOrderID: traderID + "-order", ExchangeTradeID: traderID + "-fill"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&TraderPosition{TraderID: traderID}).Error; err != nil {
			t.Fatal(err)
		}
	}

	paper := NewPaperStore(db)
	if err := paper.ResetTraderAccount("owner", "target"); err != nil {
		t.Fatal(err)
	}

	for _, model := range []interface{}{
		&PaperAccountState{}, &DecisionRecordDB{}, &EquitySnapshot{},
		&TraderOrder{}, &TraderFill{}, &TraderPosition{},
	} {
		var targetCount, otherCount int64
		if err := db.Model(model).Where("trader_id = ?", "target").Count(&targetCount).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(model).Where("trader_id = ?", "other").Count(&otherCount).Error; err != nil {
			t.Fatal(err)
		}
		if targetCount != 0 || otherCount != 1 {
			t.Fatalf("unexpected reset counts for %T: target=%d other=%d", model, targetCount, otherCount)
		}
	}

	var traderCount int64
	if err := db.Model(&Trader{}).Where("id = ?", "target").Count(&traderCount).Error; err != nil {
		t.Fatal(err)
	}
	if traderCount != 1 {
		t.Fatalf("target trader config was removed")
	}
	if err := paper.ResetTraderAccount("owner", "live"); err == nil {
		t.Fatal("expected live trader reset to be rejected")
	}
	if err := paper.ResetTraderAccount("someone-else", "other"); err == nil {
		t.Fatal("expected cross-user reset to be rejected")
	}
}

func TestUpdateTraderAndResetAccountRollsBackConfigWhenLedgerDeleteFails(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&Trader{}, &PaperAccountState{}, &DecisionRecordDB{}, &EquitySnapshot{},
		&TraderOrder{}, &TraderFill{}, &TraderPosition{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatal(err)
	}

	original := &Trader{
		ID: "target", UserID: "owner", Name: "before reset", AIModelID: "model",
		ExchangeID: "exchange", ExecutionMode: "paper", InitialBalance: 100,
		ScanIntervalMinutes: 5,
	}
	if err := db.Create(original).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&PaperAccountState{TraderID: original.ID, StateJSON: []byte(`{"balance":100}`)}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&DecisionRecordDB{TraderID: original.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&EquitySnapshot{TraderID: original.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TraderOrder{TraderID: original.ID, ExchangeOrderID: "target-order"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TraderFill{TraderID: original.ID, ExchangeOrderID: "target-order", ExchangeTradeID: "target-fill"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&TraderPosition{TraderID: original.ID}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`
		CREATE TRIGGER fail_paper_reset_positions
		BEFORE DELETE ON trader_positions
		WHEN OLD.trader_id = 'target'
		BEGIN
			SELECT RAISE(ABORT, 'injected paper reset failure');
		END;
	`).Error; err != nil {
		t.Fatal(err)
	}

	updated := *original
	updated.Name = "after reset"
	updated.InitialBalance = 200
	if err := NewPaperStore(db).UpdateTraderAndResetAccount("owner", &updated); err == nil {
		t.Fatal("expected injected ledger deletion failure")
	}

	var persisted Trader
	if err := db.First(&persisted, "id = ?", original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Name != original.Name || persisted.InitialBalance != original.InitialBalance {
		t.Fatalf("trader config was not rolled back: %#v", persisted)
	}
	for _, model := range []interface{}{
		&PaperAccountState{}, &DecisionRecordDB{}, &EquitySnapshot{},
		&TraderOrder{}, &TraderFill{}, &TraderPosition{},
	} {
		var count int64
		if err := db.Model(model).Where("trader_id = ?", original.ID).Count(&count).Error; err != nil {
			t.Fatalf("count %T: %v", model, err)
		}
		if count != 1 {
			t.Fatalf("ledger row count for %T = %d, want rollback to preserve 1 row", model, count)
		}
	}
}
