package trader

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nofx/kernel"
	"nofx/store"
)

func TestPaperBrokerOpenPriceChangeTakeProfitAndAccounting(t *testing.T) {
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 10_000,
		TakerFeeBPS:    10,
		SlippageBPS:    10,
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	entry, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000,
		Leverage: 3, StopLoss: 90, TakeProfit: 110,
	})
	if err != nil {
		t.Fatalf("open_long: %v", err)
	}
	assertPaperFloat(t, "entry price", entry.Price, 100.1)
	assertPaperFloat(t, "entry fee", entry.Fee, 1)

	exits, err := broker.ProcessPrice("MUUSDT", 111, time.Unix(1_784_650_000, 0).UTC())
	if err != nil {
		t.Fatalf("ProcessPrice: %v", err)
	}
	if len(exits) != 1 || exits[0].Action != "take_profit" {
		t.Fatalf("exit fills = %#v, want one take_profit", exits)
	}
	wantExitPrice := 111 * 0.999
	wantQuantity := 1_000 / 100.1
	wantExitFee := wantExitPrice * wantQuantity * 0.001
	wantNetPnL := (wantExitPrice-100.1)*wantQuantity - 1 - wantExitFee
	assertPaperFloat(t, "exit price", exits[0].Price, wantExitPrice)

	snapshot := broker.Snapshot()
	assertPaperFloat(t, "realized pnl", snapshot.RealizedPnL, wantNetPnL)
	assertPaperFloat(t, "fees", snapshot.Fees, 1+wantExitFee)
	assertPaperFloat(t, "equity", snapshot.Equity, 10_000+wantNetPnL)
	if snapshot.OpenPositions != 0 || snapshot.ClosedTrades != 1 || snapshot.Wins != 1 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
}

func TestPaperBrokerRestoresBalanceAndPositionsAfterRestart(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "paper.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	config := PaperBrokerConfig{InitialBalance: 5_000, TakerFeeBPS: 5, SlippageBPS: 2}
	prices := fixedPaperPriceSource{"MUUSDT": 100}
	first, err := NewPersistentPaperBroker(config, prices, st.Paper(), "paper-restart", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("NewPersistentPaperBroker first: %v", err)
	}
	if _, err := first.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 90, TakeProfit: 120,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	openRows, err := st.Position().GetOpenPositions("paper-restart")
	if err != nil {
		t.Fatalf("query trader_positions: %v", err)
	}
	if len(openRows) != 0 {
		t.Fatalf("paper trader_positions OPEN rows = %d, want zero because paper state is the runtime ledger", len(openRows))
	}
	want := first.Snapshot()

	restored, err := NewPersistentPaperBroker(config, prices, st.Paper(), "paper-restart", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("NewPersistentPaperBroker restored: %v", err)
	}
	got := restored.Snapshot()
	assertPaperFloat(t, "restored balance", got.Balance, want.Balance)
	assertPaperFloat(t, "restored equity", got.Equity, want.Equity)
	assertPaperFloat(t, "restored used margin", got.UsedMargin, want.UsedMargin)
	assertPaperFloat(t, "restored available balance", got.AvailableBalance, want.AvailableBalance)
	positions, err := restored.GetPositions()
	if err != nil || len(positions) != 1 || positions[0]["symbol"] != "MUUSDT" {
		t.Fatalf("restored positions = %#v, err=%v", positions, err)
	}
}

