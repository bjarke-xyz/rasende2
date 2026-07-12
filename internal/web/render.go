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
	"github.com/bjarke-xyz/rasende2/internal/httpx"
	"github.com/bjarke-xyz/rasende2/internal/lang"
	"github.com/bjarke-xyz/rasende2/internal/web/components"
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
//
// The templates are parsed once per language, because {{t "key"}} has to know
// which edition it is rendering and a FuncMap is bound at parse time. Passing
// the language through the data instead would mean putting it on every view
// model — including the ones the deep partials receive, which are bare domain
// values (an RssSearchResult, a site name) with nowhere to put it. So the
// language is closed over by the FuncMap instead, and the templates stay clean.
// The cost is one extra parse of 17KB at boot, and nothing per request.
type Renderer struct {
	mu      sync.RWMutex
	tmpls   map[lang.Code]*template.Template
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
	tmpls, err := r.parse()
	if err != nil {
		return nil, err
	}
	r.tmpls = tmpls
	return r, nil
}

// parse builds one template set per edition. A parse error in any of them is a
// startup failure, as it was before: the sets differ only in their FuncMap, so
// in practice they all break together.
func (r *Renderer) parse() (map[lang.Code]*template.Template, error) {
	tmpls := make(map[lang.Code]*template.Template, len(lang.All))
	for _, l := range lang.All {
		tmpl, err := template.New("").Funcs(templateFuncs(l)).ParseFS(r.fsys, r.pattern)
		if err != nil {
			return nil, err
		}
		tmpls[l.Code] = tmpl
	}
	return tmpls, nil
}

// templates returns the set to render l with, re-reading from disk in dev.
func (r *Renderer) templates(l lang.Lang) (*template.Template, error) {
	if r.reload {
		tmpls, err := r.parse()
		if err != nil {
			return nil, err
		}
		r.mu.Lock()
		r.tmpls = tmpls
		r.mu.Unlock()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	tmpl, ok := r.tmpls[l.Code]
	if !ok {
		return nil, fmt.Errorf("no template set for language %q", l.Code)
	}
	return tmpl, nil
}

// execute renders name into a buffer. Rendering to a buffer rather than
// straight to the response means a mid-render failure cannot leave a truncated
// page behind an already-sent 200.
func (r *Renderer) execute(l lang.Lang, name string, data any) ([]byte, error) {
	tmpl, err := r.templates(l)
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
func (r *Renderer) String(req *http.Request, name string, data any) string {
	b, err := r.execute(LangOf(req), name, data)
	if err != nil {
		log.Printf("render: %v", err)
		return ""
	}
	return string(b)
}

func (r *Renderer) write(w http.ResponseWriter, status int, body []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if _, err := w.Write(body); err != nil {
		log.Printf("render: writing response: %v", err)
	}
}

// Page renders a page template, wrapped in the layout unless the request came
// from htmx and only wants the fragment.
func (r *Renderer) Page(w http.ResponseWriter, req *http.Request, status int, name string, base components.BaseViewModel, data any) {
	l := LangOf(req)
	body, err := r.execute(l, name, data)
	if err != nil {
		r.fail(w, err)
		return
	}
	if !base.IncludeLayout {
		r.write(w, status, body)
		return
	}
	body, err = r.execute(l, "layout", layoutData{BaseViewModel: base, Content: template.HTML(body)})
	if err != nil {
		r.fail(w, err)
		return
	}
	r.write(w, status, body)
}

// Partial renders a single template, never wrapped in the layout.
func (r *Renderer) Partial(w http.ResponseWriter, req *http.Request, status int, name string, data any) {
	body, err := r.execute(LangOf(req), name, data)
	if err != nil {
		r.fail(w, err)
		return
	}
	r.write(w, status, body)
}

// fail is the last resort when a template itself is broken; rendering the error
// page would likely fail the same way.
func (r *Renderer) fail(w http.ResponseWriter, err error) {
	log.Printf("render: %v", err)
	httpx.String(w, http.StatusInternalServerError, "template error")
}

// templateFuncs is the FuncMap for one edition. Only "t", "ago" and
// "defaultQuery" actually close over l; the rest are the same in every set.
//
// There is deliberately no URL helper. The layout carries <base href="/da/">,
// so every relative path in a template — including the ones with query strings,
// which a helper would turn into a printf — resolves under the right edition on
// its own. See layout.html.
func templateFuncs(l lang.Lang) template.FuncMap {
	return template.FuncMap{
		"queryEscape": url.QueryEscape,
		"lower":       strings.ToLower,
		"rfc3339":     func(t time.Time) string { return t.Format(time.RFC3339) },
		"timeAgo":     getTimeDifference,
		"truncate":    truncateText,
		"paragraphs":  func(s string) []string { return strings.Split(s, "\n") },

		// t looks up a message in this edition's catalog. A key missing from the
		// catalog renders as the key itself; TestCatalogsCoverTemplates is what
		// stops that reaching production.
		"t": l.T,

		// defaultQuery is the edition's cliché — "rasende", "outrage" — which the
		// search box is prefilled with.
		"defaultQuery": func() string { return l.DefaultQuery },

		// ago renders a duration the way the index page's "seneste raseri"
		// timestamp does: "for 3 timer siden".
		"ago": func(t time.Time) string {
			return l.TimeAgo.FormatRelativeDuration(time.Since(t))
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

		// Relative, so <base> puts them in the current edition.
		"articleUrl": func(fn core.FakeNewsDto) string {
			return "fake-news/" + url.QueryEscape(fn.Slug())
		},

		"articleGeneratorUrl": func(siteId int, title string) string {
			return fmt.Sprintf("article-generator?siteId=%v&title=%v", siteId, url.QueryEscape(title))
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
}
