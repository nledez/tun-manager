# The menu bar client. Kept out of the repository's root `make all` on purpose:
# the Go gate must not start requiring Xcode, and a Swift regression must not be
# able to block a Go release.

ARTWORK := ../assets/icon.png
SMALL_ARTWORK := ../assets/icon-small.png

# Two flavours from one source tree. `make app` builds the one that gets
# installed; `make app FLAVOUR=dev` builds one that cannot disturb it.
#
# The identifier is what does the work: macOS keys the notification permission,
# the remembered menu bar position and the defaults domain on it, so two builds
# sharing one would share all three — including a FeedSocket key pointed at a
# stand-in publisher.
FLAVOUR ?= release

ifeq ($(FLAVOUR),dev)
BUNDLE_ID := net.ledez.tun-manager.menubar.dev
APP := build/Tun Manager (dev).app
DISPLAY_NAME := Tun Manager (dev)
APP_ICON := build/AppIconDev.icns
# Radians. Enough to turn the green and blue into magenta, which is plainly the
# same artwork and plainly not the installed one.
DEV_HUE := 2.1
else
BUNDLE_ID := net.ledez.tun-manager.menubar
APP := build/Tun Manager.app
DISPLAY_NAME := Tun Manager
APP_ICON := build/AppIcon.icns
endif

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Launch Services accepts one to three dot-separated integers and nothing else,
# so v0.2.0-3-gdeadbee-dirty has to be trimmed down to 0.2.0.
SHORT_VERSION := $(shell printf '%s' "$(VERSION)" | sed -E 's/^v//; s/-.*$$//; s/^[^0-9].*$$/0.0.0/')
BUILD_NUMBER := $(shell git rev-list --count HEAD 2>/dev/null || echo 0)

# Detected rather than configured, best first: a Developer ID is the only thing
# that lets the application travel, and an Apple Development certificate is
# enough for it to work properly on this machine — which matters, because
# notifications need a real signature and are unreliable under an ad-hoc one.
# A fresh clone with an empty keychain still builds, ad-hoc. Override to pin:
#   make app SIGN_IDENTITY="Apple Development: you (TEAMID)"
SIGN_IDENTITY ?= $(shell security find-identity -v -p codesigning 2>/dev/null \
	| sed -n 's/.*"\(Developer ID Application: [^"]*\)".*/\1/p' | head -1)
ifeq ($(strip $(SIGN_IDENTITY)),)
SIGN_IDENTITY := $(shell security find-identity -v -p codesigning 2>/dev/null \
	| sed -n 's/.*"\(Apple Development: [^"]*\)".*/\1/p' | head -1)
endif
ifeq ($(strip $(SIGN_IDENTITY)),)
SIGN_IDENTITY := -
endif

# Empty for a local build: it only has to run on the machine that made it, and
# asking for the second architecture doubles the link for nothing. `release`
# sets both, because macOS 26 still runs on Intel and a published application
# that skips them is a download that does nothing on somebody's Mac.
ARCHS ?=
ARCH_FLAGS := $(foreach a,$(ARCHS),--arch $(a))

# The keychain item holding the notarisation credentials. Created once with:
#   xcrun notarytool store-credentials tun-manager \
#     --apple-id <your-apple-id> --team-id <TEAMID> --password <app-specific>
NOTARY_PROFILE ?= tun-manager
ZIP := build/TunManager.zip

# Named the way the archives GoReleaser publishes are named, so the two halves
# of a release read as one download list rather than as two conventions.
RELEASE_ZIP := build/tun-manager-menubar_$(SHORT_VERSION)_darwin_universal.zip

.PHONY: all build test app run icon sign notarize verify release publish clean

all: test app

build:
	swift build

test:
	swift test
	plutil -lint Resources/Info.plist

