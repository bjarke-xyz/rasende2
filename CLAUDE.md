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
  - Local SQLite database file, accessed with `jmoiron/sqlx` over the pure-Go `modernc.org/sqlite` driver
  - Uses `sqlc` for type-safe SQL query generation
  - SQLite FTS5 (`rss_items_fts`) for full-text search

- **internal/web/** - HTTP handlers and templates
  - Gin web framework for routing
  - `a-h/templ` for HTML templating (generates `*_templ.go` files from `*.templ` files)
  - HTMX for dynamic UI updates
  - Components in `internal/web/components/`

- **internal/api/** - API endpoints (separate from web handlers)

### Key Technologies

- **Backend**: Go 1.23, Gin web framework
- **Database**: local SQLite file (`modernc.org/sqlite`, cgo-free), with sqlc for queries
- **Search**: SQLite FTS5 + a Danish analyzer in `internal/search`
- **Templates**: templ (type-safe HTML templates)
- **Frontend**: HTMX, TailwindCSS, Chart.js
- **AI**: OpenRouter API for fake news generation (uses OpenAI-compatible interface)
- **Metrics**: Prometheus client for metrics collection (exposed on :9091/metrics)

### Database Management

- SQL migrations in `internal/repository/db/migrations/`
- SQL queries in `internal/repository/db/queries/`
- Generated DAO code in `internal/repository/db/dao/` (via sqlc)
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
# Full production build (generates templates, builds CSS, compiles binary)
make build

# Development mode with hot reload
make dev

# Run code generation only (templ, sqlc, tailwind)
make generate

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

### Code Generation

When modifying database schemas or queries:

1. Update SQL files in `internal/repository/db/migrations/` or `internal/repository/db/queries/`
2. Run `sqlc generate` (included in `make generate`)

When modifying templates:

1. Edit `*.templ` files in `internal/web/components/`
2. Run `templ generate` (included in `make generate`)
3. Generated Go code will appear in `*_templ.go` files

When modifying TailwindCSS:

1. Edit `internal/web/static/css/style.css` or add Tailwind classes in `*.templ` files
2. Run `npx tailwindcss build -i internal/web/static/css/style.css -o internal/web/static/css/tailwind.css -m`

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
4. Initializes Gin router with CORS, sessions, and custom templ renderer
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

Templates use `a-h/templ` for type-safe rendering. The custom renderer in `internal/web/renderer/gin_templ_renderer.go` integrates templ with Gin.

## Testing Notes

- Tests use table-driven test patterns
- Database tests may use in-memory SQLite
- Mock implementations of interfaces are used for unit tests
