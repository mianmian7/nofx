package kernel

import (
	"fmt"
	"nofx/market"
)

func validMacroLabel(s string) bool {
	return s == "bullish_only" || s == "bearish_only" || s == "neutral" || s == "risk_off"
}

// Jev is an execution classifier, not a substitute for observed higher-timeframe
// direction. This entry-only check never interferes with position management.
func gateJevEntryTrend(dec *Decision, data *market.Data) {
	if dec == nil || (dec.Action != "open_long" && dec.Action != "open_short") {
		return
	}
	for _, tf := range []string{"1h", "4h"} {
		reason := ""
		if data == nil || data.TimeframeData[tf] == nil {
			reason = "missing trend data"
		} else {
			s := data.TimeframeData[tf]
			if len(s.EMA20Values) == 0 || len(s.EMA50Values) == 0 {
				reason = "missing EMA20/50"
			} else {
				fast, slow := s.EMA20Values[len(s.EMA20Values)-1], s.EMA50Values[len(s.EMA50Values)-1]
				if !(fast > 0 && slow > 0) || (dec.Action == "open_long" && !(fast > slow && data.CurrentPrice > fast)) || (dec.Action == "open_short" && !(fast < slow && data.CurrentPrice < fast)) {
					reason = "price and EMA trend are not aligned"
				}
			}
		}
		if reason != "" {
			dec.Action = "wait"
			dec.Reasoning = fmt.Sprintf("Jev higher-timeframe veto: %s %s", tf, reason)
			return
		}
	}
}
