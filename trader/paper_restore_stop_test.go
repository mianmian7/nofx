package trader

import (
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/store"
)

func TestPersistentPaperRestoresValidatedMovingStop(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		for _, breakeven := range []bool{false, true} {
			for _, relation := range []string{"safe", "equal", "crossed"} {
				name := side + "/" + relation
				if breakeven {
					name += "/breakeven"
				}
				t.Run(name, func(t *testing.T) {
					st, err := store.New(filepath.Join(t.TempDir(), "restore.db"))
					if err != nil {
						t.Fatal(err)
					}
					now := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
					cfg := PaperBrokerConfig{InitialBalance: 1000, Clock: func() time.Time { return now }}
					prices := fixedPaperPriceSource{"BTCUSDT": 100}
					first, err := NewPersistentPaperBroker(cfg, prices, st.Paper(), "restore-stop", nil)
					if err != nil {
						t.Fatal(err)
					}
					sl, tp, mark, newSL := 90.0, 130.0, 120.0, 105.0
					if side == "short" {
						sl, tp, mark, newSL = 110, 70, 80, 95
					}
					if breakeven {
						newSL = 100
					}
					if _, err := first.ExecuteDecision(&kernel.Decision{Symbol: "BTCUSDT", Action: "open_" + side, PositionSizeUSD: 100, Leverage: 2, StopLoss: sl, TakeProfit: tp}); err != nil {
						t.Fatal(err)
					}
					now = now.Add(time.Minute)
					prices["BTCUSDT"] = mark
					if _, err := first.ExecuteDecision(&kernel.Decision{Symbol: "BTCUSDT", Action: "update_position", NewStopLoss: newSL}); err != nil {
						t.Fatal(err)
					}
					raw, _, err := st.Paper().LoadPaperState("restore-stop")
					if err != nil {
						t.Fatal(err)
					}
					var state paperBrokerState
					if err := json.Unmarshal(raw, &state); err != nil {
						t.Fatal(err)
					}
					if relation == "equal" {
						state.Marks["BTCUSDT"] = newSL
					}
					if relation == "crossed" {
						state.Marks["BTCUSDT"] = newSL - 1
						if side == "short" {
							state.Marks["BTCUSDT"] = newSL + 1
						}
					}
					raw, err = json.Marshal(state)
					if err != nil {
						t.Fatal(err)
					}
					if err := st.Paper().SavePaperState("restore-stop", raw); err != nil {
						t.Fatal(err)
					}
					restored, err := NewPersistentPaperBroker(cfg, prices, st.Paper(), "restore-stop", nil)
					if err != nil {
						t.Fatal(err)
					}
					positions, err := restored.GetPositions()
					if err != nil || len(positions) != 1 || len(restored.QuarantinedPositions()) != 0 {
						t.Fatalf("position lost: positions=%v warnings=%v err=%v", positions, restored.RestoreWarnings(), err)
					}
					saved, _, err := st.Paper().LoadPaperState("restore-stop")
					if err != nil {
						t.Fatal(err)
					}
					if string(saved) != string(raw) {
						t.Fatal("restore rewrote a valid ledger")
					}
					cross := newSL - 1
					if side == "short" {
						cross = newSL + 1
					}
					exits, err := restored.ProcessPrice("BTCUSDT", cross, now.Add(time.Minute))
					if err != nil || len(exits) != 1 || exits[0].Action != "stop_loss" {
						t.Fatalf("fresh stop not settled: %v %v", exits, err)
					}
					exits, err = restored.ProcessPrice("BTCUSDT", cross, now.Add(2*time.Minute))
					if err != nil || len(exits) != 0 {
						t.Fatalf("duplicate stop settlement: %v %v", exits, err)
					}
				})
			}
		}
	}
}

