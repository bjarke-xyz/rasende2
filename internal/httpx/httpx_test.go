package httpx

import (
	"strings"
	"testing"
)

// The exact bytes matter. These goldens were captured from gin-contrib/sse, the
// encoder this replaced, so the wire format is unchanged by the migration.
func TestSSEvent(t *testing.T) {
	tests := []struct {
		name  string
		event string
		data  string
		want  string
	}{
		{
			name:  "single line",
			event: "sse-close",
			data:  "sse-close",
			want:  "event:sse-close\ndata:sse-close\n\n",
		},
		{
			// A rendered template, which is what the title/button/image events
			// actually carry. Every continuation line needs its own prefix or the
			// browser drops it.
			name:  "multi line",
			event: "title",
			data:  "\n<p><a href=\"x\">Et</a></p>\n",
			want:  "event:title\ndata:\ndata:<p><a href=\"x\">Et</a></p>\ndata:\n\n",
		},
		{
			name:  "carriage return is escaped, not framed",
			event: "content",
			data:  "a\rb",
			want:  "event:content\ndata:a\\rb\n\n",
		},
		{
			name:  "empty data",
			event: "ping",
			data:  "",
			want:  "event:ping\ndata:\n\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			if err := SSEvent(&b, tc.event, tc.data); err != nil {
				t.Fatalf("SSEvent: %v", err)
			}
			if got := b.String(); got != tc.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

// Whatever the payload, the result must parse as a sequence of SSE fields:
// no bare continuation lines.
func TestSSEventIsAlwaysWellFormed(t *testing.T) {
	payloads := []string{
		"plain",
		"line one\nline two",
		"\nleading newline",
		"trailing newline\n",
		"\n\n\n",
		"windows\r\nstyle",
	}

	for _, payload := range payloads {
		var b strings.Builder
		if err := SSEvent(&b, "e", payload); err != nil {
			t.Fatalf("SSEvent: %v", err)
		}
		out := strings.TrimSuffix(b.String(), "\n\n")
		for i, line := range strings.Split(out, "\n") {
			if !strings.HasPrefix(line, "event:") && !strings.HasPrefix(line, "data:") {
				t.Errorf("payload %q: line %d is not an SSE field: %q", payload, i, line)
			}
		}
	}
}
