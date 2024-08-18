.PHONY: build

BINARY_NAME=rasende2

# build builds the tailwind css sheet, and compiles the binary into a usable thing.
build:
	npm ci && \
	cp node_modules/htmx.org/dist/htmx.min.js web/static/js/vendor && \
   	templ generate && \
	go mod tidy && \
	go generate && \
	go build -ldflags="-w -s" -o ${BINARY_NAME}

# dev runs the development server where it builds the tailwind css sheet,
# and compiles the project whenever a file is changed.
dev:
	templ generate --watch --cmd="go generate" &\
	templ generate --watch --cmd="go run ."


clean:
	go clean
	rm web/static/js/vendor/*.js