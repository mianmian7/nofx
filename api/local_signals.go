package api

import (
	"net/http"
	"nofx/signals"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"
)

var localSignalSymbol = regexp.MustCompile(`^[A-Z0-9]{2,30}USDT$`)

func (s *Server) handleLocalSignals(c *gin.Context) {
	exchange := c.DefaultQuery("exchange", "binance")
	if !signals.Supported(exchange) {
		SafeBadRequest(c, "Choose binance, okx or bitget")
		return
	}
	service := signals.Default()
	if service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Market analysis is not running"})
		return
	}
	snapshot, err := service.Snapshot(exchange)
	if err != nil {
		SafeInternalError(c, "Failed to read market analysis", err)
		return
	}
	c.JSON(http.StatusOK, snapshot)
}

func (s *Server) handleLocalSignalHistory(c *gin.Context) {
	exchange := c.DefaultQuery("exchange", "binance")
	symbol := c.Query("symbol")
	if !signals.Supported(exchange) || !localSignalSymbol.MatchString(symbol) {
		SafeBadRequest(c, "A supported exchange and canonical USDT symbol are required")
		return
	}
	service := signals.Default()
	if service == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Market analysis is not running"})
		return
	}
	before, err := strconv.ParseInt(c.DefaultQuery("before", "0"), 10, 64)
	if err != nil || before < 0 {
		SafeBadRequest(c, "Invalid history cursor")
		return
	}
	history, err := service.HistoryBefore(exchange, symbol, before, 96)
	if err != nil {
		SafeInternalError(c, "Failed to read analysis history", err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"history": history, "retention_days": 30})
}
