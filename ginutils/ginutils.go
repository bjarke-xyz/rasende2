package ginutils

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func RefererOrDefault(c *gin.Context, defaultPath string) string {
	referer := c.Request.Header.Get("Referer")
	if referer != "" {
		return referer
	}
	return defaultPath
}

func IntQuery(c *gin.Context, query string, defaultVal int) int {
	valStr := c.DefaultQuery(query, fmt.Sprintf("%v", defaultVal))
	val, err := strconv.Atoi(valStr)
	if err != nil {
		val = defaultVal
	}
	return val
}

func IntForm(c *gin.Context, name string, defaultVal int) int {
	valStr := c.Request.FormValue(name)
	if valStr == "" {
		return defaultVal
	}
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

func StringForm(c *gin.Context, name string, defaultVal string) string {
	val := c.Request.FormValue(name)
	if val == "" {
		return defaultVal
	}
	return val
}

func IsAdmin(c *gin.Context) bool {
	session := sessions.Default(c)
	adminIface := session.Get("admin")
	admin, ok := adminIface.(bool)
	if !ok {
		return false
	}
	return admin
}

func RenderToStringCtx(ctx context.Context, component templ.Component) string {
	buffer := &strings.Builder{}
	component.Render(ctx, buffer)
	return buffer.String()
}
func RenderToString(c *gin.Context, component templ.Component) string {
	return RenderToStringCtx(c.Request.Context(), component)
}

const FlashTypeInfo = "Info"
const FlashTypeWarn = "Warn"
const FlashTypeError = "Error"

func AddFlash(c *gin.Context, flashType string, msg string) {
	session := sessions.Default(c)
	// Adding the message with the flashType as a key
	session.AddFlash(msg, flashType)
	err := session.Save()
	if err != nil {
		log.Printf("error saving flash: %v", err)
	}
}

func GetFlashes(c *gin.Context, flashType string) []string {
	session := sessions.Default(c)
	flashes := session.Flashes(flashType)
	flashStrings := make([]string, len(flashes))
	for i, flash := range flashes {
		if flashStr, ok := flash.(string); ok {
			flashStrings[i] = flashStr
		} else {
			log.Printf("warning: flash message is not a string, got: %v", flash)
		}
	}

	// Save session to persist changes after retrieving flashes
	err := session.Save()
	if err != nil {
		log.Printf("error saving session after getting flashes: %v", err)
	}

	return flashStrings
}

func AddFlashInfo(c *gin.Context, msg string) {
	AddFlash(c, FlashTypeInfo, msg)
}
func AddFlashWarn(c *gin.Context, msg string) {
	AddFlash(c, FlashTypeWarn, msg)
}
func AddFlashError(c *gin.Context, err error) {
	AddFlash(c, FlashTypeError, err.Error())
}
