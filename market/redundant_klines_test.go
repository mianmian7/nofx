package market

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestHedgedFreshKlinesUsesIndependentSuccessfulSource(t *testing.T) {
	now := time.Now().Add(-time.Minute).UnixMilli()
	primary := func(context.Context) ([]Kline, error) {
		return nil, errors.New("primary timeout")
	}
	standby := func(context.Context) ([]Kline, error) {
		return []Kline{{OpenTime: now, CloseTime: now + 59_999, Open: 100, High: 101, Low: 99, Close: 100, Volume: 1}}, nil
	}
	klines, proof, err := hedgedFreshKlines(context.Background(), "binance", "BTCUSDT", "1m", primary, standby)
	if err != nil {
		t.Fatalf("hedgedFreshKlines: %v", err)
	}
	if len(klines) != 1 || proof.Transport != "standby-rest" || proof.Exchange != "binance" {
		t.Fatalf("result/proof = %#v/%#v", klines, proof)
	}
}

func TestHedgedFreshKlinesRejectsConflictingLiveSources(t *testing.T) {
	now := time.Now().Add(-time.Minute).UnixMilli()
	source := func(closePrice float64) freshKlineSource {
		return func(context.Context) ([]Kline, error) {
			return []Kline{{OpenTime: now, CloseTime: now + 59_999, Open: closePrice, High: closePrice, Low: closePrice, Close: closePrice, Volume: 1}}, nil
		}
	}
	_, _, err := hedgedFreshKlines(context.Background(), "binance", "BTCUSDT", "1m", source(100), source(120))
	if err == nil || !strings.Contains(err.Error(), "conflicting fresh K-line sources") {
		t.Fatalf("error = %v, want source-conflict rejection", err)
	}
}

func TestHedgedFreshKlinesCancelsLosingSource(t *testing.T) {
	now := time.Now().Add(-time.Minute).UnixMilli()
	canceled := make(chan struct{})
	primary := func(ctx context.Context) ([]Kline, error) {
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}
	standby := func(context.Context) ([]Kline, error) {
		return []Kline{{OpenTime: now, CloseTime: now + 59_999, Open: 100, High: 101, Low: 99, Close: 100, Volume: 1}}, nil
	}
	if _, _, err := hedgedFreshKlines(context.Background(), "binance", "BTCUSDT", "1m", primary, standby); err != nil {
		t.Fatalf("hedgedFreshKlines: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("losing primary source was not canceled")
	}
}
