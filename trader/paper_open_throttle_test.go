package trader

import (
	"fmt"
	"nofx/kernel"
	"nofx/store"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func hourlyEntries(b *PaperBroker) []PaperPendingOrder {
	var out []PaperPendingOrder
	for _, o := range b.PendingOrders() {
		if !o.ReduceOnly {
			out = append(out, o)
		}
	}
	return out
}
func hourlyCount(t *testing.T, b *PaperBroker, want int) {
	t.Helper()
	if got := b.countRecentOpenEntries(b.now().Add(-time.Hour)); got != want {
		t.Fatalf("opening count=%d want=%d", got, want)
	}
}

func TestPaperHourlyOpenConcurrentAdmission(t *testing.T) {
	for _, maker := range []bool{false, true} {
		t.Run(fmt.Sprint(maker), func(t *testing.T) {
			b, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100, MakerFirst: maker, MaxOpensPerHour: 1}, slotSource())
			if err != nil {
				t.Fatal(err)
			}
			var wg sync.WaitGroup
			var accepted atomic.Int32
			for i := 0; i < 12; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					if _, err := slotOpen(b, fmt.Sprintf("COIN%dUSDT", i)); err == nil {
						accepted.Add(1)
					}
				}(i)
			}
			wg.Wait()
			if accepted.Load() != 1 {
				t.Fatalf("accepted %d", accepted.Load())
			}
			hourlyCount(t, b, 1)
		})
	}
}

