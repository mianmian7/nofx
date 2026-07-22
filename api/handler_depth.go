package api

import (
	"net/http"
	"strconv"
	"strings"

	"nofx/market"

	"github.com/gin-gonic/gin"
)

type depthMarketClient interface {
	GetDepth(symbol string, limit int) (*market.BinanceDepthSnapshot, error)
}

func (s *Server) handleDepth(c *gin.Context) {
	symbol := strings.TrimSpace(c.Query("symbol"))
	if symbol == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol parameter is required"})
		return
	}
	normalizedSymbol, err := market.NormalizeBinanceSymbol(symbol)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "symbol must be a Binance alphanumeric USDT contract"})
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if err != nil || (limit != 5 && limit != 10 && limit != 20) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "limit must be 5, 10, or 20"})
		return
	}
	client := s.depthMarketClient
	if client == nil {
		client = market.NewAPIClient()
	}
	depth, err := client.GetDepth(normalizedSymbol, limit)
	if err != nil {
		SafeInternalError(c, "Get Binance depth", err)
		return
	}
	c.JSON(http.StatusOK, depth)
}
