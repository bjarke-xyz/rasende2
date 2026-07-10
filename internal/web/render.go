package web

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bjarke-xyz/rasende2/internal/config"
	"github.com/bjarke-xyz/rasende2/internal/core"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
	"github.com/gin-gonic/gin"
)

//go:embed templates/*.html
var templateFS embed.FS

// devTemplateDir is read instead of templateFS outside production, so template
// edits show up on refresh without rebuilding. It is relative to the working
// directory, which `make dev` leaves at the repository root.
const devTemplateDir = "internal/web/templates"

// Renderer renders the templates in templates/ as one namespace: every
// {{define}} across every file must have a unique name.
//
// A page is rendered in two passes. The page template runs first, into a
// buffer, and the result is handed to "layout" as pre-rendered Content. This
// gives the layout a content slot without needing a dynamic {{template}} name,
// which html/template does not support.
type Renderer struct {
	mu      sync.RWMutex
	tmpl    *template.Template
	fsys    fs.FS
	pattern string
	reload  bool
}

// layoutData is what "layout" executes against: the base model's fields,
// promoted, plus the already-rendered page body.
type layoutData struct {
	components.BaseViewModel
	Content template.HTML
}

type headerLinkModel struct {
	Path    string
	Text    string
	Current bool
}

func NewRenderer(cfg *config.Config) (*Renderer, error) {
	// Both filesystems yield the same template names, since a template parsed
	// from a file is named after its base name.
	r := &Renderer{fsys: templateFS, pattern: "templates/*.html"}
	if cfg.AppEnv != config.AppEnvProduction {
		if _, err := os.Stat(devTemplateDir); err == nil {
			r.fsys, r.pattern, r.reload = os.DirFS(devTemplateDir), "*.html", true
		} else {
			log.Printf("templates: %v not found, serving embedded copies", devTemplateDir)
		}
	}
	tmpl, err := r.parse()
	if err != nil {
		return nil, err
	}
	r.tmpl = tmpl
	return r, nil
}

func (r *Renderer) parse() (*template.Template, error) {
	return template.New("").Funcs(templateFuncs).ParseFS(r.fsys, r.pattern)
}

// templates returns the set to render with, re-reading from disk in dev.
func (r *Renderer) templates() (*template.Template, error) {
	if !r.reload {
		r.mu.RLock()
		defer r.mu.RUnlock()
		return r.tmpl, nil
	}
	tmpl, err := r.parse()
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.tmpl = tmpl
	r.mu.Unlock()
	return tmpl, nil
}

// execute renders name into a buffer. Rendering to a buffer rather than
// straight to the response means a mid-render failure cannot leave a truncated
// page behind an already-sent 200.
func (r *Renderer) execute(name string, data any) ([]byte, error) {
	tmpl, err := r.templates()
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return nil, fmt.Errorf("rendering %q: %w", name, err)
	}
	return buf.Bytes(), nil
}

// String renders a template to a string, for embedding in an SSE event.
func (r *Renderer) String(name string, data any) string {
	b, err := r.execute(name, data)
	if err != nil {
		log.Printf("render: %v", err)
		return ""
	}
	return string(b)
}

func (r *Renderer) write(c *gin.Context, status int, body []byte) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.Status(status)
	if _, err := c.Writer.Write(body); err != nil {
		log.Printf("render: writing response: %v", err)
	}
}

// Page renders a page template, wrapped in the layout unless the request came
// from htmx and only wants the fragment.
func (r *Renderer) Page(c *gin.Context, status int, name string, base components.BaseViewModel, data any) {
	body, err := r.execute(name, data)
	if err != nil {
		r.fail(c, err)
		return
	}
	if !base.IncludeLayout {
		r.write(c, status, body)
		return
	}
	body, err = r.execute("layout", layoutData{BaseViewModel: base, Content: template.HTML(body)})
	if err != nil {
		r.fail(c, err)
		return
	}
	r.write(c, status, body)
}

// Partial renders a single template, never wrapped in the layout.
func (r *Renderer) Partial(c *gin.Context, status int, name string, data any) {
	body, err := r.execute(name, data)
	if err != nil {
		r.fail(c, err)
		return
	}
	r.write(c, status, body)
}

// fail is the last resort when a template itself is broken; rendering the error
// page would likely fail the same way.
func (r *Renderer) fail(c *gin.Context, err error) {
	log.Printf("render: %v", err)
	c.String(http.StatusInternalServerError, "template error")
}

var templateFuncs = template.FuncMap{
	"queryEscape": url.QueryEscape,
	"lower":       strings.ToLower,
	"rfc3339":     func(t time.Time) string { return t.Format(time.RFC3339) },
	"timeAgo":     getTimeDifference,
	"truncate":    truncateText,
	"paragraphs":  func(s string) []string { return strings.Split(s, "\n") },

	// danishAgo renders a duration the way the index page's "seneste raseri"
	// timestamp does: "for 3 timer siden".
	"danishAgo": func(t time.Time) string {
		return config.DanishTimeagoConfig.FormatRelativeDuration(time.Since(t))
	},

	"placeholderImg": func() string { return config.PlaceholderImgUrl },

	// headerLink and titlesSse build the arguments for the templates of the
	// same name, which take more than the single value {{template}} passes.
	"headerLink": func(currentPath, linkPath, text string) headerLinkModel {
		return headerLinkModel{Path: linkPath, Text: text, Current: currentPath == linkPath}
	},

	"titlesSse": func(siteId int, cursor string, placeholder bool) components.TitlesSseModel {
		return components.TitlesSseModel{SiteId: siteId, Cursor: cursor, Placeholder: placeholder}
	},

	"orDefault": func(s *string, fallback string) string {
		if s == nil || *s == "" {
			return fallback
		}
		return *s
	},

	"articleUrl": func(fn core.FakeNewsDto) string {
		return "/fake-news/" + url.QueryEscape(fn.Slug())
	},

	"articleGeneratorUrl": func(siteId int, title string) string {
		return fmt.Sprintf("/article-generator?siteId=%v&title=%v", siteId, url.QueryEscape(title))
	},

	// jsonAttr serialises a value for an HTML attribute. html/template escapes
	// the quotes in the result, so it round-trips through JSON.parse in main.js.
	"jsonAttr": func(v any) (string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		return string(b), nil
	},
}
