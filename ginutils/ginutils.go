package ginutils

import (
	"fmt"
	"strconv"

	"github.com/gin-gonic/gin"
)

func IntQuery(c *gin.Context, query string, defaultVal int) int {
	valStr := c.DefaultQuery(query, fmt.Sprintf("%v", defaultVal))
	val, err := strconv.Atoi(valStr)
	if err != nil {
		val = defaultVal
	}
	return val
}

func Float32Query(c *gin.Context, query string, defaultVal float32) float32 {
	valStr := c.DefaultQuery(query, fmt.Sprintf("%v", defaultVal))
	val, err := strconv.ParseFloat(valStr, 32)
	if err != nil {
		val = float64(defaultVal)
	}
	return float32(val)
}

func StringQuery(c *gin.Context, query string, defaultVal string) string {
	val := c.DefaultQuery(query, defaultVal)
	return val
}
