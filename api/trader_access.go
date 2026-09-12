package api

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// Missing and unowned IDs intentionally have the same response. Database
// failures must remain errors, never permission to consult the runtime map.
func respondTraderStoreError(c *gin.Context, message string, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Trader does not exist or no access permission"})
		return
	}
	SafeInternalError(c, message, err)
}
