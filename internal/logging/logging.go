// Package logging configures the process-wide logger from the environment.
//
//	LOG_FORMAT  json (default) | text     json for Loki, text for a terminal.
//	LOG_LEVEL   debug | info (default) | warn | error
//
// Setup also captures the standard log package: slog.SetDefault redirects
// log.Printf through this handler, so a call left behind here or sitting in a
// dependency still comes out as a structured record instead of a bare line.
package logging

import (
	"log/slog"
	"os"
	"strings"
)

func Setup() {
	opts := &slog.HandlerOptions{Level: level(), AddSource: true}
	var h slog.Handler = slog.NewJSONHandler(os.Stderr, opts)
	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "text") {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	slog.SetDefault(slog.New(h))
}

// level reads LOG_LEVEL. slog.Level parses its own text — "debug", "INFO",
// even "warn+2" — so there is no table here to drift out of sync. A value that
// does not parse is not worth refusing to start over; info is the safe answer.
func level() slog.Level {
	var l slog.Level // zero value is Info
	if s := os.Getenv("LOG_LEVEL"); s != "" {
		_ = l.UnmarshalText([]byte(s)) // leaves l untouched on failure
	}
	return l
}
