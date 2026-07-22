package trader

import "nofx/market"

type binancePaperPriceSource struct{ client *market.APIClient }

func (s *binancePaperPriceSource) GetMarketPrice(symbol string) (float64, error) {
	return s.client.GetCurrentPrice(market.NormalizeForExchange("binance", symbol))
}
