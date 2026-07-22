package api

import "testing"

func TestValidateTraderLeverageRangeMatchesManualLimits(t *testing.T) {
	if msg, code := validateTraderLeverageRange(50, 50); msg != "" || code != "" {
		t.Fatalf("expected 50/50 leverage to be accepted, got msg=%q code=%q", msg, code)
	}

	if msg, code := validateTraderLeverageRange(51, 50); msg == "" || code != "trader.create.invalid_btc_eth_leverage" {
		t.Fatalf("expected BTC/ETH leverage > 50 to be rejected, got msg=%q code=%q", msg, code)
	}

	if msg, code := validateTraderLeverageRange(50, 51); msg == "" || code != "trader.create.invalid_altcoin_leverage" {
		t.Fatalf("expected altcoin leverage > 50 to be rejected, got msg=%q code=%q", msg, code)
	}
}

func TestValidateStartConfirmationSeparatesPaperAndLive(t *testing.T) {
	if err := validateStartConfirmation("paper", ""); err != nil {
		t.Fatalf("paper start must not require live_confirm: %v", err)
	}
	if err := validateStartConfirmation("live", ""); err == nil {
		t.Fatal("live start without live_confirm must fail")
	}
	if err := validateStartConfirmation("live", "true"); err != nil {
		t.Fatalf("live start with live_confirm=true: %v", err)
	}
}
