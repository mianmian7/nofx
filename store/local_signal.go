package store

import (
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LocalSignalFrame contains only closed-candle observations from one venue.
type LocalSignalFrame struct {
	Timeframe   string  `json:"timeframe"`
	ClosedAt    int64   `json:"closed_at"`
	Price       float64 `json:"price"`
	EMA20       float64 `json:"ema20"`
	EMA50       float64 `json:"ema50"`
	RSI         float64 `json:"rsi"`
	ATRPercent  float64 `json:"atr_percent"`
	VolumeRatio float64 `json:"volume_ratio"`
	Trend       float64 `json:"trend"`
	Momentum    float64 `json:"momentum"`
	Breakout    float64 `json:"breakout"`
}

type LocalSignal struct {
	Exchange               string             `json:"exchange"`
	Symbol                 string             `json:"symbol"`
	Version                string             `json:"version"`
	Direction              string             `json:"direction"`
	Score                  float64            `json:"score"`
	Confirmed              bool               `json:"confirmed"`
	ObservedAt             int64              `json:"observed_at"`
	ClosedAt               int64              `json:"closed_at"`
	Frames                 []LocalSignalFrame `json:"frames"`
	FundingRate            *float64           `json:"funding_rate"`
	FundingTime            int64              `json:"funding_time,omitempty"`
	OpenInterest           *float64           `json:"open_interest"`
	OpenInterestUnit       string             `json:"open_interest_unit,omitempty"`
	OpenInterestObservedAt int64              `json:"open_interest_observed_at,omitempty"`
	Warnings               []string           `json:"warnings"`
}

// LocalSignalScan is replaced atomically; failed symbols are never carried
// forward under a new timestamp. Historical observations remain read-only.
type LocalSignalScan struct {
	Exchange   string        `json:"exchange" gorm:"primaryKey"`
	ObservedAt int64         `json:"observed_at"`
	Expected   int           `json:"expected"`
	Succeeded  int           `json:"succeeded"`
	Error      string        `json:"error"`
	Results    []LocalSignal `json:"results" gorm:"serializer:json"`
	Failures   []string      `json:"failures" gorm:"serializer:json"`
}

func (LocalSignalScan) TableName() string { return "local_signal_scans" }

type LocalSignalHistory struct {
	ID       uint64      `json:"-" gorm:"primaryKey"`
	Exchange string      `json:"exchange" gorm:"uniqueIndex:local_signal_bar;index:local_signal_lookup,priority:1"`
	Symbol   string      `json:"symbol" gorm:"uniqueIndex:local_signal_bar;index:local_signal_lookup,priority:2"`
	ClosedAt int64       `json:"closed_at" gorm:"uniqueIndex:local_signal_bar;index:local_signal_lookup,priority:3;index"`
	Version  string      `json:"version" gorm:"uniqueIndex:local_signal_bar"`
	Signal   LocalSignal `json:"signal" gorm:"serializer:json"`
}

func (LocalSignalHistory) TableName() string { return "local_signal_history" }

type LocalSignalStore struct{ db *gorm.DB }

func (s *Store) LocalSignal() *LocalSignalStore { return &LocalSignalStore{db: s.gdb} }

func (s *LocalSignalStore) initTables() error {
	return s.db.AutoMigrate(&LocalSignalScan{}, &LocalSignalHistory{})
}

func (s *LocalSignalStore) Latest(exchange string) (*LocalSignalScan, error) {
	var scan LocalSignalScan
	if err := s.db.Where("exchange = ?", exchange).First(&scan).Error; err != nil {
		return nil, err
	}
	return &scan, nil
}

func (s *LocalSignalStore) Save(scan *LocalSignalScan) error {
	if scan == nil || scan.Exchange == "" {
		return fmt.Errorf("missing signal scan")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, result := range scan.Results {
			row := LocalSignalHistory{Exchange: scan.Exchange, Symbol: result.Symbol, ClosedAt: result.ClosedAt, Version: result.Version, Signal: result}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
				return err
			}
		}
		return tx.Save(scan).Error
	})
}

func (s *LocalSignalStore) History(exchange, symbol string, limit int) ([]LocalSignal, error) {
	return s.HistoryBefore(exchange, symbol, 0, limit)
}

func (s *LocalSignalStore) HistoryBefore(exchange, symbol string, before int64, limit int) ([]LocalSignal, error) {
	if limit < 1 || limit > 200 {
		limit = 96
	}
	var rows []LocalSignalHistory
	query := s.db.Where("exchange = ? AND symbol = ?", exchange, symbol)
	if before > 0 {
		query = query.Where("closed_at < ?", before)
	}
	err := query.Order("closed_at DESC").Limit(limit).Find(&rows).Error
	results := make([]LocalSignal, 0, len(rows))
	for _, row := range rows {
		results = append(results, row.Signal)
	}
	return results, err
}

// Prune is bounded to avoid a long write lock on the trading database.
func (s *LocalSignalStore) Prune(now time.Time) error {
	var ids []uint64
	if err := s.db.Model(&LocalSignalHistory{}).Where("closed_at < ?", now.AddDate(0, 0, -30).UnixMilli()).Order("id").Limit(500).Pluck("id", &ids).Error; err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}
	return s.db.Where("id IN ?", ids).Delete(&LocalSignalHistory{}).Error
}
