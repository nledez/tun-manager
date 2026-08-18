BIN := bin/tun-manager
PREFIX ?= /usr/local
APPDIR ?= /Applications
APP_NAME := Tun Manager.app
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COVERAGE := coverage.out
# Fail the build below this, so coverage cannot quietly rot. Set just under the
# current figure: a real regression trips it, a rounding change does not.
COVERAGE_MIN := 99
# Coverage measures the program that ships. internal/tools holds build-time
# generators, which `make notices-check` exercises end to end on every run;
# counting them would only dilute the number this floor guards.
COVER_PKGS := $(shell go list ./... | grep -v '/internal/tools/')

.PHONY: all build test race cover cover-html vet lint fmt icon notices notices-check markers-check run install clean release release-check macos-build macos-test macos-app

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

# Installs both halves: the command line tool and the menu bar application.
#
# Run it as yourself, not with sudo. The two destinations have different owners
# — /Applications belongs to the admin group and needs no privileges, while
# /usr/local/bin belongs to root — so only that one step asks for a password.
# Running the whole thing as root would build as root too, and leave a tree full
# of files you can no longer write.
install: build macos-app
	@if [ "$$(id -u)" -eq 0 ]; then \
		echo "run this as yourself rather than with sudo: it would build as root"; \
		echo "and leave root-owned files in your working tree. It asks for a"; \
		echo "password by itself, for $(PREFIX)/bin alone."; \
		exit 1; \
	fi
	@# A running application holds its own copy open; replacing the bundle
	@# underneath it is how you get one that half works until the next login.
	@if pkill -x tun-manager-menubar 2>/dev/null; then echo "quit the running $(APP_NAME)"; fi
	rm -rf "$(APPDIR)/$(APP_NAME)"
	@# ditto rather than cp -R: it is the tool that understands bundles, and it
	@# carries the signature across intact.
	ditto "macos/build/$(APP_NAME)" "$(APPDIR)/$(APP_NAME)"
	codesign --verify --strict "$(APPDIR)/$(APP_NAME)"
	@if [ -w "$(PREFIX)/bin" ]; then \
		install -m 0755 $(BIN) "$(PREFIX)/bin/tun-manager"; \
	else \
		echo "$(PREFIX)/bin belongs to root, so this step asks for a password"; \
		sudo install -m 0755 $(BIN) "$(PREFIX)/bin/tun-manager"; \
	fi
	@echo "installed $(PREFIX)/bin/tun-manager and $(APPDIR)/$(APP_NAME)"

# The notification icon is carried in the binary, so an installed tun-manager
# has one without an install step. Regenerate it after changing the artwork.
#
# Derived from assets/icon.png rather than the banner: the banner carries the
# name and a tagline, which are unreadable at 256 pixels and absent noise at 16.
icon:
	sips -Z 256 assets/icon.png --out internal/notify/icon.png >/dev/null
	@ls -l internal/notify/icon.png

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

# The menu bar client. Deliberately absent from `all`: the Go gate must not
# start requiring Xcode, and a Swift regression must not be able to block a Go
# release. Its suite and its coverage are a separate contract.
macos-build:
	$(MAKE) -C macos build

macos-test:
	$(MAKE) -C macos test

macos-app:
	$(MAKE) -C macos app

clean:
	rm -rf bin dist $(COVERAGE)
	$(MAKE) -C macos clean
