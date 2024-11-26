package web

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/mail"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/internal/repository/db/dao"
	"github.com/bjarke-xyz/rasende2/internal/web/auth"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/bjarke-xyz/rasende2/pkg"
	"github.com/gin-gonic/gin"
)

func (w *web) HandleGetLogin(c *gin.Context) {
	showOtp := c.Query("otp") == "true"
	email := c.Query("email")
	returnPath := c.Query("returnpath")
	c.HTML(http.StatusOK, "", components.Login(components.LoginViewModel{
		Base:       w.getBaseModel(c, "Login | Rasende"),
		OTP:        showOtp,
		Email:      email,
		ReturnPath: returnPath,
	}))
}

func (w *web) notifyUserCreated(user dao.User) {
	msg := fmt.Sprintf("rasende: new user created: %v (%v)", user.Email, user.ID)
	err := w.appContext.Infra.Mail.Send(mail.SendMailRequest{
		Receiver: w.appContext.Config.AdminEmail,
		Type:     "new_user",
		Subject:  msg,
		Message:  msg,
	})
	if err != nil {
		log.Printf("failed to send mail: %v", err)
	}
}

func (w *web) HandlePostLogin(c *gin.Context) {
	ctx := c.Request.Context()
	successPath := StringForm(c, "returnPath", "/")
	redirectPath := RefererOrDefault(c, w.appContext.Config.BaseUrl+"/login")
	redirectPathUrl, err := url.Parse(redirectPath)
	if err != nil {
		AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	email := c.Request.FormValue("email")
	if !strings.Contains(email, "@") {
		AddFlashWarn(c, "Invalid email")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	db, err := db.OpenQueries(w.appContext.Config)
	if err != nil {
		AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	}
	user, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			user, err = db.CreateUser(ctx, dao.CreateUserParams{
				Email: email,
			})
			if err != nil {
				AddFlashError(c, err)
				c.Redirect(http.StatusSeeOther, redirectPath)
				return
			}
			w.notifyUserCreated(user)
		} else {
			AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
	}
	// TODO: support password login
	// password := c.Request.FormValue("password")
	formOtp := strings.TrimSpace(strings.ReplaceAll(c.Request.FormValue("otp"), "-", ""))
	if formOtp != "" {
		magicLinks, err := db.GetLinksByUserId(ctx, user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				AddFlashWarn(c, "Koden virker ikke")
				c.Redirect(http.StatusSeeOther, redirectPath)
				return
			}
			AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
		for _, magicLink := range magicLinks {
			if pkg.CheckPasswordHash(formOtp, magicLink.OtpHash) {
				db.DeleteMagicLink(ctx, magicLink.ID)
				err = db.SetUserEmailConfirmed(ctx, magicLink.UserID)
				if err != nil {
					log.Printf("error setting user %v email confirmed: %v", magicLink.UserID, err)
				}
				user, err := db.GetUser(ctx, magicLink.UserID)
				if err != nil {
					AddFlashError(c, fmt.Errorf("der skete en fejl"))
					log.Printf("failed to get user by id %v: %v", magicLink.UserID, err)
					c.Redirect(http.StatusSeeOther, redirectPath)
					return
				}
				auth.SetUserId(c, user.ID, user.IsAdmin)
				AddFlashInfo(c, "Du er nu logget ind!")
				c.Redirect(http.StatusSeeOther, successPath)
				return
			}
		}
		AddFlashWarn(c, "Koden virker ikke")
		c.Redirect(http.StatusSeeOther, redirectPath)
		return
	} else {
		// login link
		otp, err := pkg.GenerateOTP()
		if err != nil {
			AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
		otpHash, err := pkg.HashPassword(otp)
		if err != nil {
			AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
		linkCode, err := pkg.GenerateSecureToken()
		if err != nil {
			AddFlashError(c, err)
			c.Redirect(http.StatusSeeOther, redirectPath)
			return
		}
		expiresAt := time.Now().Add(15 * time.Minute)
		db.CreateMagicLink(ctx, dao.CreateMagicLinkParams{
			UserID:    user.ID,
			OtpHash:   otpHash,
			LinkCode:  linkCode,
			ExpiresAt: expiresAt,
		})
		// TODO: check if user exists before sending mail
		// TODO: reutrn path query param
		w.appContext.Infra.Mail.SendAuthLink(mail.SendAuthLinkRequest{
			Receiver:            email,
			CodePath:            fmt.Sprintf("/login-link?code=%v&returnpath=%v", linkCode, url.QueryEscape(successPath)),
			OTP:                 otp,
			ExpirationTimestamp: expiresAt,
		})
		AddFlashInfo(c, "Tjek din mail!")
		redirectQuery := redirectPathUrl.Query()
		redirectQuery.Set("otp", "true")
		redirectQuery.Set("email", email)
		redirectPathUrl.RawQuery = redirectQuery.Encode()
		c.Redirect(http.StatusSeeOther, redirectPathUrl.String())
	}
}

func (w *web) HandleGetLoginLink(c *gin.Context) {
	ctx := c.Request.Context()
	code := c.Query("code")
	successPath := StringQuery(c, "returnpath", "/")
	failurePath := "/"
	if code == "" {
		c.Redirect(http.StatusSeeOther, failurePath)
		return
	}
	db, err := db.OpenQueries(w.appContext.Config)
	if err != nil {
		AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, failurePath)
		return
	}
	magicLink, err := db.GetLinkByCode(ctx, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			AddFlashWarn(c, "Linket virker ikke")
			c.Redirect(http.StatusSeeOther, failurePath)
			return
		}
		AddFlashError(c, err)
		c.Redirect(http.StatusSeeOther, failurePath)
		return
	}

	err = db.SetUserEmailConfirmed(ctx, magicLink.UserID)
	if err != nil {
		log.Printf("error setting user %v email confirmed: %v", magicLink.UserID, err)
	}

	err = db.DeleteMagicLink(ctx, magicLink.ID)
	if err != nil {
		log.Printf("error deleting magic link: %v", err)
	}

	user, err := db.GetUser(ctx, magicLink.UserID)
	if err != nil {
		AddFlashError(c, fmt.Errorf("der skete en fejl"))
		log.Printf("failed to get user by id %v: %v", magicLink.UserID, err)
		c.Redirect(http.StatusSeeOther, failurePath)
		return
	}

	AddFlashInfo(c, "Du er nu logget ind!")
	auth.SetUserId(c, user.ID, user.IsAdmin)
	c.Redirect(http.StatusSeeOther, successPath)
}

func (w *web) HandlePostLogout(c *gin.Context) {
	redirectPath := c.Request.Header.Get("Referer")
	if redirectPath == "" {
		redirectPath = "/"
	}
	auth.ClearUserId(c)
	AddFlashInfo(c, "Du er nu logget ud!")
	c.Redirect(http.StatusSeeOther, redirectPath)
}
