package auth

import (
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

func SetUserId(c *gin.Context, userId int64, admin bool) {
	session := sessions.Default(c)
	session.Set("userid", userId)
	session.Set("admin", admin)
	session.Save()
}

func ClearUserId(c *gin.Context) {
	session := sessions.Default(c)
	session.Delete("userid")
	session.Delete("admin")
	session.Save()
}

func GetUserId(c *gin.Context) (userId int64, ok bool) {
	session := sessions.Default(c)
	userIdIface := session.Get("userid")
	if userId, ok := userIdIface.(int64); ok {
		return userId, true
	}
	return 0, false
}

func IsAdmin(c *gin.Context) bool {
	session := sessions.Default(c)
	adminIface := session.Get("admin")
	if admin, ok := adminIface.(bool); ok {
		return admin
	}
	return false
}
