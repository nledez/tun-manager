BIN := bin/tun-manager
PREFIX ?= /usr/local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COVERAGE := coverage.out
# Fail the build below this, so coverage cannot quietly rot.
COVERAGE_MIN := 85

.PHONY: all build test race cover cover-html vet lint fmt run install clean release-check

all: vet test build

build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN) .

test:
	go test ./...

race:
	go test -race ./...

cover:
	go test ./... -coverprofile=$(COVERAGE) -covermode=atomic
	@go tool cover -func=$(COVERAGE) | tail -1
	@total=$$(go tool cover -func=$(COVERAGE) | tail -1 | grep -oE '[0-9]+\.[0-9]+'); \
	if [ $${total%%.*} -lt $(COVERAGE_MIN) ]; then \
		echo "coverage $$total% is below the $(COVERAGE_MIN)% floor"; exit 1; \
	fi

# Per-statement report in the browser, to find what a number cannot show.
cover-html: cover
	go tool cover -html=$(COVERAGE)

vet:
	go vet ./...

fmt:
	gofmt -l -w .

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run

# The TUI needs root: the WireGuard control sockets are root-only.
run: build
	sudo $(BIN)

install: build
	install -m 0755 $(BIN) $(PREFIX)/bin/tun-manager

# Runs the release pipeline without publishing anything.
release-check:
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not installed: brew install goreleaser"; exit 1; }
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist $(COVERAGE)
