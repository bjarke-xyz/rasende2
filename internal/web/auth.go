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

	"github.com/bjarke-xyz/rasende2/internal/httpx"
	"github.com/bjarke-xyz/rasende2/internal/mail"
	"github.com/bjarke-xyz/rasende2/internal/repository/db"
	"github.com/bjarke-xyz/rasende2/internal/session"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/bjarke-xyz/rasende2/pkg"
)

func (h *web) HandleGetLogin(w http.ResponseWriter, r *http.Request) {
	showOtp := r.URL.Query().Get("otp") == "true"
	email := r.URL.Query().Get("email")
	returnPath := r.URL.Query().Get("returnpath")
	model := components.LoginViewModel{
		Base:       h.getBaseModel(w, r, LangOf(r).T("page.login")),
		OTP:        showOtp,
		Email:      email,
		ReturnPath: returnPath,
	}
	h.renderer.Page(w, r, http.StatusOK, "login", model.Base, model)
}

func (h *web) HandlePostLogin(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	successPath := httpx.StringForm(r, "returnPath", editionRoot(r))
	redirectPath := httpx.RefererOrDefault(r, h.appContext.Config.BaseUrl+editionRoot(r)+"/login")
	redirectPathUrl, err := url.Parse(redirectPath)
	if err != nil {
		session.AddFlashError(w, r, err)
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}
	email := r.FormValue("email")
	if !strings.Contains(email, "@") {
		session.AddFlashWarn(w, r, LangOf(r).T("auth.invalidEmail"))
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}
	db, err := db.OpenQueries(h.appContext.Config)
	if err != nil {
		session.AddFlashError(w, r, err)
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	}
	user, err := db.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			session.AddFlashWarn(w, r, LangOf(r).T("auth.userNotFound"))
			http.Redirect(w, r, redirectPath, http.StatusSeeOther)
			return
		} else {
			session.AddFlashError(w, r, err)
			http.Redirect(w, r, redirectPath, http.StatusSeeOther)
			return
		}
	}
	// TODO: support password login
	// password := r.FormValue("password")
	formOtp := strings.TrimSpace(strings.ReplaceAll(r.FormValue("otp"), "-", ""))
	if formOtp != "" {
		magicLinks, err := db.GetLinksByUserId(ctx, user.ID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				session.AddFlashWarn(w, r, LangOf(r).T("auth.badCode"))
				http.Redirect(w, r, redirectPath, http.StatusSeeOther)
				return
			}
			session.AddFlashError(w, r, err)
			http.Redirect(w, r, redirectPath, http.StatusSeeOther)
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
					session.AddFlashError(w, r, fmt.Errorf("%v", LangOf(r).T("auth.genericError")))
					log.Printf("failed to get user by id %v: %v", magicLink.UserID, err)
					http.Redirect(w, r, redirectPath, http.StatusSeeOther)
					return
				}
				session.SetUserID(w, r, user.ID, user.IsAdmin)
				session.AddFlashInfo(w, r, LangOf(r).T("auth.loggedIn"))
				http.Redirect(w, r, successPath, http.StatusSeeOther)
				return
			}
		}
		session.AddFlashWarn(w, r, LangOf(r).T("auth.badCode"))
		http.Redirect(w, r, redirectPath, http.StatusSeeOther)
		return
	} else {
		// login link
		otp, err := pkg.GenerateOTP()
		if err != nil {
			session.AddFlashError(w, r, err)
			http.Redirect(w, r, redirectPath, http.StatusSeeOther)
			return
		}
		otpHash, err := pkg.HashPassword(otp)
		if err != nil {
			session.AddFlashError(w, r, err)
			http.Redirect(w, r, redirectPath, http.StatusSeeOther)
			return
		}
		linkCode, err := pkg.GenerateSecureToken()
		if err != nil {
			session.AddFlashError(w, r, err)
			http.Redirect(w, r, redirectPath, http.StatusSeeOther)
			return
		}
		expiresAt := time.Now().Add(15 * time.Minute)
		db.CreateMagicLink(ctx, user.ID, otpHash, linkCode, expiresAt)
		// TODO: check if user exists before sending mail
		// TODO: reutrn path query param
		// The link has to come back into the edition the visitor left from, or
		// they finish signing in on the wrong side of the site.
		l := LangOf(r)
		h.appContext.Infra.Mail.SendAuthLink(mail.SendAuthLinkRequest{
			Receiver:            email,
			CodePath:            fmt.Sprintf("/%v/login-link?code=%v&returnpath=%v", l.Code, linkCode, url.QueryEscape(successPath)),
			OTP:                 otp,
			ExpirationTimestamp: expiresAt,
			Lang:                l,
		})
		session.AddFlashInfo(w, r, LangOf(r).T("auth.checkMail"))
		redirectQuery := redirectPathUrl.Query()
		redirectQuery.Set("otp", "true")
		redirectQuery.Set("email", email)
		redirectPathUrl.RawQuery = redirectQuery.Encode()
		http.Redirect(w, r, redirectPathUrl.String(), http.StatusSeeOther)
	}
}

func (h *web) HandleGetLoginLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	code := r.URL.Query().Get("code")
	successPath := httpx.StringQuery(r, "returnpath", editionRoot(r))
	failurePath := editionRoot(r)
	if code == "" {
		http.Redirect(w, r, failurePath, http.StatusSeeOther)
		return
	}
	db, err := db.OpenQueries(h.appContext.Config)
	if err != nil {
		session.AddFlashError(w, r, err)
		http.Redirect(w, r, failurePath, http.StatusSeeOther)
		return
	}
	magicLink, err := db.GetLinkByCode(ctx, code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			session.AddFlashWarn(w, r, LangOf(r).T("auth.badLink"))
			http.Redirect(w, r, failurePath, http.StatusSeeOther)
			return
		}
		session.AddFlashError(w, r, err)
		http.Redirect(w, r, failurePath, http.StatusSeeOther)
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
		session.AddFlashError(w, r, fmt.Errorf("%v", LangOf(r).T("auth.genericError")))
		log.Printf("failed to get user by id %v: %v", magicLink.UserID, err)
		http.Redirect(w, r, failurePath, http.StatusSeeOther)
		return
	}

	session.AddFlashInfo(w, r, LangOf(r).T("auth.loggedIn"))
	session.SetUserID(w, r, user.ID, user.IsAdmin)
	http.Redirect(w, r, successPath, http.StatusSeeOther)
}

func (h *web) HandlePostLogout(w http.ResponseWriter, r *http.Request) {
	redirectPath := r.Header.Get("Referer")
	if redirectPath == "" {
		redirectPath = editionRoot(r)
	}
	session.ClearUserID(w, r)
	session.AddFlashInfo(w, r, LangOf(r).T("auth.loggedOut"))
	http.Redirect(w, r, redirectPath, http.StatusSeeOther)
}