func TestPersistentPaperBrokerQuarantinesInvalidHistoricalProtection(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "paper-quarantine.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	state := paperBrokerState{
		InitialBalance: 1_000,
		Balance:        1_000,
		Positions: map[string]PaperPosition{
			// This mirrors the observed failure: a long stop above its entry.
			"MUUSDT:long": {
				Symbol: "MUUSDT", Side: "long", Quantity: 1, EntryPrice: 852.91,
				Leverage: 3, StopLoss: 855, TakeProfit: 900,
			},
			"BTCUSDT:short": {
				Symbol: "BTCUSDT", Side: "short", Quantity: 1, EntryPrice: 100,
				Leverage: 3, StopLoss: 110, TakeProfit: 90,
			},
		},
		Marks:  map[string]float64{"MUUSDT": 852.91, "BTCUSDT": 100},
		NextID: 1,
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("marshal state: %v", err)
	}
	if err := st.Paper().SavePaperState("paper-quarantine", raw); err != nil {
		t.Fatalf("save paper state: %v", err)
	}

	broker, err := NewPersistentPaperBroker(
		PaperBrokerConfig{InitialBalance: 1_000},
		fixedPaperPriceSource{"MUUSDT": 852.91, "BTCUSDT": 100},
		st.Paper(), "paper-quarantine", newPaperTradeRecorder(st, "paper"),
	)
	if err != nil {
		t.Fatalf("NewPersistentPaperBroker: %v", err)
	}
	warnings := broker.RestoreWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "MUUSDT:long") || !strings.Contains(warnings[0], "below entry price") {
		t.Fatalf("restore warnings = %#v", warnings)
	}
	if len(broker.QuarantinedPositions()) != 1 {
		t.Fatalf("quarantined positions = %#v, want one", broker.QuarantinedPositions())
	}
	positions, err := broker.GetPositions()
	if err != nil || len(positions) != 1 || positions[0]["symbol"] != "BTCUSDT" || positions[0]["side"] != "short" {
		t.Fatalf("active restored positions = %#v, err=%v", positions, err)
	}

	saved, found, err := st.Paper().LoadPaperState("paper-quarantine")
	if err != nil || !found {
		t.Fatalf("load migrated paper state: found=%v err=%v", found, err)
	}
	var migrated paperBrokerState
	if err := json.Unmarshal(saved, &migrated); err != nil {
		t.Fatalf("decode migrated paper state: %v", err)
	}
	if len(migrated.Positions) != 1 || len(migrated.QuarantinedPositions) != 1 || len(migrated.RestoreWarnings) != 1 {
		t.Fatalf("migrated state active=%d quarantined=%d warnings=%d", len(migrated.Positions), len(migrated.QuarantinedPositions), len(migrated.RestoreWarnings))
	}
}

func TestPaperBrokerLiquidationPrecedesStopLoss(t *testing.T) {
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000, TakerFeeBPS: 5, SlippageBPS: 0,
		MaintenanceMarginRatio: 0.005,
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 500,
		Leverage: 5, StopLoss: 90, TakeProfit: 120,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	exits, err := broker.ProcessPrice("MUUSDT", 80, time.Unix(1_784_650_100, 0).UTC())
	if err != nil {
		t.Fatalf("ProcessPrice: %v", err)
	}
	if len(exits) != 1 || exits[0].Action != "liquidation" {
		t.Fatalf("exits = %#v, want liquidation before stop loss", exits)
	}
}

func TestPaperBrokerShortTakeProfitUsesAbsolutePriceAndDirection(t *testing.T) {
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000, SlippageBPS: 0}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_short", PositionSizeUSD: 300,
		Leverage: 3, StopLoss: 110, TakeProfit: 90,
	}); err != nil {
		t.Fatalf("open_short: %v", err)
	}
	exits, err := broker.ProcessPrice("MUUSDT", 90, time.Unix(1_784_650_050, 0).UTC())
	if err != nil {
		t.Fatalf("ProcessPrice: %v", err)
	}
	if len(exits) != 1 || exits[0].Action != "take_profit" || exits[0].Side != "short" {
		t.Fatalf("short exits = %#v, want one short take_profit", exits)
	}
}

func TestValidatePaperExitPricesCoversBothDirections(t *testing.T) {
	tests := []struct {
		name   string
		action string
		entry  float64
		stop   float64
		target float64
		valid  bool
	}{
		{name: "long valid", action: "open_long", entry: 100, stop: 90, target: 110, valid: true},
		{name: "long stop wrong side", action: "open_long", entry: 100, stop: 105, target: 110},
		{name: "long target wrong side", action: "open_long", entry: 100, stop: 90, target: 95},
		{name: "short valid", action: "open_short", entry: 100, stop: 110, target: 90, valid: true},
		{name: "short stop wrong side", action: "open_short", entry: 100, stop: 95, target: 90},
		{name: "short target wrong side", action: "open_short", entry: 100, stop: 110, target: 105},
		{name: "optional protections", action: "open_long", entry: 100, valid: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePaperExitPrices(tt.action, tt.entry, tt.stop, tt.target)
			if tt.valid && err != nil {
				t.Fatalf("validation error = %v", err)
			}
			if !tt.valid && err == nil {
				t.Fatal("invalid protection was accepted")
			}
		})
	}
}

