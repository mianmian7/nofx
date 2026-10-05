package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// PaperStopLossUpdate records an executed protection update, not an AI proposal.
type PaperStopLossUpdate struct {
	StopLoss  float64
	MarkPrice float64
	Quantity  float64
	Timestamp time.Time
}

// LastPaperStopLossUpdate supplies provenance for ledgers written before the
// broker persisted protection evidence. Only the latest successful SL update
// for this position generation is returned; older matching levels cannot win.
func (s *PaperStore) LastPaperStopLossUpdate(traderID, symbol string, entryTime time.Time) (*PaperStopLossUpdate, error) {
	var rows []DecisionRecordDB
	if err := s.db.Select("id", "timestamp", "decisions").
		Where("trader_id = ? AND timestamp >= ?", traderID, entryTime).
		Order("timestamp DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		var actions []DecisionAction
		if err := json.Unmarshal([]byte(row.Decisions), &actions); err != nil {
			return nil, fmt.Errorf("paper protection history record %d: %w", row.ID, err)
		}
		for i := len(actions) - 1; i >= 0; i-- {
			a := actions[i]
			if a.Action != "update_position" || !strings.EqualFold(a.Symbol, symbol) || !a.Success || a.Degraded || a.Error != "" || a.StopLoss == 0 {
				continue
			}
			if a.Timestamp.Before(entryTime) || a.Timestamp.IsZero() {
				continue
			}
			return &PaperStopLossUpdate{StopLoss: a.StopLoss, MarkPrice: a.Price, Quantity: a.Quantity, Timestamp: a.Timestamp}, nil
		}
	}
	return nil, nil
}
