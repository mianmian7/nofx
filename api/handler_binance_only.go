package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// handleHyperliquidGone keeps retired Hyperliquid/XYZ/Vergex routes explicit
// for older clients while guaranteeing that no disabled venue side effect occurs.
func (s *Server) handleHyperliquidGone(c *gin.Context) {
	c.JSON(http.StatusGone, gin.H{
		"error":     "This Hyperliquid/Vergex route is disabled; use Binance market and execution paths instead",
		"error_key": "product.hyperliquid_disabled",
	})
}
