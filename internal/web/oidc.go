package web

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/bjarke-xyz/rasende2/internal/lang"
	"github.com/bjarke-xyz/rasende2/internal/session"
)

// Login is delegated to an external OIDC provider (the shared auth server). This
// app is a confidential authorization-code + PKCE client: it never sees a
// password, and gets the user's identity and roles from the provider.

// HandleGetLogin starts the login by redirecting to the provider's /authorize,
// stashing the anti-CSRF state, PKCE verifier and return path in the session.
func (h *web) HandleGetLogin(w http.ResponseWriter, r *http.Request) {
	cfg := h.appContext.Config
	state := randomToken()
	verifier := randomToken()
	session.SetLoginFlow(w, r, state, verifier, returnPathFrom(r))

	authURL := cfg.OIDCIssuer + "/authorize?" + url.Values{
		"client_id":             {cfg.OIDCClientID},
		"redirect_uri":          {cfg.OIDCRedirectURI()},
		"response_type":         {"code"},
		"scope":                 {"openid email"},
		"state":                 {state},
		"code_challenge":        {pkceChallenge(verifier)},
		"code_challenge_method": {"S256"},
	}.Encode()
	http.Redirect(w, r, authURL, http.StatusSeeOther)
}

// HandleGetAuthCallback completes the login: verify state, exchange the code for
// tokens, read the user from /userinfo, and set the session. Admin is taken from
// the provider's role claim.
func (h *web) HandleGetAuthCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := r.URL.Query()

	state, verifier, returnPath, ok := session.LoginFlow(r)
	session.ClearLoginFlow(w, r)
	if !ok || q.Get("state") != state {
		session.AddFlashWarn(w, r, LangOf(r).T("auth.genericError"))
		http.Redirect(w, r, "/"+string(lang.Default), http.StatusSeeOther)
		return
	}
	returnPath = safeReturn(returnPath)
	if e := q.Get("error"); e != "" {
		log.Printf("oidc: callback error: %v (%v)", e, q.Get("error_description"))
		session.AddFlashWarn(w, r, LangOf(r).T("auth.genericError"))
		http.Redirect(w, r, returnPath, http.StatusSeeOther)
		return
	}

	tok, err := h.oidcExchange(ctx, q.Get("code"), verifier)
	if err != nil {
		log.Printf("oidc: token exchange: %v", err)
		session.AddFlashError(w, r, fmt.Errorf("%v", LangOf(r).T("auth.genericError")))
		http.Redirect(w, r, returnPath, http.StatusSeeOther)
		return
	}
	info, err := h.oidcUserinfo(ctx, tok.AccessToken)
	if err != nil {
		log.Printf("oidc: userinfo: %v", err)
		session.AddFlashError(w, r, fmt.Errorf("%v", LangOf(r).T("auth.genericError")))
		http.Redirect(w, r, returnPath, http.StatusSeeOther)
		return
	}

	session.SetUserID(w, r, info.Sub, hasRole(info.Roles, "admin"))
	session.AddFlashInfo(w, r, LangOf(r).T("auth.loggedIn"))
	http.Redirect(w, r, returnPath, http.StatusSeeOther)
}

// HandlePostLogout clears the session.
func (h *web) HandlePostLogout(w http.ResponseWriter, r *http.Request) {
	redirectPath := r.Header.Get("Referer")
	if redirectPath == "" {
		redirectPath = editionRoot(r)
	}
	session.ClearUserID(w, r)
	session.AddFlashInfo(w, r, LangOf(r).T("auth.loggedOut"))
	http.Redirect(w, r, redirectPath, http.StatusSeeOther)
}

// --- OIDC client calls ------------------------------------------------------

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

func (h *web) oidcExchange(ctx context.Context, code, verifier string) (tokenResponse, error) {
	cfg := h.appContext.Config
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {cfg.OIDCRedirectURI()},
		"code_verifier": {verifier},
		"client_id":     {cfg.OIDCClientID},
		"client_secret": {cfg.OIDCClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.OIDCIssuer+"/token", strings.NewReader(form.Encode()))
	if err != nil {
		return tokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return tokenResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return tokenResponse{}, fmt.Errorf("token endpoint status %d: %s", resp.StatusCode, body)
	}
	var tok tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tok); err != nil {
		return tokenResponse{}, err
	}
	return tok, nil
}

type userinfoResponse struct {
	Sub   string   `json:"sub"`
	Email string   `json:"email"`
	Roles []string `json:"roles"`
}

// oidcUserinfo reads the user from the provider. /userinfo verifies the access
// token's signature, so the identity and roles it returns are trustworthy without
// this client needing to validate the JWT itself.
func (h *web) oidcUserinfo(ctx context.Context, accessToken string) (userinfoResponse, error) {
	cfg := h.appContext.Config
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.OIDCIssuer+"/userinfo", nil)
	if err != nil {
		return userinfoResponse{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return userinfoResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return userinfoResponse{}, fmt.Errorf("userinfo status %d", resp.StatusCode)
	}
	var info userinfoResponse
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return userinfoResponse{}, err
	}
	return info, nil
}

// --- helpers ----------------------------------------------------------------

func hasRole(roles []string, want string) bool {
	for _, role := range roles {
		if role == want {
			return true
		}
	}
	return false
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("oidc: reading random bytes: %v", err))
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// returnPathFrom decides where to send the user after login: an explicit
// ?returnpath, else the page they came from, else the edition's front page.
func returnPathFrom(r *http.Request) string {
	if rp := r.URL.Query().Get("returnpath"); strings.HasPrefix(rp, "/") {
		return rp
	}
	if ref := r.Header.Get("Referer"); ref != "" {
		if u, err := url.Parse(ref); err == nil && strings.HasPrefix(u.Path, "/") {
			return u.Path
		}
	}
	return editionRoot(r)
}

// safeReturn refuses anything that is not a local, non-protocol-relative path, so
// the return path can never become an open redirect.
func safeReturn(p string) string {
	if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") {
		return p
	}
	return "/" + string(lang.Default)
}
