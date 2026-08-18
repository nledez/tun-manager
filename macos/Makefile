# The menu bar client. Kept out of the repository's root `make all` on purpose:
# the Go gate must not start requiring Xcode, and a Swift regression must not be
# able to block a Go release.

APP := build/Tun Manager.app
ARTWORK := ../assets/icon.png
BUNDLE_ID := net.ledez.tun-manager.menubar

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
# Launch Services accepts one to three dot-separated integers and nothing else,
# so v0.2.0-3-gdeadbee-dirty has to be trimmed down to 0.2.0.
SHORT_VERSION := $(shell printf '%s' "$(VERSION)" | sed -E 's/^v//; s/-.*$$//; s/^[^0-9].*$$/0.0.0/')
BUILD_NUMBER := $(shell git rev-list --count HEAD 2>/dev/null || echo 0)

# Ad-hoc by default, so a fresh clone builds on any Mac with no keychain set up.
# Override for a signature that survives being moved:
#   make app SIGN_IDENTITY="Apple Development: you (TEAMID)"
SIGN_IDENTITY ?= -

.PHONY: all build test app run icon sign clean

all: test app

build:
	swift build

test:
	swift test
	plutil -lint Resources/Info.plist

# Assembles the bundle. `app` is phony and has no file prerequisites: make
# breaks on prerequisites containing spaces, and "Tun Manager.app" has one.
app: icon
	swift build -c release
	rm -rf "$(APP)"
	mkdir -p "$(APP)/Contents/MacOS" "$(APP)/Contents/Resources"
	# Asked for rather than spelled out: the path moves the moment --arch is
	# added, and a hardcoded .build/release would package a stale binary.
	cp "$$(swift build -c release --show-bin-path)/tun-manager-menubar" \
		"$(APP)/Contents/MacOS/tun-manager-menubar"
	cp build/AppIcon.icns "$(APP)/Contents/Resources/AppIcon.icns"
	# Patched on the copy, so the committed plist stays a complete, lintable
	# file a reviewer can read rather than a template full of placeholders.
	cp Resources/Info.plist "$(APP)/Contents/Info.plist"
	plutil -replace CFBundleShortVersionString -string "$(SHORT_VERSION)" "$(APP)/Contents/Info.plist"
	plutil -replace CFBundleVersion -string "$(BUILD_NUMBER)" "$(APP)/Contents/Info.plist"
	plutil -replace NETLedezGitDescribe -string "$(VERSION)" "$(APP)/Contents/Info.plist"
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

icon: build/AppIcon.icns

# iconutil accepts these ten names and no others, and refuses a file whose
# pixel size does not match its name.
#
# assets/icon.png is square, so nothing is stretched. It is the emblem alone:
# the banner beside it carries the name and a tagline, which say nothing at 32
# pixels and are noise at 16.
build/AppIcon.icns: $(ARTWORK)
	rm -rf build/AppIcon.iconset
	mkdir -p build/AppIcon.iconset
	@for pair in 16:icon_16x16 32:icon_16x16@2x 32:icon_32x32 64:icon_32x32@2x \
		128:icon_128x128 256:icon_128x128@2x 256:icon_256x256 \
		512:icon_256x256@2x 512:icon_512x512 1024:icon_512x512@2x; do \
		size=$${pair%%:*}; name=$${pair##*:}; \
		sips -z $$size $$size "$(ARTWORK)" --out "build/AppIcon.iconset/$$name.png" >/dev/null; \
	done
	iconutil -c icns build/AppIcon.iconset -o build/AppIcon.icns

clean:
	rm -rf .build build
