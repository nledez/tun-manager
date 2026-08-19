# Working on tun-manager

Written for coding agents, and useful to anyone new to the repository. In
English like the rest of it; conversations with the maintainer are in French.

## Testing

**Everything is tested.** A change arrives with the test that would have caught
its absence. Write the test first, watch it fail for the reason you expect, then
make it pass — a test that has never been red has not been shown to test
anything.

**Anything not tested is explained, in two places.** Both are required:

1. A `NOT TESTED:` comment **on the code itself**, next to what is untested,
   saying why in a couple of lines and naming the section of
   `docs/coverage-gaps.md` that argues for it. Whoever reads that code sees the
   reason without going looking for it.
2. A section in `docs/coverage-gaps.md` under `## Deliberately untested`, with a
   `###` heading matching the name the comment refers to.

```go
// NOT TESTED: this calls os.Exit, so covering it means starting a subprocess to
// confirm that Go can start a program. Everything it reaches is covered.
// See docs/coverage-gaps.md, "main".
func main() {
```

`make markers-check` fails when a marker names a section that does not exist. It
runs in `make all` and in CI. Nothing checks the reverse — that uncovered code
carries a marker — so that part is on you.

**"Cannot be tested" is nearly always premature.** Every claim of that kind in
this repository turned out to be wrong: the WireGuard control sockets, the host
interface listing, `osascript`, the composition root, a glob pattern "that can
only be a literal". Each was reached through one line that did nothing but call
out, and one line can be injected — an interface, a function field, a
configurable path. Look for the handle before writing the excuse.

**Tests are hermetic.** No test may read anything the machine happens to have.
Configuration paths, binaries and run directories are pointed at `t.TempDir()`.
This is not theoretical: the `doctor` tests once read `/opt/homebrew/bin/wg-quick`,
passed locally and failed in CI.

**Fixtures are invented.** Never copy real configuration into `testdata`.
Addresses come from the ranges reserved for documentation — RFC 5737
(`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`) and RFC 3849
(`2001:db8::/32`) — so no test can reach a real host. WireGuard keys are valid
base64 generated for the repository. Tunnel names are placeholders: `alpha`,
`bravo`, `charlie`.

**Coverage.** `make cover` prints the total and fails below the floor in the
Makefile. The floor tracks the current figure; raise it when coverage rises.
Read `docs/coverage-gaps.md` before concluding that something is out of reach.

## Before saying it is done

```sh
make all      # vet, lint, tests, notices, markers, build
make cover    # the total against the floor
```

Report what the commands printed. If something fails, say so with the output.

## Secrets

`PrivateKey` and `PresharedKey` are never parsed, stored or printed. The TUI log
pane redacts anything shaped like a WireGuard key. Keep it that way, and never
commit a real endpoint, key or tunnel name.

## The menu bar application

`macos/` holds a Swift client of the status feed. It is built with SwiftPM and
has no Xcode project, so every file in it reviews as text.

The testing rules above apply, with one carve-out and one addition:

- **The Go coverage floor does not apply to Swift.** They are separate
  contracts, and `make all` deliberately does not build or test `macos/` — the
  Go gate must not start requiring Xcode.
- **`Sources/TunManagerMenuBar/` is untested on purpose**, argued for in
  `macos/docs/coverage-gaps.md` the same way a `NOT TESTED:` marker is here.
  That is only defensible because every decision lives in `TunManagerFeed`,
  which does not link AppKit — so a decision cannot drift into an untested file
  without the compiler noticing. **An `if` in the AppKit layer is a bug of
  placement.**

Fixtures follow the same rules: invented keys, RFC 5737 addresses, tunnels
called `alpha`, `bravo`, `charlie`.

Run it with `make macos-test` and `make macos-app`.

## Stand-in publishers

Trying the menu bar application by hand needs something on the other end of the
socket. **Never bind the default path**, `/var/run/tun-manager.sock`.

A stand-in there is indistinguishable from the real thing: the application shows
invented tunnels with no sign that they are invented, and one left running
outlives the session that started it. This is not hypothetical — a fixture
tunnel called `loose` was reported as a bug in the tunnel list, because from the
outside that is exactly what it looked like.

So:

```sh
# Bind somewhere private, and point the application at it.
python3 /tmp/fake/publisher.py            # binds /tmp/fake/f.sock
defaults write net.ledez.tun-manager.menubar FeedSocket /tmp/fake/f.sock

# Afterwards, both halves. Leaving the key behind is the same failure.
pkill -f fake/publisher.py
defaults delete net.ledez.tun-manager.menubar
```

`About Tun Manager…` prints the socket in use, which is how to tell at a glance
which one is being looked at.

Better still, prefer the flag to the defaults key:

```sh
"macos/build/Tun Manager (dev).app/Contents/MacOS/tun-manager-menubar" \
    --socket /tmp/tm-demo/feed.sock
```

It leaves nothing behind. Half of the failure above was the key outliving the
publisher; an argument cannot.

## Stand-in WireGuard

`internal/tools/wgsim` does the same job one layer down: it serves the UAPI
sockets tun-manager reads WireGuard through, so the program can be tried, and
photographed, without real tunnels.

**Never point it at `/var/run/wireguard`.** Its sockets are indistinguishable
from a live tunnel's and the real `wg` would find them. It refuses that path
outright, but the rule is the point rather than the guard.

```sh
make demo                       # writes the fixtures, serves /tmp/tm-demo
pkill -f wgsim; rm -rf /tmp/tm-demo
```

`configs/wireguard/*.conf` are generated and committed. Edit
`internal/tools/wgsim/main.go` and run `make demo-configs`; `make all` fails when
the two have drifted apart. They carry no `PrivateKey`: the parser never reads
one, so leaving it out keeps every key out of the repository and means `wg-quick`
refuses the file, which is what stops a fixture from ever bringing anything up.

## Git

Commit messages, PR titles and issues are in English. Say what changed and why
the change is the right one; a message that only restates the diff is noise.

## Releases

`make release VERSION=X.Y.Z` checks the tree, tags and pushes. Pushing the tag
is what publishes: CI re-runs the whole suite on the tagged commit and only then
builds the archives and creates the release. `make release-check` runs the same
pipeline locally without publishing, and CI runs it on every push.

## The program itself

It runs entirely as root, started with `sudo`. Two consequences that catch
people out:

- `sudo` rewrites `HOME` to `/var/root`, so the configuration path is resolved
  through `SUDO_USER` (`internal/privdrop`).
- Root has no GUI session, so notifications are posted back as the pre-sudo
  user.

A tunnel is matched to its interface through `/var/run/wireguard/<name>.name`,
never by peer public key alone: two configs reaching the same server through
different endpoints share a key, and matching on it reports both as up when only
one is.
