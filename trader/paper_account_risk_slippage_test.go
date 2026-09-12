package trader

import (
	"fmt"
	"nofx/kernel"
	"testing"
)

func TestPaperAccountRiskIncludesPostFillSlippageEquityLoss(t *testing.T) {
	for _, side := range []string{"long", "short"} {
		for _, slip := range []float64{2, 25, 100, 500} {
			t.Run(fmt.Sprintf("%s/%gbps", side, slip), func(t *testing.T) {
				cfg := accountRiskConfig(t, false)
				cfg.TakerFeeBPS = 0
				cfg.MakerFeeBPS = 0
				cfg.SlippageBPS = slip
				cfg.MinEntryNotional = 0
				b, err := NewPaperBroker(cfg, fixedPaperPriceSource{"MUUSDT": 100})
				if err != nil {
					t.Fatal(err)
				}
				stop, target := 90.0, 140.0
				if side == "short" {
					stop, target = 110, 60
				}
				_, err = b.ExecuteDecision(&kernel.Decision{Symbol: "MUUSDT", Action: "open_" + side, PositionSizeUSD: 300, Leverage: 3, StopLoss: stop, TakeProfit: target})
				if err != nil {
					t.Fatal(err)
				}
				p := b.positions["MUUSDT:"+side]
				risk, err := paperPositionRisk(side, p.EntryPrice, p.StopLoss, p.Quantity, b.accountRiskCostRatioLocked())
				if err != nil {
					t.Fatal(err)
				}
				if risk > b.Snapshot().Equity*.01+1e-9 {
					t.Fatalf("stop risk %.10f exceeds post-fill-equity cap %.10f", risk, b.Snapshot().Equity*.01)
				}
			})
		}
	}
}
