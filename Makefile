.PHONY: build npm-ci npm-build-prod npm-build-dev dev test clean duda

BINARY_NAME=rasende2

npm-ci:
	npm ci

# npm's only remaining job is vendoring the three frontend libraries.
npm-build-prod:
	cp node_modules/htmx.org/dist/htmx.min.js internal/web/static/js/vendor && \
	cp node_modules/htmx-ext-sse/sse.js internal/web/static/js/vendor && \
	cp node_modules/chart.js/dist/chart.umd.js internal/web/static/js/vendor

npm-build-dev: npm-build-prod
	cp node_modules/chart.js/dist/chart.umd.js.map internal/web/static/js/vendor

# build compiles the binary. Templates and CSS are embedded, not generated.
build: npm-ci npm-build-prod
	go mod tidy && \
	go build -ldflags="-w -s" -o ${BINARY_NAME} cmd/web/main.go

# dev runs the development server. Outside APP_ENV=production the templates are
# re-read from disk on every request, so editing a .html file only needs a
# refresh. Go changes still need a restart.
dev: npm-build-dev
	go run cmd/web/main.go

test:
	go test ./...

clean:
	go clean
	rm -f internal/web/static/js/vendor/*.js
	rm -f internal/web/static/js/vendor/*.js.map
	rm -f ${BINARY_NAME}
	rm -rf cache/*
	touch cache/.gitkeep

duda:
	go run cmd/duda/main.go