func TestPaperLegacyMovingStopRequiresExecutedEvidence(t *testing.T) {
	for _, mode := range []string{"success", "partial_position", "failed", "degraded", "proposal", "wrong_symbol", "previous_position", "wrong_level", "newer_level", "wrong_side", "bad_mark", "bad_quantity"} {
		t.Run(mode, func(t *testing.T) {
			st, err := store.New(filepath.Join(t.TempDir(), "legacy.db"))
			if err != nil {
				t.Fatal(err)
			}
			entryTime := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
			p := PaperPosition{Symbol: "BTCUSDT", Side: "long", Quantity: 1, EntryPrice: 100, Leverage: 2, StopLoss: 105, TakeProfit: 130, EntryTime: entryTime, InitialMargin: 50}
			if mode == "partial_position" {
				p.Quantity = .5
				p.InitialMargin = 25
			}
			state := paperBrokerState{InitialBalance: 1000, Balance: 1000, Positions: map[string]PaperPosition{"BTCUSDT:long": p}, Marks: map[string]float64{"BTCUSDT": 104}, NextID: 1}
			raw, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Paper().SavePaperState("legacy", raw); err != nil {
				t.Fatal(err)
			}
			a := store.DecisionAction{Action: "update_position", Symbol: "BTCUSDT", Quantity: 1, Price: 120, StopLoss: 105, Timestamp: entryTime.Add(time.Minute), Success: true}
			switch mode {
			case "failed":
				a.Success = false
			case "degraded":
				a.Degraded = true
			case "proposal":
				a.Action = "wait"
			case "wrong_symbol":
				a.Symbol = "ETHUSDT"
			case "previous_position":
				a.Timestamp = entryTime.Add(-time.Minute)
			case "wrong_level":
				a.StopLoss = 106
			case "wrong_side":
				a.Price = 90
			case "bad_mark":
				a.Price = 0
			case "bad_quantity":
				a.Quantity = 0
			}
			if err := st.Decision().LogDecision(&store.DecisionRecord{TraderID: "legacy", Timestamp: a.Timestamp, Decisions: []store.DecisionAction{a}}); err != nil {
				t.Fatal(err)
			}
			if mode == "newer_level" {
				a.StopLoss = 106
				a.Timestamp = a.Timestamp.Add(time.Minute)
				if err := st.Decision().LogDecision(&store.DecisionRecord{TraderID: "legacy", Timestamp: a.Timestamp, Decisions: []store.DecisionAction{a}}); err != nil {
					t.Fatal(err)
				}
			}
			broker, err := NewPersistentPaperBroker(PaperBrokerConfig{InitialBalance: 1000}, fixedPaperPriceSource{"BTCUSDT": 104}, st.Paper(), "legacy", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := mode == "success" || mode == "partial_position"
			if (len(broker.positions) == 1) != want {
				t.Fatalf("active=%d want=%v warnings=%v", len(broker.positions), want, broker.RestoreWarnings())
			}
			if want {
				saved, _, err := st.Paper().LoadPaperState("legacy")
				if err != nil {
					t.Fatal(err)
				}
				var migrated paperBrokerState
				if err := json.Unmarshal(saved, &migrated); err != nil {
					t.Fatal(err)
				}
				position := migrated.Positions["BTCUSDT:long"]
				if position.StopLossEvidence == nil {
					t.Fatal("verified legacy evidence was not persisted")
				}
				position.StopLossEvidence = nil
				migrated.Positions["BTCUSDT:long"] = position
				financial, err := json.Marshal(migrated)
				if err != nil || string(financial) != string(raw) {
					t.Fatal("legacy proof migration changed financial state")
				}
				if err := st.GormDB().Where("trader_id = ?", "legacy").Delete(&store.DecisionRecordDB{}).Error; err != nil {
					t.Fatal(err)
				}
				again, err := NewPersistentPaperBroker(PaperBrokerConfig{InitialBalance: 1000}, fixedPaperPriceSource{"BTCUSDT": 104}, st.Paper(), "legacy", nil)
				if err != nil || len(again.positions) != 1 {
					t.Fatalf("log retention lost position on second restore: %v", err)
				}
			}
		})
	}
}

func TestPaperMovingStopEvidenceCannotRelaxEntryOrInvalidState(t *testing.T) {
	entry := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	base := PaperPosition{Symbol: "BTCUSDT", Side: "long", Quantity: 1, EntryPrice: 100, Leverage: 2, StopLoss: 105, TakeProfit: 130, EntryTime: entry,
		StopLossEvidence: &PaperStopLossEvidence{Symbol: "BTCUSDT", Side: "long", EntryPrice: 100, EntryTime: entry, StopLoss: 105, MarkPrice: 120, UpdatedAt: entry.Add(time.Minute)}}
	for _, mode := range []string{"safe_mark_only", "wrong_symbol", "wrong_side", "wrong_entry", "wrong_generation", "wrong_level", "old_update", "nan_mark", "nan_stop", "invalid_side", "invalid_quantity", "invalid_leverage", "invalid_tp"} {
		t.Run(mode, func(t *testing.T) {
			p := base
			proof := *base.StopLossEvidence
			p.StopLossEvidence = &proof
			switch mode {
			case "safe_mark_only":
				p.StopLossEvidence = nil
			case "wrong_symbol":
				proof.Symbol = "ETHUSDT"
			case "wrong_side":
				proof.Side = "short"
			case "wrong_entry":
				proof.EntryPrice = 99
			case "wrong_generation":
				proof.EntryTime = entry.Add(-time.Hour)
			case "wrong_level":
				proof.StopLoss = 106
			case "old_update":
				proof.UpdatedAt = entry.Add(-time.Minute)
			case "nan_mark":
				proof.MarkPrice = math.NaN()
			case "nan_stop":
				p.StopLoss = math.NaN()
			case "invalid_side":
				p.Side = "unknown"
			case "invalid_quantity":
				p.Quantity = -1
			case "invalid_leverage":
				p.Leverage = 0
			case "invalid_tp":
				p.TakeProfit = 90
			}
			if err := validateRestoredPaperPosition(p); err == nil {
				t.Fatal("invalid state admitted")
			}
		})
	}
	if err := validatePaperExitPrices("open_long", 100, 105, 130); err == nil {
		t.Fatal("initial entry guard relaxed")
	}
	if err := validatePaperExitPrices("open_short", 100, 95, 70); err == nil {
		t.Fatal("initial short entry guard relaxed")
	}
}
