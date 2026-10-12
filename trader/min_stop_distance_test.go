package trader

import (
	"math"
	"testing"

	"nofx/kernel"
)

func near(a, b float64) bool { return math.Abs(a-b) <= 1e-9*math.Max(1, math.Abs(b)) }

func TestMinStopConfigDefaultsAndEnv(t *testing.T) {
	t.Setenv("NOFX_MIN_STOP_ATR_MULT", "")
	t.Setenv("NOFX_MIN_STOP_PCT", "")
	c := loadMinStopConfig()
	if c.ATRMult != 1.2 || c.Pct != 0.5 || !c.enabled() {
		t.Fatalf("defaults = %+v", c)
	}
	t.Setenv("NOFX_MIN_STOP_ATR_MULT", "2")
	t.Setenv("NOFX_MIN_STOP_PCT", "0")
	c = loadMinStopConfig()
	if c.ATRMult != 2 || c.Pct != 0 {
		t.Fatalf("env = %+v", c)
	}
	t.Setenv("NOFX_MIN_STOP_ATR_MULT", "-1")
	if loadMinStopConfig().ATRMult != 1.2 {
		t.Fatal("negative must fall back to default")
	}
	t.Setenv("NOFX_MIN_STOP_ATR_MULT", "0")
	if loadMinStopConfig().enabled() {
		t.Fatal("0/0 must disable")
	}
}

func TestWidenStopLongPctFloorScalesSizeKeepsTP(t *testing.T) {
	d := &kernel.Decision{Symbol: "BTCUSDT", Action: "open_long", Leverage: 3, StopLoss: 99.8, TakeProfit: 101, PositionSizeUSD: 50}
	res, err := widenStopToMinimum(d, 100, 0.1, minStopConfig{ATRMult: 1.2, Pct: 0.5}, -20)
	if err != nil || !res.Widened {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if !near(d.StopLoss, 99.5) || d.TakeProfit != 101 || d.RiskRewardStopLoss != 99.8 {
		t.Fatalf("decision=%+v", d)
	}
	// risk at stop preserved: 50*0.2% == size*0.5%
	if !near(d.PositionSizeUSD, 20) || !near(d.PositionSizeUSD*0.5/100, 50*0.2/100) {
		t.Fatalf("size=%v", d.PositionSizeUSD)
	}
}

func TestWidenStopShortATRDominates(t *testing.T) {
	d := &kernel.Decision{Symbol: "ETHUSDT", Action: "open_short", Leverage: 3, StopLoss: 2001, TakeProfit: 1980, PositionSizeUSD: 90}
	res, err := widenStopToMinimum(d, 2000, 10, minStopConfig{ATRMult: 1.2, Pct: 0.5}, -20) // 1.2*10=12 > 10
	if err != nil || !res.Widened || !near(d.StopLoss, 2012) || !near(d.PositionSizeUSD, 90*1.0/12) {
		t.Fatalf("res=%+v d=%+v err=%v", res, d, err)
	}
}

func TestWidenStopNoChangeWhenAlreadyWide(t *testing.T) {
	d := &kernel.Decision{Action: "open_long", Leverage: 3, StopLoss: 98, TakeProfit: 110, PositionSizeUSD: 50}
	res, _ := widenStopToMinimum(d, 100, 0.5, minStopConfig{ATRMult: 1.2, Pct: 0.5}, -20)
	if res.Widened || d.StopLoss != 98 || d.PositionSizeUSD != 50 || d.RiskRewardStopLoss != 0 {
		t.Fatalf("unexpected change %+v", d)
	}
}

func TestWidenStopRespectsHardMarginStop(t *testing.T) {
	// 20x leverage, -20% margin => 1% price boundary; ATR wants 3%.
	d := &kernel.Decision{Action: "open_long", Leverage: 20, StopLoss: 99.9, TakeProfit: 105, PositionSizeUSD: 100}
	res, err := widenStopToMinimum(d, 100, 2.5, minStopConfig{ATRMult: 1.2, Pct: 0.5}, -20)
	if err != nil || !res.Widened || !near(d.StopLoss, 99) || !near(d.PositionSizeUSD, 10) {
		t.Fatalf("res=%+v d=%+v err=%v", res, d, err)
	}
}

func TestWidenStopIgnoresNonEntries(t *testing.T) {
	d := &kernel.Decision{Action: "update_position", StopLoss: 99.9}
	if res, _ := widenStopToMinimum(d, 100, 1, minStopConfig{ATRMult: 1.2, Pct: 0.5}, -20); res.Widened || d.StopLoss != 99.9 {
		t.Fatal("non-entry changed")
	}
}

func TestRiskRewardGateUsesOriginalStop(t *testing.T) {
	b := &PaperBroker{config: PaperBrokerConfig{MinRiskRewardRatio: 2}}
	// original stop 99.8 -> RR (101-100)/0.2 = 5; widened 99.5 -> RR 2.0 still ok; widened 99.4 -> 1.67 fails.
	if err := b.validateEntryRiskRewardLocked("open_long", 100, riskRewardStop(99.8, 99.4), 101); err != nil {
		t.Fatalf("original-stop gate rejected: %v", err)
	}
	if err := b.validateEntryRiskRewardLocked("open_long", 100, riskRewardStop(0, 99.4), 101); err == nil {
		t.Fatal("widened-stop gate should reject")
	}
}
