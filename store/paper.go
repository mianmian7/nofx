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
