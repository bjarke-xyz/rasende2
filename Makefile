.PHONY: build npm-build-prod npm-build-dev generate

BINARY_NAME=rasende2

npm-ci:
	npm ci

npm-build-prod:
	cp node_modules/htmx.org/dist/htmx.min.js internal/web/static/js/vendor && \
	cp node_modules/htmx-ext-sse/sse.js internal/web/static/js/vendor && \
	cp node_modules/chart.js/dist/chart.umd.js internal/web/static/js/vendor

npm-build-dev: npm-build-prod
	cp node_modules/chart.js/dist/chart.umd.js.map internal/web/static/js/vendor

# build builds the tailwind css sheet, and compiles the binary into a usable thing.
build: npm-ci npm-build-prod generate
	go mod tidy && \
	go build -ldflags="-w -s" -o ${BINARY_NAME} cmd/web/main.go

# dev runs the development server where it builds the tailwind css sheet,
# and compiles the project whenever a file is changed.
dev: npm-build-dev generate
	templ generate --watch --cmd="go run cmd/web/main.go"

generate:
	templ generate
	sqlc generate
	npx tailwindcss build -i internal/web/static/css/style.css -o internal/web/static/css/tailwind.css -m

clean:
	go clean
	rm -f internal/web/static/css/tailwind.css
	rm -f internal/web/static/js/vendor/*.js
	rm -f internal/web/static/js/vendor/*.js.map
	rm -f ${BINARY_NAME}
	rm -rf cache/*
	touch cache/.gitkeep

duda:
	go run cmd/duda/main.go