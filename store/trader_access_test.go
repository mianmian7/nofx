package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTraderAccessTest(t *testing.T) (*gorm.DB, *TraderStore) {
	t.Helper()
	db := openTraderTestDatabase(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&Trader{}, &EquitySnapshot{}); err != nil {
		t.Fatal(err)
	}
	s := NewTraderStore(db)
	for _, id := range []string{"alice", "bob"} {
		if err := s.Create(&Trader{ID: id + "-trader", UserID: id, Name: id, AIModelID: "model", ExchangeID: "paper", InitialBalance: 100}); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&EquitySnapshot{TraderID: id + "-trader", Timestamp: time.Now(), TotalEquity: 100}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, s
}

func TestTraderMutationsRejectUnownedAndMissingIDs(t *testing.T) {
	for _, target := range []string{"bob-trader", "missing"} {
		for _, operation := range []string{"delete", "prompt", "competition", "status", "initial balance", "configuration"} {
			t.Run(operation+"/"+target, func(t *testing.T) {
				db, s := setupTraderAccessTest(t)
				var err error
				switch operation {
				case "delete":
					err = s.Delete("alice", target)
				case "prompt":
					err = s.UpdateCustomPrompt("alice", target, "untrusted", true)
				case "competition":
					err = s.UpdateShowInCompetition("alice", target, false)
				case "status":
					err = s.UpdateStatus("alice", target, true)
				case "initial balance":
					err = s.UpdateInitialBalance("alice", target, 999)
				case "configuration":
					err = s.Update(&Trader{ID: target, UserID: "alice", Name: "untrusted"})
				}
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					t.Errorf("mutation error = %v, want record not found", err)
				}
				var count int64
				if err := db.Model(&EquitySnapshot{}).Count(&count).Error; err != nil || count != 2 {
					t.Fatalf("equity histories changed: count=%d err=%v", count, err)
				}
				owner, err := s.GetByID("bob-trader")
				if err != nil || owner.CustomPrompt != "" || !owner.ShowInCompetition || owner.IsRunning || owner.InitialBalance != 100 || owner.Name != "bob" {
					t.Fatalf("other account's trader changed: %+v, %v", owner, err)
				}
			})
		}
	}
}

func TestTraderDeleteIsAtomicAndOwnerScoped(t *testing.T) {
	db, s := setupTraderAccessTest(t)
	if err := db.Exec(`CREATE TRIGGER fail_equity_delete BEFORE DELETE ON trader_equity_snapshots BEGIN SELECT RAISE(ABORT, 'injected failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("alice", "alice-trader"); err == nil {
		t.Fatal("snapshot deletion failure must be returned")
	}
	if _, err := s.GetByID("alice-trader"); err != nil {
		t.Fatalf("failed delete did not preserve trader: %v", err)
	}
	if err := db.Exec("DROP TRIGGER fail_equity_delete").Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("alice", "alice-trader"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetByID("alice-trader"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("owner's trader was not deleted: %v", err)
	}
	var snapshots []EquitySnapshot
	if err := db.Find(&snapshots).Error; err != nil || len(snapshots) != 1 || snapshots[0].TraderID != "bob-trader" {
		t.Fatalf("wrong remaining equity snapshots: %+v, %v", snapshots, err)
	}
}

func TestTraderOwnerCanRepeatConfigUpdates(t *testing.T) {
	_, s := setupTraderAccessTest(t)
	for range 2 {
		if err := s.UpdateCustomPrompt("alice", "alice-trader", "owner prompt", true); err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateShowInCompetition("alice", "alice-trader", false); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetByID("alice-trader")
	if err != nil || got.CustomPrompt != "owner prompt" || !got.OverrideBasePrompt || got.ShowInCompetition {
		t.Fatalf("owner updates were not saved: %+v, %v", got, err)
	}
}

func TestTraderLookupIsOwnerScopedAndDefaultsToLatest(t *testing.T) {
	_, s := setupTraderAccessTest(t)
	if err := s.Create(&Trader{ID: "alice-latest", UserID: "alice", Name: "Latest", AIModelID: "model", ExchangeID: "paper", CreatedAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bob-trader", "missing", ""} {
		if _, err := s.GetForUser("alice", id); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("lookup %q must reject an unowned or unspecified ID: %v", id, err)
		}
	}
	got, err := s.GetForUser("alice", "alice-trader")
	if err != nil || got.ID != "alice-trader" {
		t.Fatalf("explicit owner lookup: %+v, %v", got, err)
	}
	got, err = s.GetLatestForUser("alice")
	if err != nil || got.ID != "alice-latest" {
		t.Fatalf("default selection: %+v, %v", got, err)
	}
	if _, err := s.GetLatestForUser("empty-user"); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("empty user must not resolve another account's trader: %v", err)
	}
}

func BenchmarkTraderOwnerLookup(b *testing.B) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		b.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		b.Fatal(err)
	}
	defer sqlDB.Close()
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&Trader{}); err != nil {
		b.Fatal(err)
	}
	rows := make([]Trader, 1000)
	for i := range rows {
		rows[i] = Trader{ID: fmt.Sprintf("trader-%04d", i), UserID: "owner", Name: "Paper", AIModelID: "model", ExchangeID: "paper"}
	}
	if err := db.CreateInBatches(rows, 100).Error; err != nil {
		b.Fatal(err)
	}
	s := NewTraderStore(db)
	b.Run("list_all_1000", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := s.List("owner"); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("get_one", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := s.GetForUser("owner", "trader-0999"); err != nil {
				b.Fatal(err)
			}
		}
	})
}
