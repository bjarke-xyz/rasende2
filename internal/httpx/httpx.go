// Package httpx is the small amount of net/http plumbing that a web framework
// would otherwise provide: a middleware chain, an access log, a panic barrier,
// a server-sent-event encoder, and the response helpers the handlers write
// through.
//
// It exists because this application used almost none of what a framework
// offers — no request binding, no validation, no framework-side template
// rendering — so the framework was paying for itself with about this much code.
package httpx

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"slices"
	"strings"
	"time"
)

// Middleware wraps a handler. Chain applies them so that the first is outermost,
// which is the order they are written in.
type Middleware func(http.Handler) http.Handler

func Chain(h http.Handler, middlewares ...Middleware) http.Handler {
	for _, m := range slices.Backward(middlewares) {
		h = m(h)
	}
	return h
}

// responseWriter records what the handler did, which neither the access log nor
// the panic barrier can otherwise see.
//
// Unwrap is what keeps streaming working: http.ResponseController walks it to
// reach the real writer's Flush, so wrapping the response does not silently cost
// the SSE handlers their ability to flush.
type responseWriter struct {
	http.ResponseWriter
	status int
	size   int
}

func (w *responseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.size += n
	return n, err
}

func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// written reports whether a status line has gone out, after which the response
// can no longer be changed.
func (w *responseWriter) written() bool { return w.status != 0 }

// wrap is idempotent, so Logger and Recovery can each ask for a recording writer
// without caring which of them is on the outside.
func wrap(w http.ResponseWriter) *responseWriter {
	if rw, ok := w.(*responseWriter); ok {
		return rw
	}
	return &responseWriter{ResponseWriter: w}
}

// Logger writes one line per request.
func Logger(trustCloudflare bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := wrap(w)
			next.ServeHTTP(rw, r)

			path := r.URL.Path
			if r.URL.RawQuery != "" {
				path += "?" + r.URL.RawQuery
			}
			status := rw.status
			if status == 0 {
				status = http.StatusOK // handler wrote nothing; net/http will send 200
			}
			// duration_ms is a float rather than a time.Duration because the two
			// handlers encode a Duration differently — JSON writes raw nanoseconds,
			// text writes "1.23ms" — which would make the field's type depend on
			// LOG_FORMAT.
			slog.Info("request",
				"method", r.Method,
				"path", path,
				"status", status,
				"duration_ms", float64(time.Since(start).Microseconds())/1000,
				"ip", clientIP(r, trustCloudflare),
			)
		})
	}
}

// clientIP is only ever used for the log line. In production the app sits behind
// Cloudflare, which is the only party allowed to name the client.
func clientIP(r *http.Request, trustCloudflare bool) string {
	if trustCloudflare {
		if ip := r.Header.Get("CF-Connecting-IP"); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Recovery keeps one broken request from taking the process down.
func Recovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := wrap(w)
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			// ErrAbortHandler is how a handler says "stop, quietly". net/http
			// expects to receive it.
			if rec == http.ErrAbortHandler {
				panic(rec)
			}
			// The stack is its own attribute rather than part of the message:
			// under the JSON handler that keeps the whole trace in one record
			// and one Loki event, recoverable with `jq -r .stack`.
			slog.Error("panic serving request",
				"method", r.Method,
				"path", r.URL.Path,
				"panic", fmt.Sprint(rec),
				"stack", string(debug.Stack()),
			)

			// A stream that has already sent bytes cannot be turned into a 500;
			// writing one would append garbage to a response the client is
			// mid-way through reading. Dropping the connection is all that is left.
			if rw.written() {
				return
			}
			rw.WriteHeader(http.StatusInternalServerError)
		}()
		next.ServeHTTP(rw, r)
	})
}

// --- server-sent events -----------------------------------------------------

// sseDataReplacer reproduces gin-contrib/sse's framing exactly.
var sseDataReplacer = strings.NewReplacer("\n", "\ndata:", "\r", "\\r")

// SSEvent writes one server-sent event.
//
// Every line of the payload gets its own "data:" prefix. This matters more than
// it looks: the payloads here are rendered HTML templates and those are
// multi-line, and a continuation line that is not prefixed is not part of the
// event — the browser discards it, with no error anywhere. Emitting
// "data: <payload>" with a lone Fprintf silently truncates every multi-line
// event to its first line.
func SSEvent(w io.Writer, event, data string) error {
	var b strings.Builder
	b.WriteString("event:")
	b.WriteString(event)
	b.WriteString("\ndata:")
	b.WriteString(sseDataReplacer.Replace(data))
	b.WriteString("\n\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// Flush pushes what has been written to the client. It goes through
// ResponseController rather than a http.Flusher assertion so that it still works
// when the writer is wrapped by the middleware above.
func Flush(w http.ResponseWriter) {
	if err := http.NewResponseController(w).Flush(); err != nil {
		slog.Warn("sse: flush failed", "error", err)
	}
}

// SSEHeaders prepares the response for streaming. Call before the first event.
func SSEHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "close")
	h.Set("Transfer-Encoding", "chunked")
}

// --- responses --------------------------------------------------------------

func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("http: writing json failed", "error", err)
	}
}

func String(w http.ResponseWriter, status int, format string, args ...any) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	var err error
	if len(args) == 0 {
		// Not Fprintf: a bare format string may legitimately contain a '%'.
		_, err = io.WriteString(w, format)
	} else {
		_, err = fmt.Fprintf(w, format, args...)
	}
	if err != nil {
		slog.Error("http: writing string failed", "error", err)
	}
}
