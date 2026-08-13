BINARY  := raffle
PKG     := ./cmd/server
DB      ?= diaper-raffle.db
ADDR    ?= :8080

.PHONY: run dev build test check fmt vet tidy clean backup

## run: build and start the server
run:
	go run $(PKG) -addr $(ADDR) -db $(DB)

## dev: serve assets from disk, so CSS and JS edits need only a refresh
dev:
	go run $(PKG) -addr $(ADDR) -db $(DB) -dev -verbose

## build: one self-contained binary, assets and all
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINARY) $(PKG)

## test: the whole suite
test:
	go test ./...

## check: what should pass before committing
check: fmt vet
	go test -race ./...

fmt:
	gofmt -l -w .

vet:
	go vet ./...

tidy:
	go mod tidy

## backup: copy the database safely while the server is running
backup:
	sqlite3 $(DB) ".backup '$(DB).bak'"
	@echo "wrote $(DB).bak"

clean:
	rm -f $(BINARY)
