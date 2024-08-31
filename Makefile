.PHONY: build npm-build-prod npm-build-dev generate

BINARY_NAME=rasende2

npm-ci:
	npm ci

npm-build-prod:
	cp node_modules/htmx.org/dist/htmx.min.js web/static/js/vendor && \
	cp node_modules/htmx-ext-sse/sse.js web/static/js/vendor && \
	cp node_modules/chart.js/dist/chart.umd.js web/static/js/vendor

npm-build-dev: npm-build-prod
	cp node_modules/chart.js/dist/chart.umd.js.map web/static/js/vendor

# build builds the tailwind css sheet, and compiles the binary into a usable thing.
build: npm-ci npm-build-prod
	go mod tidy && \
	go generate && \
	go build -ldflags="-w -s" -o ${BINARY_NAME}

# dev runs the development server where it builds the tailwind css sheet,
# and compiles the project whenever a file is changed.
dev: npm-build-dev generate
	templ generate --watch --cmd="go generate" &\
	templ generate --watch --cmd="go run ."

generate:
	go generate
	templ generate
	sqlc generate

clean:
	go clean
	rm ${BINARY_NAME}
	rm web/static/js/vendor/*.js
	rm web/static/js/vendor/*.js.map