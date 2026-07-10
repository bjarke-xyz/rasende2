# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Rasende2 is a Danish news aggregator that scrapes RSS feeds from various news sites, searches for titles containing the word "rasende" (angry/furious), and displays them. The application also includes a "fake news" generator feature that uses OpenRouter to create satirical news articles.

## Core Architecture

### Application Structure

The application follows a layered architecture:

- **cmd/** - Entry points for executables
  - `cmd/web/main.go` - Main web server application
  - `cmd/duda/main.go` - RSS feed discovery tool for finding potential news sites

- **internal/core/** - Core domain interfaces and types
  - Defines `NewsService` and `NewsRepository` interfaces
  - Contains domain models: `NewsSite`, `RssItemDto`, `FakeNewsDto`, `RssSearchResult`
  - `AppContext` is the main dependency injection container with `Config`, `Infra` (infrastructure), and `Deps` (dependencies)

- **internal/news/** - Business logic implementation
  - `service.go` implements `NewsService` interface
  - Handles RSS fetching, caching, search indexing
  - Manages fake news generation and voting

- **internal/repository/** - Data persistence layer
  - Local SQLite database file, accessed with plain `database/sql` over the pure-Go `modernc.org/sqlite` driver
  - SQLite FTS5 (`rss_items_fts`) for full-text search

- **internal/web/** - HTTP handlers and templates
  - Gin web framework for routing
  - `html/template` files in `internal/web/templates/`, embedded with `go:embed`
  - HTMX for dynamic UI updates
  - View models in `internal/web/components/models.go`

- **internal/api/** - API endpoints (separate from web handlers)

### Key Technologies

- **Backend**: Go 1.23, Gin web framework
- **Database**: local SQLite file (`modernc.org/sqlite`, cgo-free), queried with plain `database/sql`
- **Search**: SQLite FTS5 + a Danish analyzer in `internal/search`
- **Templates**: standard-library `html/template`
- **Frontend**: HTMX, hand-written CSS, Chart.js
- **AI**: OpenRouter API for fake news generation (uses OpenAI-compatible interface)
- **Metrics**: Prometheus client for metrics collection (exposed on :9091/metrics)

### Database Management

- SQL migrations in `internal/repository/db/migrations/` (goose, embedded, run at startup)
- Queries are hand-written: `internal/repository/sqlite_news.go` for news and fake news,
  `internal/repository/db/users.go` for the magic-link login
- Database file path configured via the `DB_CONN_STR` environment variable

### Search Implementation

Search is SQLite FTS5, in the same file as the data. SQLite ships no Danish stemmer, so the
linguistics live in Go: `internal/search` runs UAX#29 segmentation, lowercasing, Danish stop-word
removal and the Snowball Danish stemmer. The **same** pipeline stems text on the way into
`rss_items_fts` and stems the query before it is matched, which is what makes a search for `raser`
find a headline containing `rasende`. FTS5's own `unicode61` tokenizer therefore only ever splits an
already-analyzed token stream on whitespace.

This replaced bleve, whose `da` analyzer the Go pipeline reproduces token-for-token. Before that the
app used PostgreSQL's `to_tsvector('danish', ...)`. MySQL's FULLTEXT index was tested but did not
find verb variations of "rasende".

Consequences worth knowing:

- `InsertItems` writes the index row in the **same transaction** as the `rss_items` row, so the index
  cannot drift. There is no reconciliation job.
- The stemmed tokens on disk are only meaningful relative to the analyzer that wrote them. After
  changing `internal/search`, rebuild via `POST /api/admin/rebuild-index` (`Authorization: $JOB_KEY`).
- `rss_items.id` is an explicit `INTEGER PRIMARY KEY` because `rss_items_fts` is joined on it by
  rowid, and `VACUUM` is permitted to renumber *implicit* rowids.
- A query that stems to zero tokens (only stop words) must return no results rather than reach FTS5
  as an empty `MATCH`, which is a syntax error.
- FTS5 auxiliary functions such as `bm25()` reject a table alias, so `rss_items_fts` is never aliased.

## Common Development Commands

### Build and Run

```bash
# Vendor the frontend JS and compile the binary
make build

# Development server; templates reload from disk on refresh
make dev

# Run tests
make test

# Clean build artifacts
make clean
```

### Individual Build Steps

```bash
# Install npm dependencies
make npm-ci

# Build vendor JavaScript files (htmx, chart.js)
make npm-build-prod

# Run the duda RSS discovery tool
make duda
# or
go run cmd/duda/main.go
```

### Running Tests

```bash
# Run all tests
go test ./...

# Run specific package tests
go test ./internal/core
go test ./internal/repository
```

### Changing database schemas

1. Add a goose migration in `internal/repository/db/migrations/`
2. Update the matching hand-written query and its `Scan` call. Column-list constants live
   next to the scan helpers at the top of `internal/repository/sqlite_news.go`; the scan
   order must match the column order.

### Changing templates or CSS

Neither has a build step. Edit `internal/web/templates/*.html` or
`internal/web/static/css/style.css` and refresh; outside `APP_ENV=production` the renderer
re-reads templates from disk on every request. Add a case to `TestTemplatesExecute` in
`internal/web/render_test.go` for any new template or conditional branch — `html/template`
resolves names and fields at execute time, so an untested branch fails in production, not
at build.

## Configuration

Configuration is loaded from environment variables (via `.env` file or system environment).

Key environment variables:
- `PORT` - Server port (default: 8080)
- `DB_CONN_STR` - Path to the local SQLite database file
- `LLM_API_KEY` - OpenRouter API key for fake news generation
- `APP_ENV` - `development` or `production`
- `COOKIE_SECRET` - Secret for session cookies
- `JOB_KEY` - Authentication key for background job endpoints

See `.env.example` for all configuration options.

## Application Initialization Flow

1. `cmd/web/main.go` loads config from environment
2. Opens database connection and runs migrations
3. Creates `AppContext` with config, infrastructure, and dependencies
4. Initializes Gin router with CORS and sessions, and parses the templates
5. Registers API and web routes
6. Starts metrics server on :9091
7. Starts main HTTP server with graceful shutdown

## Important Patterns

### Dependency Injection

The `AppContext` struct is passed throughout the application:

```go
type AppContext struct {
    Config *config.Config
    Infra  *AppInfra      // Cache, Mail
    Deps   *AppDeps       // NewsService, AiClient
}
```

### Repository Pattern

All database operations go through the `NewsRepository` interface, implemented by `internal/repository/sqlite_news.go`.

### Service Layer

Business logic is in `NewsService` interface, implemented by `internal/news/service.go`. This handles:
- RSS feed fetching and parsing
- Search indexing
- Caching (using `AppContext.Infra.Cache`)
- Fake news generation

Database backups are **not** handled by this app. The `sqlite-backer-upper` container in the infra
repo snapshots `/opt/data/rasende2/db.db` to Cloudflare R2 on a cron schedule.

### Template Rendering

`internal/web/render.go` parses every file in `internal/web/templates/` into **one** template
set, so each `{{define}}` name must be unique across all files. Handlers call one of:

- `renderer.Page(c, status, name, base, data)` — renders the page into a buffer, then hands
  it to `"layout"` as pre-rendered `Content`. This is how the layout gets a content slot:
  `{{template}}` needs a constant name, so the layout cannot dispatch on the page. When
  `base.IncludeLayout` is false (the request came from htmx) the page body is served bare.
- `renderer.Partial(c, status, name, data)` — one template, never wrapped.
- `renderer.String(name, data)` — renders to a string for `c.SSEvent`.

Everything renders through a buffer first, so a template error cannot emit a half-written
page under an already-sent 200.

Danish formatting, URL building and the `hasVoted`-style predicates live in the `FuncMap` at
the bottom of `render.go`, or as methods on the view models in `components/models.go`.
Prefer a method: it is type-checked, a `FuncMap` entry is not.

## Testing Notes

- Tests use table-driven test patterns
- Database tests may use in-memory SQLite
- Mock implementations of interfaces are used for unit tests
