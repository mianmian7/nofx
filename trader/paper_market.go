package trader

import "nofx/market"

type binancePaperPriceSource struct{ client *market.APIClient }

func (s *binancePaperPriceSource) GetMarketPrice(symbol string) (float64, error) {
	return s.client.GetCurrentPrice(market.NormalizeForExchange("binance", symbol))
}

func (s *binancePaperPriceSource) GetDepth(symbol string, limit int) (*market.BinanceDepthSnapshot, error) {
	return s.client.GetDepth(market.NormalizeForExchange("binance", symbol), limit)
}

func (s *binancePaperPriceSource) GetFundingHistory(symbol string, startTime, endTime int64) ([]market.FundingEvent, error) {
	return s.client.GetFundingHistory(market.NormalizeForExchange("binance", symbol), startTime, endTime)
}

func (s *binancePaperPriceSource) GetFundingSnapshot(symbol string) (*market.FundingSnapshot, error) {
	return s.client.GetFundingSnapshot(market.NormalizeForExchange("binance", symbol))
}
