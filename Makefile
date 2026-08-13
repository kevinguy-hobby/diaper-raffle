BINARY  := raffle
PKG     := ./cmd/server
DB      ?= diaper-raffle.db
ADDR    ?= :8080

.PHONY: run dev party status set-password open build test check fmt vet tidy clean backup

## run: build and start the server
run:
	go run $(PKG) -addr $(ADDR) -db $(DB)

## party: start the server and a Cloudflare tunnel, and print the public URL
party:
	@./scripts/party.sh $(HOST)

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

## set-password: choose the shared password (prompts, never echoes)
set-password: build
	@./scripts/set-password.sh

## open: remove the password, making the site public again
open: build
	@./scripts/set-password.sh --clear

## status: check every hop from the app to the public URL
status:
	@./scripts/status.sh $(HOST)

## backup: copy the database safely while the server is running
backup:
	sqlite3 $(DB) ".backup '$(DB).bak'"
	@echo "wrote $(DB).bak"

clean:
	rm -f $(BINARY)