func TestPaperHourlyOpenPartialRepriceCancelCloseAndRestart(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st, err := store.New(filepath.Join(t.TempDir(), "hour.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cfg := PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MaxOpensPerHour: 1, MakerMaxReprices: 3, Clock: func() time.Time { return now }}
	source := slotSource()
	b, err := NewPersistentPaperBroker(cfg, source, st.Paper(), "partial", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = slotOpen(b, "BTCUSDT"); err != nil {
		t.Fatal(err)
	}
	depth, _ := source.GetDepth("BTCUSDT", 5)
	firstFill := now.Add(time.Second)
	for i := 0; i < 2; i++ {
		now = firstFill.Add(time.Duration(i) * 30 * time.Minute)
		entry := hourlyEntries(b)[0]
		if _, err = b.applyMakerFill(entry.OrderID, .05, depth, now); err != nil {
			t.Fatal(err)
		}
		if _, err = b.expireOrRepriceMakerOrder(entry.OrderID, depth, now.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		hourlyCount(t, b, 1)
		b, err = NewPersistentPaperBroker(cfg, source, st.Paper(), "partial", nil)
		if err != nil {
			t.Fatal(err)
		}
		hourlyCount(t, b, 1)
	}
	entry := hourlyEntries(b)[0]
	if err = b.CancelOrder(entry.Symbol, fmt.Sprint(entry.OrderID)); err != nil {
		t.Fatal(err)
	}
	hourlyCount(t, b, 1)
	if _, err = slotOpen(b, "ETHUSDT"); err == nil {
		t.Fatal("partial cancellation erased an actual opening")
	}
	if _, err = b.ExecuteDecision(&kernel.Decision{Symbol: "BTCUSDT", Action: "close_long"}); err != nil {
		t.Fatal("exit blocked", err)
	}
	for _, o := range b.PendingOrders() {
		if o.Action == "close_long" {
			if _, err = b.applyMakerFill(o.OrderID, o.RemainingQuantity, depth, now); err != nil {
				t.Fatal(err)
			}
		}
	}
	if b.Snapshot().OpenPositions != 0 {
		t.Fatal("close failed")
	}
	hourlyCount(t, b, 1)
	now = firstFill.Add(time.Hour)
	hourlyCount(t, b, 0) // Later partial fills/reprices must not restart the clock.
	if _, err = slotOpen(b, "ETHUSDT"); err != nil {
		t.Fatal("hour boundary did not release logical opening", err)
	}
}

func TestPaperHourlyOpenOldUnfilledReservationsAndCancel(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MaxOpensPerHour: 1, Clock: func() time.Time { return now }}, slotSource())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = slotOpen(b, "BTCUSDT"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	hourlyCount(t, b, 1)
	if _, err = slotOpen(b, "ETHUSDT"); err == nil {
		t.Fatal("aged pending entry lost its reservation")
	}
	o := hourlyEntries(b)[0]
	if err = b.CancelOrder(o.Symbol, fmt.Sprint(o.OrderID)); err != nil {
		t.Fatal(err)
	}
	hourlyCount(t, b, 0)
	if _, err = slotOpen(b, "ETHUSDT"); err != nil {
		t.Fatal("unfilled cancellation did not release reservation", err)
	}
}

func TestPaperHourlyOpenRestoredOverLimitRevalidated(t *testing.T) {
	for _, stage := range []string{"fill", "reprice"} {
		t.Run(stage, func(t *testing.T) {
			st, err := store.New(filepath.Join(t.TempDir(), "limit.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			cfg := PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MaxOpensPerHour: 2, MakerMaxReprices: 2}
			source := slotSource()
			b, err := NewPersistentPaperBroker(cfg, source, st.Paper(), "lower", nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, sym := range []string{"BTCUSDT", "ETHUSDT"} {
				if _, err = slotOpen(b, sym); err != nil {
					t.Fatal(err)
				}
			}
			cfg.MaxOpensPerHour = 1
			b, err = NewPersistentPaperBroker(cfg, source, st.Paper(), "lower", nil)
			if err != nil {
				t.Fatal(err)
			}
			o := hourlyEntries(b)[0]
			depth, _ := source.GetDepth(o.Symbol, 5)
			if stage == "fill" {
				_, err = b.applyMakerFill(o.OrderID, o.RemainingQuantity, depth, b.now())
			} else {
				_, err = b.expireOrRepriceMakerOrder(o.OrderID, depth, b.now())
			}
			if err == nil || !strings.Contains(err.Error(), "throttle") {
				t.Fatalf("legacy over-limit entry admitted: %v", err)
			}
			hourlyCount(t, b, 1)
			if b.Snapshot().OpenPositions != 0 {
				t.Fatal("rejected entry mutated position")
			}
			o = hourlyEntries(b)[0]
			if _, err = b.applyMakerFill(o.OrderID, o.RemainingQuantity, depth, b.now()); err != nil {
				t.Fatal("remaining valid reservation blocked", err)
			}
		})
	}
}

func TestPaperHourlyOpenPersistenceFailuresKeepCorrectReservations(t *testing.T) {
	for _, operation := range []string{"place", "fill", "reprice", "cancel"} {
		t.Run(operation, func(t *testing.T) {
			st, err := store.New(filepath.Join(t.TempDir(), "fail.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer st.Close()
			ledger := &paperRiskFailingLedger{PaperLedger: st.Paper()}
			cfg := PaperBrokerConfig{InitialBalance: 100, MakerFirst: true, MaxOpensPerHour: 1, MakerMaxReprices: 2}
			source := slotSource()
			b, err := NewPersistentPaperBroker(cfg, source, ledger, "failure", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if operation != "place" {
				if _, err = slotOpen(b, "BTCUSDT"); err != nil {
					t.Fatal(err)
				}
				want = 1
			}
			run := func() error {
				if operation == "place" {
					_, err := slotOpen(b, "BTCUSDT")
					return err
				}
				o := hourlyEntries(b)[0]
				depth, _ := source.GetDepth(o.Symbol, 5)
				switch operation {
				case "fill":
					_, err := b.applyMakerFill(o.OrderID, o.RemainingQuantity, depth, b.now())
					return err
				case "reprice":
					_, err := b.expireOrRepriceMakerOrder(o.OrderID, depth, b.now())
					return err
				default:
					return b.CancelOrder(o.Symbol, fmt.Sprint(o.OrderID))
				}
			}
			ledger.fail = true
			if err = run(); err == nil {
				t.Fatal("expected persistence failure")
			}
			hourlyCount(t, b, want)
			ledger.fail = false
			b, err = NewPersistentPaperBroker(cfg, source, ledger, "failure", nil)
			if err != nil {
				t.Fatal(err)
			}
			hourlyCount(t, b, want)
			if err = run(); err != nil {
				t.Fatal("retry failed", err)
			}
			if operation == "cancel" {
				want = 0
			} else {
				want = 1
			}
			hourlyCount(t, b, want)
		})
	}
}

func TestPaperHourlyOpenLegacyMissingEvidenceIsConservative(t *testing.T) {
	b, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100, MaxOpensPerHour: 1}, slotSource())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = slotOpen(b, "BTCUSDT"); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.fills = nil
	b.mu.Unlock()
	hourlyCount(t, b, 1)
	if _, err = slotOpen(b, "ETHUSDT"); err == nil {
		t.Fatal("legacy recent position disappeared from count")
	}
}

func TestPaperHourlyOpenPolicyBindingAndCompatibility(t *testing.T) {
	strategy := store.GetDefaultStrategyConfig("en")
	throttle := store.DefaultTradeThrottleConfig()
	throttle.MaxOpensPerHour = 1
	strategy.RiskControl.TradeThrottle = &throttle
	if paperStrategyMaxOpensPerHour(&strategy) != 1 {
		t.Fatal("strategy hourly limit not bound")
	}
	strategy.StrategyType = "grid_trading"
	if paperStrategyMaxOpensPerHour(&strategy) != 0 || paperStrategyMaxOpensPerHour(nil) != 0 {
		t.Fatal("grid/legacy contract changed")
	}
	b, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 100}, slotSource())
	if err != nil {
		t.Fatal(err)
	}
	for _, sym := range []string{"BTCUSDT", "ETHUSDT"} {
		if _, err = slotOpen(b, sym); err != nil {
			t.Fatal("unconfigured broker restricted", err)
		}
	}
	if err = b.setHourlyOpenLimit(1); err != nil {
		t.Fatal(err)
	}
	if _, err = slotOpen(b, "SOLUSDT"); err == nil {
		t.Fatal("refreshed policy ignored")
	}
	if _, err = NewPaperBroker(PaperBrokerConfig{InitialBalance: 100, MaxOpensPerHour: -1}, slotSource()); err == nil {
		t.Fatal("negative hourly limit accepted")
	}
}
