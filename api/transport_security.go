package api

import (
	"net/http"
	"nofx/config"

	"github.com/gin-gonic/gin"
)

// requireTransportEncryption makes credential-bearing endpoints fail closed.
// A disabled flag must never become permission to accept API keys over plaintext.
func requireTransportEncryption(c *gin.Context) bool {
	cfg := config.Get()
	if cfg != nil && cfg.TransportEncryption {
		return true
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": "transport encryption is required for sensitive configuration writes",
		"code":  "ENCRYPTION_REQUIRED",
	})
	return false
}