func TestPaperBrokerRejectsOpenWhenMarginAndFeeExceedAvailableBalance(t *testing.T) {
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000,
		TakerFeeBPS:    10,
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}

	_, err = broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 3_000, Leverage: 3,
	})
	if err == nil {
		t.Fatal("open_long succeeded, want insufficient available margin error")
	}
	if snapshot := broker.Snapshot(); snapshot.OpenPositions != 0 || snapshot.Balance != 1_000 {
		t.Fatalf("rejected open mutated account: %#v", snapshot)
	}
}

func TestPaperBrokerRejectsNonPositiveLeverage(t *testing.T) {
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	for _, leverage := range []int{0, -1} {
		_, err := broker.ExecuteDecision(&kernel.Decision{
			Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 100, Leverage: leverage,
		})
		if err == nil {
			t.Fatalf("leverage %d succeeded, want fail-closed error", leverage)
		}
	}
}

func TestPaperBrokerSnapshotReservesInitialMargin(t *testing.T) {
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000,
		TakerFeeBPS:    10,
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 4,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}

	snapshot := broker.Snapshot()
	assertPaperFloat(t, "wallet balance", snapshot.Balance, 999)
	assertPaperFloat(t, "used margin", snapshot.UsedMargin, 250)
	assertPaperFloat(t, "available balance", snapshot.AvailableBalance, 749)
	positions, err := broker.GetPositions()
	if err != nil {
		t.Fatalf("GetPositions: %v", err)
	}
	assertPaperFloat(t, "position initial margin", positions[0]["initial_margin"].(float64), 250)
}

func TestPaperBrokerCloseReleasesInitialMargin(t *testing.T) {
	broker, err := NewPaperBroker(PaperBrokerConfig{
		InitialBalance: 1_000,
		TakerFeeBPS:    10,
	}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 400, Leverage: 4,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_long"}); err != nil {
		t.Fatalf("close_long: %v", err)
	}

	snapshot := broker.Snapshot()
	assertPaperFloat(t, "released used margin", snapshot.UsedMargin, 0)
	assertPaperFloat(t, "available after release", snapshot.AvailableBalance, 999.2)
	assertPaperFloat(t, "wallet after entry and exit fees", snapshot.Balance, 999.2)
}

func TestPaperBrokerShortUsesAndReleasesInitialMargin(t *testing.T) {
	prices := fixedPaperPriceSource{"MUUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_short", PositionSizeUSD: 600, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_short: %v", err)
	}
	assertPaperFloat(t, "short used margin", broker.Snapshot().UsedMargin, 200)
	assertPaperFloat(t, "short available balance", broker.Snapshot().AvailableBalance, 800)

	prices["MUUSDT"] = 90
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_short"}); err != nil {
		t.Fatalf("close_short: %v", err)
	}
	snapshot := broker.Snapshot()
	assertPaperFloat(t, "short released margin", snapshot.UsedMargin, 0)
	assertPaperFloat(t, "short profit wallet", snapshot.Balance, 1_060)
	assertPaperFloat(t, "short available after close", snapshot.AvailableBalance, 1_060)
}

