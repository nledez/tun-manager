#!/bin/sh
# Builds a .icns from two drawings: a simplified one for the sizes below 64
# pixels, where the detailed one turns to mush, and the detailed one above.
#
# Lives here rather than in the Makefile because it is built twice — once per
# flavour — and a loop written twice is a loop that drifts.
#
#   iconset.sh <detailed.png> <simplified.png> <out.icns>

set -eu
detailed=$1
simplified=$2
output=$3
staging="${output%.icns}.iconset"

rm -rf "$staging"
mkdir -p "$staging"

# iconutil accepts these ten names and no others, and refuses a file whose
# pixel size does not match its name.
for pair in 16:icon_16x16 32:icon_16x16@2x 32:icon_32x32 64:icon_32x32@2x; do
	size=${pair%%:*}
	name=${pair##*:}
	sips -z "$size" "$size" "$simplified" --out "$staging/$name.png" >/dev/null
done
for pair in 128:icon_128x128 256:icon_128x128@2x 256:icon_256x256 \
	512:icon_256x256@2x 512:icon_512x512 1024:icon_512x512@2x; do
	size=${pair%%:*}
	name=${pair##*:}
	sips -z "$size" "$size" "$detailed" --out "$staging/$name.png" >/dev/null
done

iconutil -c icns "$staging" -o "$output"