# Assembles the bundle. `app` is phony and has no file prerequisites: make
# breaks on prerequisites containing spaces, and "Tun Manager.app" has one.
app: $(APP_ICON)
	swift build -c release $(ARCH_FLAGS)
	rm -rf "$(APP)"
	mkdir -p "$(APP)/Contents/MacOS" "$(APP)/Contents/Resources"
	# Asked for rather than spelled out: the path moves the moment --arch is
	# added, and a hardcoded .build/release would package a stale binary.
	cp "$$(swift build -c release $(ARCH_FLAGS) --show-bin-path)/tun-manager-menubar" \
		"$(APP)/Contents/MacOS/tun-manager-menubar"
	cp "$(APP_ICON)" "$(APP)/Contents/Resources/AppIcon.icns"
	# Patched on the copy, so the committed plist stays a complete, lintable
	# file a reviewer can read rather than a template full of placeholders.
	cp Resources/Info.plist "$(APP)/Contents/Info.plist"
	plutil -replace CFBundleShortVersionString -string "$(SHORT_VERSION)" "$(APP)/Contents/Info.plist"
	plutil -replace CFBundleVersion -string "$(BUILD_NUMBER)" "$(APP)/Contents/Info.plist"
	plutil -replace NETLedezGitDescribe -string "$(VERSION)" "$(APP)/Contents/Info.plist"
	plutil -replace CFBundleIdentifier -string "$(BUNDLE_ID)" "$(APP)/Contents/Info.plist"
	plutil -replace CFBundleName -string "$(DISPLAY_NAME)" "$(APP)/Contents/Info.plist"
	plutil -replace CFBundleDisplayName -string "$(DISPLAY_NAME)" "$(APP)/Contents/Info.plist"
	$(MAKE) sign
	@echo "built $(APP) ($(SHORT_VERSION), $(VERSION))"

# --timestamp is not optional. A signature made without one stops validating the
# day its certificate expires, which for an Apple Development certificate is
# about a year: the application built today would refuse to launch next year
# with nothing to explain why.
#
# There is deliberately no `spctl` check here. Without a Developer ID it can
# never pass, and a permanently red gate is a gate somebody eventually deletes.
sign:
	codesign --force --options runtime --timestamp \
		--identifier "$(BUNDLE_ID)" --sign "$(SIGN_IDENTITY)" "$(APP)"
	codesign --verify --strict --verbose=2 "$(APP)"

run: app
	open "$(APP)"

# Notarises and staples, which is the only thing that lets the application run
# on somebody else's Mac. Everything before this point works without it; this
# is what removes the "damaged and can't be opened" dialog after the bundle has
# travelled through a zip, a download or AirDrop.
notarize: app
	@case "$(SIGN_IDENTITY)" in \
		"Developer ID Application:"*) ;; \
		*) echo "signed as '$(SIGN_IDENTITY)', which Gatekeeper will not accept."; \
		   echo "Create the certificate: Xcode > Settings > Accounts >"; \
		   echo "  your team > Manage Certificates > + > Developer ID Application"; \
		   exit 1;; \
	esac
	rm -f "$(ZIP)"
	# ditto, not zip: a plain zip loses the bundle's symlinks and extended
	# attributes, and the submission is rejected for a reason that does not
	# mention either.
	ditto -c -k --keepParent "$(APP)" "$(ZIP)"
	xcrun notarytool submit "$(ZIP)" --keychain-profile "$(NOTARY_PROFILE)" --wait
	# Stapled to the application rather than to the zip, so the ticket travels
	# inside the bundle and validates on a machine that is offline.
	xcrun stapler staple "$(APP)"
	rm -f "$(ZIP)"
	ditto -c -k --keepParent "$(APP)" "$(ZIP)"
	@$(MAKE) verify
	@echo "notarised: $(ZIP) can be given to somebody"

