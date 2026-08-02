package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"nofx/market"

	"github.com/gin-gonic/gin"
)

type depthMarketClient interface {
	GetDepth(symbol string, limit int) (*market.DepthSnapshot, error)
}

func (s *Server) handleDepth(c *gin.Context) {
	symbol := strings.TrimSpace(c.Query("symbol"))
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol parameter is required"})
		return
	}
	exchange := strings.ToLower(strings.TrimSpace(c.DefaultQuery("exchange", "binance")))
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || (limit != 5 && limit != 10 && limit != 20) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be 5, 10, or 20"})
		return
	}
	if exchange != "binance" && exchange != "okx" && exchange != "bitget" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "depth exchange must be binance, okx, or bitget"})
		return
	}
	var depth *market.DepthSnapshot
	if exchange == "binance" && s.depthMarketClient != nil {
		normalizedSymbol, normalizeErr := market.NormalizeBinanceSymbol(symbol)
		if normalizeErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "symbol must be a Binance alphanumeric USDT contract"})
			return
		}
		depth, err = s.depthMarketClient.GetDepth(normalizedSymbol, limit)
	} else {
		provider, providerErr := market.NewMarketDataProvider(exchange)
		if providerErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": providerErr.Error()})
			return
		}
		depth, err = provider.GetDepth(provider.NormalizeSymbol(symbol), limit)
	}
	if err != nil {
		SafeInternalError(c, fmt.Sprintf("Get %s depth", exchange), err)
		return
	}
	c.JSON(http.StatusOK, depth)
}
