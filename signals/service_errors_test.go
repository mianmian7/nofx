package signals

import (
	"context"
	"errors"
	"fmt"
	"nofx/market"
	"strings"
	"testing"
)

type failedCandleReader struct {
	market.MarketDataProvider
	err error
}

func (p failedCandleReader) GetKlinesFreshContext(context.Context, string, string, int) ([]market.Kline, error) {
	return nil, p.err
}
func TestCollectPreservesCandleFailureCause(t *testing.T) {
	cause := fmt.Errorf("public market HTTP429 code50011")
	_, err := collect(context.Background(), failedCandleReader{err: cause}, "BTCUSDT")
	if !errors.Is(err, cause) || !strings.Contains(err.Error(), "HTTP429") || !strings.Contains(err.Error(), "15m") {
		t.Fatalf("lost candle cause: %v", err)
	}
}