# The bundle as it is published: universal, notarised, stapled, and carrying the
# tag it was cut from.
#
# Deliberately not part of `make release` at the repository root. That gate must
# not start requiring Xcode, and a Swift failure must not be able to hold back a
# Go release — the two ship on the same tag, they do not share a build.
#
# Every check refuses rather than repairs, because the cost lands on whoever
# downloads the result: a bundle built off a tag carries a version number that
# matches no commit, and one built as the development flavour is a different
# application wearing the release's name.
release:
	@test -z "$$(git status --porcelain)" \
		|| { echo "the tree is dirty: the bundle would claim a version nobody can check out"; exit 1; }
	@git describe --exact-match --tags HEAD >/dev/null 2>&1 \
		|| { echo "HEAD carries no tag: cut the release from the repository root first"; exit 1; }
	@test "$(FLAVOUR)" = release \
		|| { echo "FLAVOUR=$(FLAVOUR) would publish the development build"; exit 1; }
	$(MAKE) notarize ARCHS="arm64 x86_64" ZIP="$(RELEASE_ZIP)"
	@lipo -archs "$(APP)/Contents/MacOS/tun-manager-menubar" | grep -qw x86_64 \
		|| { echo "the binary is not universal: Intel Macs would download nothing that runs"; exit 1; }
	@echo "release bundle: $(RELEASE_ZIP) ($(VERSION))"

# Attaches the bundle to the release the tag produced. Separate from `release`
# above because the two wait on different things: notarisation is minutes of
# Apple's time, and the release itself does not exist until CI is green. They
# finish in either order, and this is the half that needs both.
publish:
	@command -v gh >/dev/null 2>&1 || { echo "gh not installed: brew install gh"; exit 1; }
	@test -f "$(RELEASE_ZIP)" || { echo "no $(RELEASE_ZIP): run make release first"; exit 1; }
	@# Asked separately, because an unauthenticated gh fails the lookup below
	@# exactly as a missing release does, and would send you looking at CI for a
	@# problem that is on this machine.
	@gh auth status >/dev/null 2>&1 || { echo "gh is not signed in: run gh auth login"; exit 1; }
	@gh release view "$(VERSION)" >/dev/null 2>&1 \
		|| { echo "no $(VERSION) release yet: CI cuts it once the tagged commit is green"; exit 1; }
	@# Re-checked here rather than trusted from `release`: the zip on disk may
	@# predate the tag, and an un-notarised upload is only discovered by whoever
	@# downloads it.
	@$(MAKE) verify
	gh release upload "$(VERSION)" "$(RELEASE_ZIP)" --clobber
	@echo "attached $(RELEASE_ZIP) to $(VERSION)"

# The real gate, and only meaningful once notarised — which is why spctl lives
# here and not in `app`, where it could never pass.
verify:
	codesign --verify --strict --verbose=2 "$(APP)"
	spctl --assess --type execute --verbose=4 "$(APP)"
	xcrun stapler validate "$(APP)"

icon: $(APP_ICON)

# iconutil accepts these ten names and no others, and refuses a file whose
# pixel size does not match its name.
#
# assets/icon.png is square, so nothing is stretched. It is the emblem alone:
# the banner beside it carries the name and a tagline, which say nothing at 32
# pixels and are noise at 16.
build/AppIcon.icns: $(ARTWORK) $(SMALL_ARTWORK) scripts/iconset.sh
	@mkdir -p build
	scripts/iconset.sh "$(ARTWORK)" "$(SMALL_ARTWORK)" $@

# The development icon is the same artwork with its hue rotated: recognisably
# this application, and not mistakable for the installed one in the Dock or in
# Command-Tab.
build/icon-dev.png: $(ARTWORK) scripts/tint.swift
	@mkdir -p build
	swift scripts/tint.swift "$(ARTWORK)" $@ $(DEV_HUE)

build/icon-small-dev.png: $(SMALL_ARTWORK) scripts/tint.swift
	@mkdir -p build
	swift scripts/tint.swift "$(SMALL_ARTWORK)" $@ $(DEV_HUE)

build/AppIconDev.icns: build/icon-dev.png build/icon-small-dev.png scripts/iconset.sh
	scripts/iconset.sh build/icon-dev.png build/icon-small-dev.png $@

clean:
	rm -rf .build build