func TestPaperBrokerRecomputesMarginForLegacyPersistedPosition(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "legacy-paper.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	legacy := []byte(`{
		"initial_balance":1000,
		"balance":999,
		"positions":{"MUUSDT:long":{
			"Symbol":"MUUSDT","Side":"long","Quantity":6,"EntryPrice":100,
			"Leverage":3,"EntryFee":1,"LiquidationPrice":67.1666666667
		}},
		"marks":{"MUUSDT":100},"total_fees":1,"peak_equity":1000,"next_id":2
	}`)
	if err := st.Paper().SavePaperState("legacy-margin", legacy); err != nil {
		t.Fatalf("SavePaperState: %v", err)
	}

	broker, err := NewPersistentPaperBroker(
		PaperBrokerConfig{InitialBalance: 1_000},
		fixedPaperPriceSource{"MUUSDT": 100},
		st.Paper(),
		"legacy-margin",
		newPaperTradeRecorder(st, "paper"),
	)
	if err != nil {
		t.Fatalf("NewPersistentPaperBroker: %v", err)
	}
	snapshot := broker.Snapshot()
	assertPaperFloat(t, "legacy used margin", snapshot.UsedMargin, 200)
	assertPaperFloat(t, "legacy available balance", snapshot.AvailableBalance, 799)
}

func TestPaperAccountInfoUsesReservedInitialMarginNotMarkValue(t *testing.T) {
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, fixedPaperPriceSource{"MUUSDT": 100})
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 1_000, Leverage: 4,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	if _, err := broker.ProcessPrice("MUUSDT", 110, time.Now().UTC()); err != nil {
		t.Fatalf("ProcessPrice: %v", err)
	}
	at := &AutoTrader{trader: broker, paperBroker: broker, executionMode: ExecutionModePaper, initialBalance: 1_000}
	account, err := at.GetAccountInfo()
	if err != nil {
		t.Fatalf("GetAccountInfo: %v", err)
	}
	assertPaperFloat(t, "account margin used", account["margin_used"].(float64), 250)
	assertPaperFloat(t, "account available balance", account["available_balance"].(float64), 750)
}

func TestPaperPerformanceReconstructsExactLegacyFillHistory(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "legacy-performance.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	legacy := []byte(`{
		"initial_balance":10000,"balance":10012.015984153188,
		"positions":{"KORUUSDT:long":{"Symbol":"KORUUSDT","Side":"long","Quantity":130.13861699064,"EntryPrice":23.084615999999997,"Leverage":3,"EntryFee":1.5021,"InitialMargin":1001.4}},
		"fills":[
			{"OrderID":1,"Symbol":"SOXLUSDT","Action":"open_short","Price":156.628668,"Quantity":22.345845397855264,"Fee":1.75,"RealizedPnL":0,"Time":"2026-07-21T19:29:49.893241334Z"},
			{"OrderID":2,"Symbol":"SOXLUSDT","Action":"close_short","Price":158.601714,"Quantity":22.345845397855264,"Fee":1.772044690439428,"RealizedPnL":-47.61142556929577,"Time":"2026-07-21T19:46:10.755796447Z"},
			{"OrderID":3,"Symbol":"SNDKUSDT","Action":"open_long","Price":1590.778092,"Quantity":2.1894945734517948,"Fee":1.7415,"RealizedPnL":0,"Time":"2026-07-21T20:03:25.837961979Z"},
			{"OrderID":4,"Symbol":"SNDKUSDT","Action":"take_profit","Price":1628.304274,"Quantity":2.1894945734517948,"Fee":1.7825816859256822,"RealizedPnL":78.63929016543888,"Time":"2026-07-21T21:01:36.31170213Z"},
			{"OrderID":5,"Symbol":"SNDKUSDT","Action":"open_long","Price":1630.97613,"Quantity":2.4525190322681176,"Fee":2,"RealizedPnL":0,"Time":"2026-07-21T21:08:00.02886671Z"},
			{"OrderID":6,"Symbol":"SNDKUSDT","Action":"close_long","Price":1625.464842,"Quantity":2.4525190322681176,"Fee":1.9932417306438446,"RealizedPnL":-17.509780442954543,"Time":"2026-07-21T22:22:47.518006514Z"},
			{"OrderID":7,"Symbol":"KORUUSDT","Action":"open_long","Price":23.084615999999997,"Quantity":130.13861699064,"Fee":1.5021,"RealizedPnL":0,"Time":"2026-07-22T00:07:54.195537544Z"}
		],
		"marks":{"KORUUSDT":22.69},"total_fees":12.541468107008955,"closed_trades":3,"wins":1,"peak_equity":10051.134552325533,"max_drawdown":0.9001307084660417,"next_id":8
	}`)
	if err := st.Paper().SavePaperState("legacy-performance", legacy); err != nil {
		t.Fatalf("SavePaperState: %v", err)
	}
	broker, err := NewPersistentPaperBroker(
		PaperBrokerConfig{InitialBalance: 10_000},
		fixedPaperPriceSource{"KORUUSDT": 22.69},
		st.Paper(),
		"legacy-performance",
		newPaperTradeRecorder(st, "paper"),
	)
	if err != nil {
		t.Fatalf("NewPersistentPaperBroker: %v", err)
	}

	performance := broker.Performance()
	if performance.TotalTrades != 3 || performance.WinTrades != 1 || performance.LossTrades != 2 {
		t.Fatalf("trade counts = %#v", performance)
	}
	assertPaperFloat(t, "closed trade net pnl", performance.TotalPnL, 13.518084153188568)
	assertPaperFloat(t, "all paper fees", performance.TotalFees, 12.541468107008955)
	assertPaperFloat(t, "win rate", performance.WinRate, 100.0/3.0)
	if len(performance.ClosedTrades) != 3 {
		t.Fatalf("closed trades = %#v", performance.ClosedTrades)
	}
	if performance.ClosedTrades[0].Symbol != "SNDKUSDT" || performance.ClosedTrades[0].Side != "long" || performance.ClosedTrades[0].CloseReason != "close_long" {
		t.Fatalf("most recent closed trade = %#v", performance.ClosedTrades[0])
	}
	if performance.ClosedTrades[2].Symbol != "SOXLUSDT" || performance.ClosedTrades[2].Side != "short" {
		t.Fatalf("oldest closed trade = %#v", performance.ClosedTrades[2])
	}
}

func TestPaperFillsPersistSideAndLeverageForFutureReconstruction(t *testing.T) {
	prices := fixedPaperPriceSource{"MUUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_short", PositionSizeUSD: 400, Leverage: 4,
	}); err != nil {
		t.Fatalf("open_short: %v", err)
	}
	prices["MUUSDT"] = 90
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_short"}); err != nil {
		t.Fatalf("close_short: %v", err)
	}
	fills := broker.RecentFills(2)
	for _, fill := range fills {
		if fill.Side != "short" || fill.Leverage != 4 {
			t.Fatalf("fill metadata = %#v", fill)
		}
	}
	trade := broker.Performance().ClosedTrades[0]
	if trade.Side != "short" || trade.Leverage != 4 {
		t.Fatalf("reconstructed trade = %#v", trade)
	}
}

func TestPaperStatusExposesPersistentPerformanceHistory(t *testing.T) {
	prices := fixedPaperPriceSource{"MUUSDT": 100}
	broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 1_000}, prices)
	if err != nil {
		t.Fatalf("NewPaperBroker: %v", err)
	}
	if _, err := broker.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_long", PositionSizeUSD: 300, Leverage: 3,
	}); err != nil {
		t.Fatalf("open_long: %v", err)
	}
	prices["MUUSDT"] = 110
	if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_long"}); err != nil {
		t.Fatalf("close_long: %v", err)
	}
	at := &AutoTrader{id: "paper-status", executionMode: ExecutionModePaper, paperBroker: broker}
	status := at.GetStatus()
	performance, ok := status["paper_performance"].(PaperPerformance)
	if !ok {
		t.Fatalf("paper_performance missing or wrong type: %#v", status["paper_performance"])
	}
	if performance.TotalTrades != 1 || len(performance.ClosedTrades) != 1 {
		t.Fatalf("paper performance = %#v", performance)
	}
}

func TestPaperPerformanceReconstructsManualAndRiskCloseReasons(t *testing.T) {
	tests := []struct {
		name       string
		openAction string
		side       string
		leverage   int
		stopLoss   float64
		takeProfit float64
		exitPrice  float64
		manual     bool
		wantReason string
	}{
		{name: "manual long", openAction: "open_long", side: "long", leverage: 3, exitPrice: 105, manual: true, wantReason: "close_long"},
		{name: "long take profit", openAction: "open_long", side: "long", leverage: 3, takeProfit: 110, exitPrice: 111, wantReason: "take_profit"},
		{name: "short stop loss", openAction: "open_short", side: "short", leverage: 3, stopLoss: 110, exitPrice: 111, wantReason: "stop_loss"},
		{name: "short liquidation", openAction: "open_short", side: "short", leverage: 5, exitPrice: 121, wantReason: "liquidation"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			prices := fixedPaperPriceSource{"MUUSDT": 100}
			broker, err := NewPaperBroker(PaperBrokerConfig{InitialBalance: 2_000}, prices)
			if err != nil {
				t.Fatalf("NewPaperBroker: %v", err)
			}
			if _, err := broker.ExecuteDecision(&kernel.Decision{
				Symbol: "MUUSDT", Action: tc.openAction, PositionSizeUSD: 500,
				Leverage: tc.leverage, StopLoss: tc.stopLoss, TakeProfit: tc.takeProfit,
			}); err != nil {
				t.Fatalf("%s: %v", tc.openAction, err)
			}
			prices["MUUSDT"] = tc.exitPrice
			if tc.manual {
				if _, err := broker.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_" + tc.side}); err != nil {
					t.Fatalf("manual close: %v", err)
				}
			} else if _, err := broker.ProcessPrice("MUUSDT", tc.exitPrice, time.Now().UTC().Add(time.Second)); err != nil {
				t.Fatalf("ProcessPrice: %v", err)
			}
			performance := broker.Performance()
			if len(performance.ClosedTrades) != 1 {
				t.Fatalf("closed trades = %#v", performance.ClosedTrades)
			}
			trade := performance.ClosedTrades[0]
			if trade.Side != tc.side || trade.CloseReason != tc.wantReason || trade.Leverage != tc.leverage {
				t.Fatalf("closed trade = %#v", trade)
			}
		})
	}
}

func TestPaperPerformanceAndFillMetadataSurviveRestart(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "paper-performance-restart.db"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	prices := fixedPaperPriceSource{"MUUSDT": 100}
	config := PaperBrokerConfig{InitialBalance: 2_000, TakerFeeBPS: 5}
	first, err := NewPersistentPaperBroker(config, prices, st.Paper(), "paper-performance-restart", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("NewPersistentPaperBroker first: %v", err)
	}
	if _, err := first.ExecuteDecision(&kernel.Decision{
		Symbol: "MUUSDT", Action: "open_short", PositionSizeUSD: 500, Leverage: 4,
	}); err != nil {
		t.Fatalf("open_short: %v", err)
	}
	prices["MUUSDT"] = 90
	if _, err := first.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "close_short"}); err != nil {
		t.Fatalf("close_short: %v", err)
	}
	closedRows, err := st.Position().GetClosedPositions("paper-performance-restart", 10)
	if err != nil || len(closedRows) != 1 || closedRows[0].Status != "CLOSED" {
		t.Fatalf("paper trader_positions closed rows = %#v, err=%v", closedRows, err)
	}
	want := first.Performance()

	restored, err := NewPersistentPaperBroker(config, prices, st.Paper(), "paper-performance-restart", newPaperTradeRecorder(st, "paper"))
	if err != nil {
		t.Fatalf("NewPersistentPaperBroker restored: %v", err)
	}
	got := restored.Performance()
	if got.TotalTrades != want.TotalTrades || len(got.ClosedTrades) != 1 {
		t.Fatalf("restored performance = %#v, want %#v", got, want)
	}
	trade := got.ClosedTrades[0]
	if trade.Side != "short" || trade.Leverage != 4 || trade.CloseReason != "close_short" {
		t.Fatalf("restored trade metadata = %#v", trade)
	}
	assertPaperFloat(t, "restored pnl", got.TotalPnL, want.TotalPnL)
	assertPaperFloat(t, "restored fees", got.TotalFees, want.TotalFees)
}

func assertPaperFloat(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-8 {
		t.Fatalf("%s = %.12f, want %.12f", label, got, want)
	}
}
