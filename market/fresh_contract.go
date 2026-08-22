package market

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type MarketCapability uint32

const (
	CapabilityPrice MarketCapability = 1 << iota
	CapabilityKlines
	CapabilityDepth
	CapabilityFunding
	CapabilityOpenInterest
	CapabilityContractSpec
)

const TradingCapabilities = CapabilityPrice |
	CapabilityKlines |
	CapabilityDepth |
	CapabilityFunding |
	CapabilityOpenInterest |
	CapabilityContractSpec

type MarketDataUnavailableError struct {
	Exchange   string
	Symbol     string
	Capability MarketCapability
	Cause      error
}

func (err *MarketDataUnavailableError) Error() string {
	parts := []string{strings.TrimSpace(err.Exchange), "fresh market data unavailable"}
	if err.Symbol != "" {
		parts = append(parts, "for "+err.Symbol)
	}
	if err.Capability != 0 {
		parts = append(parts, "("+err.Capability.String()+")")
	}
	message := strings.Join(parts, " ")
	if err.Cause != nil {
		message += ": " + err.Cause.Error()
	}
	return message
}

func (err *MarketDataUnavailableError) Unwrap() error { return err.Cause }

type UnsupportedMarketCapabilityError struct {
	Exchange string
	Missing  MarketCapability
}

func (err *UnsupportedMarketCapabilityError) Error() string {
	return fmt.Sprintf("%s market-data provider is missing required capabilities: %s", err.Exchange, err.Missing.String())
}

func (capability MarketCapability) Has(required MarketCapability) bool {
	return capability&required == required
}

func (capability MarketCapability) String() string {
	labels := make([]string, 0, 6)
	for _, item := range []struct {
		value MarketCapability
		label string
	}{
		{CapabilityPrice, "price"},
		{CapabilityKlines, "klines"},
		{CapabilityDepth, "depth"},
		{CapabilityFunding, "funding"},
		{CapabilityOpenInterest, "open_interest"},
		{CapabilityContractSpec, "contract_spec"},
	} {
		if capability.Has(item.value) {
			labels = append(labels, item.label)
		}
	}
	if len(labels) == 0 {
		return "none"
	}
	return strings.Join(labels, ",")
}

func RequireMarketCapabilities(provider MarketDataProvider, required MarketCapability) error {
	if provider == nil {
		return errors.New("market-data provider is nil")
	}
	missing := required &^ provider.Capabilities()
	if missing != 0 {
		return &UnsupportedMarketCapabilityError{Exchange: provider.Exchange(), Missing: missing}
	}
	return nil
}

type FreshnessProof struct {
	Exchange   string        `json:"exchange"`
	Transport  string        `json:"transport"`
	SourceTime time.Time     `json:"source_time"`
	ReceivedAt time.Time     `json:"received_at"`
	Age        time.Duration `json:"age"`
	Sequence   int64         `json:"sequence,omitempty"`
	SequenceOK bool          `json:"sequence_ok"`
	Reconciled bool          `json:"reconciled"`
}

func validateFreshSourceTimestamp(exchange, symbol, field string, timestamp int64, maxAge time.Duration) (time.Time, error) {
	if timestamp <= 0 {
		return time.Time{}, fmt.Errorf("%s %s %s has no source timestamp", exchange, symbol, field)
	}
	sourceTime := time.UnixMilli(timestamp).UTC()
	age, valid := boundedStreamAge(time.Now().UTC(), sourceTime)
	if !valid {
		return time.Time{}, fmt.Errorf("%s %s %s source timestamp is too far in the future", exchange, symbol, field)
	}
	if maxAge <= 0 || age > maxAge {
		return time.Time{}, fmt.Errorf("%s %s %s source timestamp is stale by %s", exchange, symbol, field, age)
	}
	return sourceTime, nil
}
