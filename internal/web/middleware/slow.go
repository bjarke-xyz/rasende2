package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
)

// Slow is used to test slow HTTP requests locally
func Slow(delay time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		if delay.Milliseconds() <= 0 {
			c.Next()
			return
		}
		time.Sleep(delay)
		c.Next()
	}
}
