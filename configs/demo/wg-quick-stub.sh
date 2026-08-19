#!/bin/sh
# Stands in for wg-quick in the demo, and refuses.
#
# The demo's tunnels are served by internal/tools/wgsim: there is no interface
# to create and no route to write. Pointing wg_quick at the real binary would
# mean a key pressed during a demo reached the machine's actual tunnels.
echo "wg-quick-stub: this is the tun-manager demo; nothing here can be brought $1" >&2
echo "wg-quick-stub: the tunnels are served by internal/tools/wgsim" >&2
exit 1
