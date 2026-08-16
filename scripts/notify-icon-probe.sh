#!/usr/bin/env bash
#
# Probes the three ways of putting an icon on a macOS notification, so that a
# screenshot says which one works.
#
# macOS takes the icon of a notification from the .app bundle that sends it.
# terminal-notifier sends from its own bundle, which is why every notification
# so far has shown a terminal. -appIcon used to override that and no longer
# does, so the alternatives are attaching the image beside the text, or sending
# from a bundle of our own.
#
# Temporary: delete this once one of the three is chosen.
#
# Usage: scripts/notify-icon-probe.sh 1|2|3|all

set -euo pipefail

cd "$(dirname "$0")/.."
image="$PWD/assets/tun-manager.png"
[ -f "$image" ] || { echo "no image at $image" >&2; exit 1; }

command -v terminal-notifier >/dev/null 2>&1 || {
	echo "terminal-notifier is not installed: brew install terminal-notifier" >&2
	exit 1
}

# bundle prints the path of terminal-notifier's own .app, which sits beside the
# bin/ directory holding the executable. Resolved rather than hardcoded so a
# brew upgrade does not silently probe an old copy.
bundle() {
	local exe
	exe=$(command -v terminal-notifier)
	exe=$(python3 -c 'import os,sys; print(os.path.realpath(sys.argv[1]))' "$exe")
	local guess="${exe%/bin/terminal-notifier}/terminal-notifier.app"
	if [ -d "$guess" ]; then
		echo "$guess"
		return
	fi
	find "$(brew --prefix)/Cellar/terminal-notifier" -maxdepth 2 -name 'terminal-notifier.app' 2>/dev/null | head -1
}

one() {
	echo "1: -appIcon, the flag that used to replace the icon"
	terminal-notifier \
		-title "tun-manager" \
		-subtitle "TEST 1 appIcon" \
		-message "the icon on the LEFT should be the logo" \
		-appIcon "$image"
}

two() {
	echo "2: -contentImage, the image beside the text rather than as the icon"
	terminal-notifier \
		-title "tun-manager" \
		-subtitle "TEST 2 contentImage" \
		-message "a thumbnail of the logo should be on the RIGHT" \
		-contentImage "$image"
}

# three sends from a copy of terminal-notifier's bundle carrying our icon and
# our identifier. The copy is rebuilt every run: it is a probe, and a stale one
# would answer the wrong question.
three() {
	local src app iconset
	src=$(bundle)
	if [ -z "$src" ] || [ ! -d "$src" ]; then
		echo "terminal-notifier.app not found" >&2
		return 1
	fi

	app="${TMPDIR:-/tmp}/tun-manager-probe/tun-manager.app"
	rm -rf "$(dirname "$app")"
	mkdir -p "$(dirname "$app")"
	cp -R "$src" "$app"

	iconset="${TMPDIR:-/tmp}/tun-manager-probe/icon.iconset"
	mkdir -p "$iconset"
	for size in 16 32 64 128 256 512; do
		sips -Z "$size" "$image" --out "$iconset/icon_${size}x${size}.png" >/dev/null
		sips -Z "$((size * 2))" "$image" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
	done
	# Keeping the file name the bundle already refers to avoids editing the
	# plist key as well.
	iconutil -c icns "$iconset" -o "$app/Contents/Resources/Terminal.icns"

	# A distinct identifier is what makes macOS treat this as its own source,
	# with its own icon and its own entry in the notification settings.
	plutil -replace CFBundleIdentifier -string "net.ledez.tun-manager" "$app/Contents/Info.plist"
	plutil -replace CFBundleName -string "tun-manager" "$app/Contents/Info.plist"

	# Replacing a resource breaks the existing signature, and macOS will not run
	# a bundle whose signature does not match. Ad-hoc signing is free and needs
	# no developer account.
	codesign --force --deep --sign - "$app" >/dev/null 2>&1

	echo "3: our own bundle, $app"
	"$app/Contents/MacOS/terminal-notifier" \
		-title "tun-manager" \
		-subtitle "TEST 3 bundle" \
		-message "the icon on the LEFT should be the logo"
}

case "${1:-}" in
1) one ;;
2) two ;;
3) three ;;
all)
	one
	sleep 2
	two
	sleep 2
	three
	;;
*)
	echo "usage: $0 1|2|3|all" >&2
	echo >&2
	echo "  1  -appIcon        icon on the LEFT" >&2
	echo "  2  -contentImage   thumbnail on the RIGHT" >&2
	echo "  3  own bundle      icon on the LEFT" >&2
	exit 1
	;;
esac

echo
echo "Screenshot the notification. The subtitle says which test it came from."
echo "If an icon looks stale, macOS caches them per bundle identifier:"
echo "  killall NotificationCenter"
