BIN := bin/tun-manager
PREFIX ?= /usr/local
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COVERAGE := coverage.out
# Fail the build below this, so coverage cannot quietly rot. Set just under the
# current figure: a real regression trips it, a rounding change does not.
COVERAGE_MIN := 99
# Coverage measures the program that ships. internal/tools holds build-time
# generators, which `make notices-check` exercises end to end on every run;
# counting them would only dilute the number this floor guards.
COVER_PKGS := $(shell go list ./... | grep -v '/internal/tools/')

.PHONY: all build test race cover cover-html vet lint fmt notices notices-check markers-check run install clean release release-check

all: vet lint test notices-check markers-check build

# -trimpath keeps local paths out of the binary, so the same source at the same
# version produces the same bytes on any machine.
build:
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(BIN) .

test:
	go test ./...

race:
	go test -race -count=1 ./...

cover:
	go test $(COVER_PKGS) -coverprofile=$(COVERAGE) -covermode=atomic
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

# The licenses of every module linked into the binary. BSD-3-Clause clause 2
# and the equivalent clauses of the dependencies require shipping them.
notices:
	go run ./internal/tools/notices -o THIRD-PARTY-NOTICES.txt ./...

# Fails when a dependency change was not reflected in the notices file.
notices-check: notices
	git diff --exit-code -- THIRD-PARTY-NOTICES.txt

# Cuts a release. Pushing the tag is what publishes: CI re-runs the suite on the
# tagged commit and only then builds the archives and creates the release.
#
#   make release VERSION=0.1.0
#   make release VERSION=0.1.0 DRY_RUN=1   # every check, no tag
release:
	@scripts/release.sh $(VERSION)

# Every deliberate omission carries a NOT TESTED: marker naming the section of
# docs/coverage-gaps.md that argues for it. A marker whose reasoning lives only
# in a commit message is an excuse rather than a decision, so check the section
# exists.
markers-check:
	@grep -rho 'docs/coverage-gaps.md, "[^"]*"' --include='*.go' . \
		| sed 's/.*"\(.*\)"/\1/' | sort -u | while read -r section; do \
		grep -qF "### $$section" docs/coverage-gaps.md \
			|| { echo "no \"$$section\" section in docs/coverage-gaps.md"; exit 1; }; \
	done
	@echo "every NOT TESTED marker is documented"

# Runs the release pipeline without publishing anything.
release-check:
	@command -v goreleaser >/dev/null 2>&1 || { echo "goreleaser not installed: brew install goreleaser"; exit 1; }
	goreleaser release --snapshot --clean

clean:
	rm -rf bin dist $(COVERAGE)
