package httpx

import (
	"net/http"
	"strconv"
)

// The handlers read loosely-typed input from query strings and forms, where a
// missing or unparseable value is not an error but a fall back to the default.
// These keep that decision in one place.

func StringQuery(r *http.Request, name, defaultVal string) string {
	if v := r.URL.Query().Get(name); v != "" {
		return v
	}
	return defaultVal
}

func IntQuery(r *http.Request, name string, defaultVal int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return defaultVal
	}
	return v
}

func StringForm(r *http.Request, name, defaultVal string) string {
	if v := r.FormValue(name); v != "" {
		return v
	}
	return defaultVal
}

func IntForm(r *http.Request, name string, defaultVal int) int {
	v, err := strconv.Atoi(r.FormValue(name))
	if err != nil {
		return defaultVal
	}
	return v
}

// RefererOrDefault is where the "go back to where you came from" redirects get
// their target.
func RefererOrDefault(r *http.Request, defaultPath string) string {
	if referer := r.Header.Get("Referer"); referer != "" {
		return referer
	}
	return defaultPath
}
