package trader

import "nofx/market"

type binancePaperPriceSource struct{ client *market.APIClient }

func (s *binancePaperPriceSource) GetMarketPrice(symbol string) (float64, error) {
	return s.client.GetCurrentPriceFresh(market.NormalizeForExchange("binance", symbol))
}

func (s *binancePaperPriceSource) GetDepth(symbol string, limit int) (*market.DepthSnapshot, error) {
	return s.client.GetDepthFresh(market.NormalizeForExchange("binance", symbol), limit)
}

func (s *binancePaperPriceSource) GetFundingHistory(symbol string, startTime, endTime int64) ([]market.FundingEvent, error) {
	return s.client.GetFundingHistory(market.NormalizeForExchange("binance", symbol), startTime, endTime)
}

func (s *binancePaperPriceSource) GetFundingSnapshot(symbol string) (*market.FundingSnapshot, error) {
	return s.client.GetFundingSnapshot(market.NormalizeForExchange("binance", symbol))
}

type marketPaperPriceSource struct {
	provider market.MarketDataProvider
}

func newMarketPaperPriceSource(provider market.MarketDataProvider) *marketPaperPriceSource {
	return &marketPaperPriceSource{provider: provider}
}

func (source *marketPaperPriceSource) GetMarketPrice(symbol string) (float64, error) {
	return source.provider.GetCurrentPriceFresh(symbol)
}

func (source *marketPaperPriceSource) GetDepth(symbol string, limit int) (*market.DepthSnapshot, error) {
	return source.provider.GetDepthFresh(symbol, limit)
}

func (source *marketPaperPriceSource) GetFundingHistory(symbol string, startTime, endTime int64) ([]market.FundingEvent, error) {
	return source.provider.GetFundingHistory(symbol, startTime, endTime)
}

func (source *marketPaperPriceSource) GetFundingSnapshot(symbol string) (*market.FundingSnapshot, error) {
	return source.provider.GetFundingSnapshot(symbol)
}
