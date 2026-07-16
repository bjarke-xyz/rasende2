// Package session holds the signed cookie that carries the login and the flash
// messages.
//
// It calls gorilla/sessions directly. gin-contrib/sessions, which this replaces,
// was only a thin adapter over exactly this library, so the cookie on disk is
// unchanged — same name, same gob encoding, same HMAC — and sessions issued by
// the previous build keep working.
package session

import (
	"context"
	"log"
	"net/http"

	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/httpx"
	"github.com/gorilla/sessions"
)

// cookieName is load-bearing: change it and every logged-in visitor is logged out.
const cookieName = "mysession"

const (
	keyUserID = "userid"
	keyAdmin  = "admin"

	// Transient state for the in-progress OIDC login (cleared once completed).
	keyLoginState    = "login_state"
	keyLoginVerifier = "login_verifier"
	keyLoginReturn   = "login_return"
)

// NewStore builds the cookie store. The secret only authenticates the cookie, it
// does not encrypt it: the contents are readable by the client, so nothing may go
// in here that the client is not allowed to see.
//
// secure should be true in production (HTTPS). It is left off in development so
// the cookie works over plain http; SameSite=Lax is enough for the OIDC callback,
// which arrives as a top-level GET navigation.
func NewStore(secret string, secure bool) sessions.Store {
	store := sessions.NewCookieStore([]byte(secret))
	store.Options = &sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 30,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,

		// gorilla's zero value leaves this false, which let any script on the page
		// read the login cookie. Nothing needs it from JS.
		HttpOnly: true,
	}
	return store
}

type storeKey struct{}

// Middleware makes the store reachable from the request, which is what lets the
// helpers below take nothing but (w, r) and still be callable from any handler.
func Middleware(store sessions.Store) httpx.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), storeKey{}, store)))
		})
	}
}

// get returns the request's session, decoding the cookie on first use.
//
// gorilla caches it on the request, so the repeated calls the handlers make —
// getBaseModel alone asks for flashes three times — decode once.
func get(r *http.Request) (*sessions.Session, bool) {
	store, ok := r.Context().Value(storeKey{}).(sessions.Store)
	if !ok {
		log.Printf("session: no store on request; is the middleware installed?")
		return nil, false
	}
	// An error here means the cookie was unreadable (wrong secret, tampering, an
	// old format). gorilla still hands back a usable empty session, which is what
	// we want: treat the visitor as logged out rather than failing the request.
	s, err := store.Get(r, cookieName)
	if err != nil {
		log.Printf("session: decoding %q: %v", cookieName, err)
	}
	return s, s != nil
}

func save(w http.ResponseWriter, r *http.Request, s *sessions.Session) {
	if err := s.Save(r, w); err != nil {
		log.Printf("session: save: %v", err)
	}
}

// --- login ------------------------------------------------------------------

// SetUserID records the logged-in user. userID is the OIDC subject (a stable
// opaque string from the auth server); admin comes from the token's role claim.
func SetUserID(w http.ResponseWriter, r *http.Request, userID string, admin bool) {
	s, ok := get(r)
	if !ok {
		return
	}
	s.Values[keyUserID] = userID
	s.Values[keyAdmin] = admin
	save(w, r, s)
}

func ClearUserID(w http.ResponseWriter, r *http.Request) {
	s, ok := get(r)
	if !ok {
		return
	}
	delete(s.Values, keyUserID)
	delete(s.Values, keyAdmin)
	save(w, r, s)
}

// UserID returns the logged-in user's OIDC subject and whether anyone is logged in.
func UserID(r *http.Request) (string, bool) {
	s, ok := get(r)
	if !ok {
		return "", false
	}
	userID, ok := s.Values[keyUserID].(string)
	return userID, ok && userID != ""
}

// --- OIDC login flow --------------------------------------------------------

// SetLoginFlow stashes the transient state of an in-progress OIDC login: the
// anti-CSRF state, the PKCE verifier, and where to send the user afterwards.
func SetLoginFlow(w http.ResponseWriter, r *http.Request, state, verifier, returnPath string) {
	s, ok := get(r)
	if !ok {
		return
	}
	s.Values[keyLoginState] = state
	s.Values[keyLoginVerifier] = verifier
	s.Values[keyLoginReturn] = returnPath
	save(w, r, s)
}

// LoginFlow returns the stashed login flow, if any.
func LoginFlow(r *http.Request) (state, verifier, returnPath string, ok bool) {
	s, sok := get(r)
	if !sok {
		return "", "", "", false
	}
	state, _ = s.Values[keyLoginState].(string)
	verifier, _ = s.Values[keyLoginVerifier].(string)
	returnPath, _ = s.Values[keyLoginReturn].(string)
	return state, verifier, returnPath, state != "" && verifier != ""
}

// ClearLoginFlow removes the transient login state once the callback is handled.
func ClearLoginFlow(w http.ResponseWriter, r *http.Request) {
	s, ok := get(r)
	if !ok {
		return
	}
	delete(s.Values, keyLoginState)
	delete(s.Values, keyLoginVerifier)
	delete(s.Values, keyLoginReturn)
	save(w, r, s)
}

func IsAdmin(r *http.Request) bool {
	s, ok := get(r)
	if !ok {
		return false
	}
	admin, _ := s.Values[keyAdmin].(bool)
	return admin
}

// --- flashes ----------------------------------------------------------------

// AddFlash queues a message to be shown on the next page the visitor loads.
func AddFlash(w http.ResponseWriter, r *http.Request, flashType, msg string) {
	s, ok := get(r)
	if !ok {
		return
	}
	s.AddFlash(msg, flashType)
	save(w, r, s)
}

func AddFlashInfo(w http.ResponseWriter, r *http.Request, msg string) {
	AddFlash(w, r, core.FlashTypeInfo, msg)
}

func AddFlashWarn(w http.ResponseWriter, r *http.Request, msg string) {
	AddFlash(w, r, core.FlashTypeWarn, msg)
}

func AddFlashError(w http.ResponseWriter, r *http.Request, err error) {
	AddFlash(w, r, core.FlashTypeError, err.Error())
}

// Flashes reads the queued messages and clears them, so they are shown once.
// The save is what performs the clearing.
//
// It saves even when there was nothing to clear, which looks wasteful and is:
// every page render asks for three flash types and so re-issues the cookie three
// times. It is kept because the re-issue is what slides the 30-day expiry
// forward on each visit. Saving only when a flash was consumed would quietly
// turn the session into a fixed 30-days-from-login window and log active
// visitors out.
func Flashes(w http.ResponseWriter, r *http.Request, flashType string) []string {
	s, ok := get(r)
	if !ok {
		return nil
	}
	flashes := s.Flashes(flashType)
	msgs := make([]string, 0, len(flashes))
	for _, flash := range flashes {
		msg, ok := flash.(string)
		if !ok {
			log.Printf("session: flash is not a string: %v", flash)
			continue
		}
		msgs = append(msgs, msg)
	}
	save(w, r, s)
	return msgs
}
