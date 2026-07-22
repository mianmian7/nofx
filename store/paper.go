package store

import (
	"errors"
	"time"

	"gorm.io/gorm"
)

type PaperAccountState struct {
	TraderID  string    `gorm:"primaryKey;column:trader_id" json:"trader_id"`
	StateJSON []byte    `gorm:"column:state_json;not null" json:"-"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (PaperAccountState) TableName() string { return "paper_account_states" }

type PaperStore struct{ db *gorm.DB }

func NewPaperStore(db *gorm.DB) *PaperStore { return &PaperStore{db: db} }

func (s *PaperStore) initTables() error { return s.db.AutoMigrate(&PaperAccountState{}) }

func (s *PaperStore) LoadPaperState(traderID string) ([]byte, bool, error) {
	var row PaperAccountState
	err := s.db.Where("trader_id = ?", traderID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return append([]byte(nil), row.StateJSON...), true, nil
}

func (s *PaperStore) SavePaperState(traderID string, state []byte) error {
	row := PaperAccountState{TraderID: traderID, StateJSON: append([]byte(nil), state...)}
	return s.db.Save(&row).Error
}

// ResetTraderAccount removes all simulated trading state and history for one
// user-owned Paper trader. The trader configuration itself is retained.
func (s *PaperStore) ResetTraderAccount(userID, traderID string) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&Trader{}).
			Where("id = ? AND user_id = ? AND execution_mode = ?", traderID, userID, "paper").
			Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return errors.New("paper trader not found")
		}

		models := []interface{}{
			&PaperAccountState{},
			&DecisionRecordDB{},
			&EquitySnapshot{},
			&TraderFill{},
			&TraderOrder{},
			&TraderPosition{},
		}
		for _, model := range models {
			if err := tx.Where("trader_id = ?", traderID).Delete(model).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
