# npm's only job is vendoring htmx and chart.js into static/js/vendor, which is
# then go:embed'ed into the binary. Its own stage keeps node out of the Go
# builder and off the cache path of every Go-only change.
# https://hub.docker.com/_/node
FROM node:22-alpine AS vendor

RUN apk add --no-cache make

WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

# Via make, so the list of vendored files lives in exactly one place.
COPY Makefile ./
RUN mkdir -p internal/web/static/js/vendor && make npm-build-prod

# Use the offical golang image to create a binary.
# This is based on Debian and sets the GOPATH to /go.
# https://hub.docker.com/_/golang
FROM golang:1.26-bookworm AS builder

# Create and change to the app directory.
WORKDIR /app

# Retrieve application dependencies.
# This allows the container build to reuse cached dependencies.
# Expecting to copy go.mod and if present go.sum.
COPY go.* ./
RUN go mod download

# Copy local code to the container image.
COPY . ./
COPY --from=vendor /app/internal/web/static/js/vendor internal/web/static/js/vendor

# modernc.org/sqlite is pure Go, so CGO can stay off — which lets the runtime
# image be a near-empty base with no libc. Templates, static assets and
# migrations are all go:embed'ed, so the binary is the whole app.
RUN CGO_ENABLED=0 make go-build

# Distroless static: ~2MB, ships ca-certificates and tzdata, and nothing else —
# no shell, no package manager. Suits a static CGO-free binary.
FROM gcr.io/distroless/static-debian12

# Copy the binary to the production image from the builder stage.
COPY --from=builder /app/rasende2 /app/rasende2

# Run the web service on container startup.
CMD ["/app/rasende2"]
