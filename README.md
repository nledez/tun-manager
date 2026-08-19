<div align="center">

<img src="assets/tun-manager.png" alt="" width="140">

# tun-manager

**Watch and drive the WireGuard tunnels of a macOS laptop, from one binary.**

Handshake age, transferred bytes, latency, and which network you are on.

[![CI](https://github.com/nledez/tun-manager/actions/workflows/ci.yml/badge.svg)](https://github.com/nledez/tun-manager/actions/workflows/ci.yml)
[![Coverage Status](https://coveralls.io/repos/github/nledez/tun-manager/badge.svg?branch=main)](https://coveralls.io/github/nledez/tun-manager?branch=main)
[![License](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)

</div>

![The tun-manager terminal interface: a table of five tunnels with their group,
state, handshake age, transferred bytes, latency and endpoint, above a traffic
graph of the selected tunnel.](assets/screenshot-tui.png)

The tunnels above are invented, served by the simulator described in [Seeing it
work, without tunnels](#seeing-it-work-without-tunnels) — so this is
reproducible on a machine with no WireGuard on it at all.

While a batch runs, a spinner turns beside the context, the header counts the
tunnels off, and the row being waited on says so where its state would be — so a
slow `wg-quick` is never mistaken for a hung program:

```
 tun-manager  ctx: office (en0 198.51.100.42)  ⠋ 2 running        next ⟳ 4m12s
 ───────────────────────────────────────────────────────────────────────────────
    NAME          GRP    STATE     HANDSHAKE  RX / TX        PING   ENDPOINT
 ▸  alpha         needed ● up      12s        1.2M / 840K    18ms   192.0.2.10:51820
    bravo         needed ● stale   9m04s      2.3M / 961K    ×      198.51.100.20:51821
    charlie       extra  ⠋ starting                                 gateway.example:51824
    delta         —      ⠋ stopping                                 198.51.100.30:51822
```

## Why

I run several WireGuard tunnels. Some have to be up whenever the laptop is,
others only when I ask. Nothing I found managed that distinction, so for a
while it was a shell script driving [gum](https://github.com/charmbracelet/gum)
— which works, and stops scaling the moment the tunnel list grows.

What it had to keep:

- tunnels stay managed by [wireguard-tools](https://www.wireguard.com/), not by a reimplementation
- configurations stay unreadable to anyone but root, so a key cannot leak
  through a file listing
- which tunnels are *needed* depends on the network the machine is on

## Why root

The program runs entirely as root, started with `sudo`. Two reasons:

- the WireGuard control sockets under `/var/run/wireguard` are root-only, and
  that is where the live state (handshakes, counters, endpoints) comes from;
- `wg-quick` creates the `utun` interface and rewrites the routing table.

`sudo` asks for the password before the binary starts, so the TUI never has to
prompt for one. Two consequences are handled explicitly:

- `sudo` rewrites `HOME` to `/var/root`, so the configuration file is resolved
  through `SUDO_USER` instead;
- root has no GUI session, so notifications are posted back as the pre-sudo user.

## Install

Download the archive for your machine from the
[releases](https://github.com/nledez/tun-manager/releases) — `darwin_universal`
runs on both Intel and Apple Silicon — or build from source:

```sh
brew install wireguard-tools
make build              # ./bin/tun-manager alone
make install            # /usr/local/bin/tun-manager
                        #   + /Applications/Tun Manager.app

sudo mkdir -p /private/wireguard/config
sudo chown 0:0 /private/wireguard /private/wireguard/config
sudo chmod 700 /private/wireguard /private/wireguard/config
sudo ls -ld /private/wireguard /private/wireguard/config

mkdir -p ~/.config/tun-manager
cp configs/config.example.yaml ~/.config/tun-manager/config.yaml

# Sudo is mandatory: it copies the file into /private/wireguard/config, which is
# owned by root and readable by nobody else. A "# TO_CHECK=" line is mandatory
# in each configuration file before it can be imported.
sudo tun-manager import <config1> ~/Downloads/config1.conf
...
# Edit ~/.config/tun-manager/config.yaml to fit the imported configurations.
# The example ships placeholder names: replace every one of them under groups
# and overrides, or up --group refuses a tunnel that does not exist.
# import only ever adds to the "all" group -- needed and extra are yours to fill.

# Check installation before first run
sudo tun-manager doctor

open "/Applications/Tun Manager.app"
# "tun-manager is not running" is normal, fix this with:
sudo tun-manager

# Press 's' to start needed tunnels, check if menubar application is sync with TUI
```

`make install` builds both halves, so it needs the Swift toolchain as well as
Go. `make build` and `make macos-app` do one each if you only want one.

**Run it as yourself, not with `sudo`.** The two destinations have different
owners — `/Applications` belongs to the admin group and needs no privileges,
while `/usr/local/bin` belongs to root — so the target asks for a password by
itself, for that one step. Running the whole thing as root would build as root
and leave a working tree full of files you can no longer write.

`PREFIX` and `APPDIR` both override.

## Usage

```sh
sudo tun-manager                    # interactive TUI
sudo tun-manager status             # aligned table
sudo tun-manager status --json      # for tmux, starship, scripts
sudo tun-manager up alpha bravo
sudo tun-manager up --group needed  # resolved against the current network
sudo tun-manager down --all
sudo tun-manager import work ~/Downloads/work.conf
sudo tun-manager backup             # tar.gz of the configuration and every .conf
tun-manager doctor                  # environment check; sudo to see config_dir
```

### Keys

| Key | Action |
|---|---|
| `r` | refresh now |
| `p` | ping the check address of every live tunnel |
| `s` | tear everything down if anything is up, otherwise start the `needed` group |
| `␣` | select / deselect a tunnel |
| `⏎` | toggle the selection, or the row under the cursor |
| `n` | bring the `needed` group up |
| `g` | traffic graph of the tunnel under the cursor |
| `l` | log pane (command outcomes, errors) |
| `↑↓` `jk` | move · `?` help · `q` quit |

The table refreshes on its own every `refresh_interval` (5 minutes by default),
and right after every operation.

`g` opens a graph of the tunnel under the cursor, download above upload, each on
its own scale so that a busy download does not flatten the upload into a line
along the axis. Readings are taken once a second, but only while the pane is
open: closing it stops the sampling and drops the history.

A batch starts every tunnel at once, spaced a few milliseconds apart so that two
`wg-quick` runs do not reach the routing table in the same instant, so eight of
them take about as long as the slowest rather than as long as all of them added
up. Each reports for itself: the row says `starting` or `stopping` while it
waits, and the header counts how many are in flight.

Nothing waits for a batch. The cursor, the log pane and the help keep
responding, and `⏎` on another tunnel starts it there and then — batches
overlap. The one thing refused is a second `wg-quick` on a tunnel that already
has one, which is skipped rather than queued.

### Status feed

While `tun-manager` runs it publishes what it knows on a unix socket, so a
menu bar application can show tunnel state, graph traffic and raise
notifications without any privilege of its own.

```yaml
feed: true                              # default
feed_socket: /var/run/tun-manager.sock  # default
```

The socket is created by root and handed to whoever ran `sudo tun-manager`,
mode `0600`. It carries the same JSON as `tun-manager status --json`, one
object per line, plus a `hello` on connect, a `sample` once a second for each
tunnel a client asked to watch, a `ping` when a probe round finishes, and a
`bye` on the way out.

**Nothing on that socket can start or stop a tunnel.** A client may watch a
tunnel's counters, ask for a refresh, and ask for a ping. That is the whole
vocabulary.

The ping is the one verb with an effect outside the process: honouring it makes
`tun-manager`, which runs as root, send packets. What bounds it is that a client
names a *tunnel*, never an address — the address comes from that tunnel's
`# TO_CHECK=` line, so no name arriving on the socket can make `tun-manager`
reach somewhere it was not already configured to reach. Rounds are rate-limited
to one every two seconds, as refreshes are.

It is only alive while `tun-manager` is: there is no daemon. Run
`sudo tun-manager doctor` to see where the socket would bind and who could
read it.

### The menu bar application

`Tun Manager.app` is the consumer of that socket. It shows state, graphs traffic
and raises notifications, and it starts and stops nothing — which is why it needs
no privileges of its own and no helper installed anywhere.

<img src="assets/screenshot-app-menu.png" width="320" alt="The menu bar menu: tunnels grouped under needed and extra, each with a state glyph, then how long ago the state was updated, Refresh, Ping, About and Quit.">

The state is in the glyph rather than the colour: the menu bar of macOS 26 is
often transparent over whatever the wallpaper happens to be, and a coloured
glyph is unreadable there. `needed` is listed first because it is the group the
answer to "is everything up?" depends on.

`p` asks `tun-manager` for a round of probes — the same key it is in the
terminal. Clicking a tunnel opens a window, which lists them all with what the
terminal's table shows.

![The window's overview: every tunnel in one table, with state, handshake age,
transferred bytes, latency and endpoint. One tunnel is stale and its latency
cell holds a red cross.](assets/screenshot-app-overview.png)

A cross in the latency column is a probe that got no answer, with the reason on
hover. An empty cell is a tunnel nobody probed, which is a different thing and
reads as one.

Picking a tunnel on the left shows what is going through it — download and
upload each on its own scale, so a busy download does not flatten the upload
into a line along the axis. The byte counts beside the charts are as fresh as
the charts rather than the minutes-old ones from the last full refresh.

![One tunnel in the window: interface, endpoint, handshake age, bytes received
and sent, check address and latency, above separate charts for received and sent
traffic.](assets/screenshot-app-detail.png)

Switching tunnels keeps every history: the subscriptions are released only when
the window closes. Building it is in [`macos/README.md`](macos/README.md).

## Seeing it work, without tunnels

Everything above needs tunnels that are up, which is a poor way to try something
out and a worse way to take a screenshot. `internal/tools/wgsim` stands in for a
machine with tunnels on it: it writes the `.conf` files and serves the UAPI
sockets tun-manager reads them through. Five invented tunnels, addresses from the
ranges reserved for documentation, counters that climb while it runs.

```sh
make demo
```

That prints the two commands to run, one per window. **No `sudo`:** nothing a
simulated run reads is root-only.

Under the hood it is four flags, and they work on their own:

```sh
tun-manager --config <file> --config-dir <dir> --wg-socket <dir> \
            --feed-socket <path> --fake-ping
```

`--wg-socket` names a *directory*, not a socket: WireGuard's userspace API is one
socket per interface, and the `<name>.name` files live beside them. One flag
moves both, because that is how `/var/run/wireguard` is.

`--fake-ping` invents the round trips instead of measuring them. The demo's check
addresses reach nothing, so a real probe would time out on every row. It is
derived from the address rather than drawn at random, so the same demo
photographs the same twice.

`tun-manager doctor` leads with a warning naming whatever is simulated. Without
it, every line under it describes something that does not exist.

The demo's `wg_quick` points at a stub that refuses, so a key pressed during a
demo cannot reach the machine's real tunnels.

The menu bar application takes the matching flag:

```sh
"/Applications/Tun Manager.app/Contents/MacOS/tun-manager-menubar" --socket <path>
```

[`docs/simulator.md`](docs/simulator.md) has the rest: the simulator's own flags,
what it puts in that directory, the five tunnels and how to change them, and what
an empty table is telling you.

## How a tunnel's state is decided

A tunnel is matched to its live interface through `/var/run/wireguard/<name>.name`,
the file `wg-quick` writes when it brings a tunnel up. That is the only reliable
identity: `wg-quick` picks whatever `utunN` is free, and **peer public keys are
not unique** — two configs reaching the same server through different endpoints
(an IPv4 and an IPv6 one, say) share theirs, and matching on the key alone
reports both as up when only one is.

When that directory cannot be read, matching falls back to the peer public key;
`doctor` says so.

| State | Meaning |
|---|---|
| `● up` | a device carries the peer and handshook less than 3 minutes ago |
| `● stale` | the device exists but has not handshook lately — up, but nothing gets through |
| `○ down` | no live device carries the tunnel |

A down tunnel has no interface, so its handshake, counters and latency columns
stay empty. `p` skips it too: several configs may share a check address, and
probing it while a sibling is up would report a latency for both.

## Check addresses

`up` pre-checks before acting: if the tunnel's check address already answers,
`wg-quick` is not run. The address comes from a
`# TO_CHECK=<ip>` comment in the `.conf`; without one it is inferred from the
first single-host `AllowedIPs` entry, then from the endpoint host.

## Adding a tunnel

```sh
sudo tun-manager import <name> <file.conf>
```

Copies the `.conf` into `config_dir` as `<name>.conf`, mode `0600` and owned by
root — it holds a private key, and `wg-quick` reads it as root — then lists
`<name>` under `groups: all` in your configuration.

Everything is checked before anything is written, and the one check worth
knowing about is that **the file must carry a `# TO_CHECK=<address>` comment**.
Without one there is no address to ping, and a tunnel that is up while carrying
nothing looks exactly like one that works. An inferred address is not enough
here: the comment has to be there.

The configuration is copied to `config.yaml.before-update` first, and the edit
goes through the YAML rather than through a re-serialised struct, so your
comments and the order you wrote things in survive. Importing the same name
twice does not list it twice, and an existing `<name>.conf` is never replaced —
remove it first if that is what you mean.

## Backups

```sh
sudo tun-manager backup
```

Writes `tun-manager-<date>-<time>.tar.gz` beside the configuration directory —
`/private/wireguard/` by default, so it lands next to what it archives rather
than inside it, where a `*.conf` glob would start finding archives. The name
sorts by age, which is how a directory of them gets read.

```
tun-manager-20260818-142305.tar.gz
├── config.yaml          your ~/.config/tun-manager/config.yaml
└── config/alpha.conf    one per tunnel, original modes preserved
    config/bravo.conf
```

**The archive holds every private key on the machine in one file.** It is
created `0600` and stays root's — unlike the configuration, which `import`
hands back to you. Keeping the file modes inside the archive means a restore
puts a `0600` `.conf` back as `0600`, rather than as whatever the umask of the
day decides.

Two backups in the same second do not overwrite each other, and a backup that
fails partway removes what it had written: half an archive is worse than none,
because it looks like a backup.

## Configuration

See [`configs/config.example.yaml`](configs/config.example.yaml). Every field is
optional and a key left out gets its default, but **a key tun-manager does not
recognise is refused**, naming the file and the line — a misspelling that did
nothing in silence would be indistinguishable from a setting that does not work.
The cost is that a configuration written for a newer tun-manager will not load
on an older one, which is a clear failure at startup rather than a setting that
quietly does not apply. The part worth
understanding is `overrides`: a tunnel's group can depend on the detected
network, so a tunnel that reaches a LAN you are sometimes sitting on can be
*needed* when away and merely *extra* when already there.

## Development

[`AGENTS.md`](AGENTS.md) holds the conventions this repository is kept to:
everything is tested, anything that is not carries a `NOT TESTED:` comment and a
section in [`docs/coverage-gaps.md`](docs/coverage-gaps.md), fixtures are
invented, and pushed history is never rewritten.

```sh
make                # vet, lint, tests, notices check, build
make test           # everything, no network and no root needed
make race
make lint           # golangci-lint, configured in .golangci.yml
make cover          # coverage per package, fails below the floor in the Makefile
make cover-html     # per-statement report in the browser
make notices        # regenerate THIRD-PARTY-NOTICES.txt from the module graph
make markers-check  # every NOT TESTED marker names a documented section
make demo           # the simulator and the two commands to point at it
make demo-configs   # regenerate configs/wireguard from internal/tools/wgsim
make release-check  # runs the release pipeline without publishing
```

Cutting a release is one command. It refuses rather than repairs: the branch
has to be `main`, the tree clean, `main` level with `origin/main`, the tag
unused on both sides, and `make all` green. Then it tags and pushes, and CI
does the rest.

```sh
make release VERSION=0.1.0
make release VERSION=0.1.0 DRY_RUN=1   # every check, no tag
```

CI runs the same checks on macOS, the only platform this targets, and publishes
coverage to Coveralls on every push and pull request. It also runs the release
pipeline short of publishing, so a broken `.goreleaser.yaml` fails there rather
than after a tag is pushed. Tagging `vX.Y.Z` runs everything again and cuts a
GitHub release.

The suite runs without root and without touching a real tunnel: the WireGuard
state, the command runner, the network interfaces, the notification transport
and the clock are all injected.
[`docs/coverage-gaps.md`](docs/coverage-gaps.md) lists what is left uncovered
and why. Each deliberate omission carries a `NOT TESTED:` comment on the code itself,
naming the section that argues for it; `make markers-check` fails when one of
those sections is missing.

Secrets never leave the parser: `PrivateKey` and `PresharedKey` are ignored, and
the log pane redacts anything shaped like a WireGuard key.

## Notification without `Tun Manager.app`

Notifications show the logo above as a thumbnail when [`terminal-notifier`][tn]
is installed (`brew install terminal-notifier`), and fall back to `osascript`
without it.

The icon on the left of a notification is not ours to set: since macOS 11 it is
the icon of the `.app` bundle that sent the notification, so it shows whichever
tool did the sending. `terminal-notifier` still accepts `-appIcon`, but macOS
ignores it. `sudo tun-manager notify` posts a sample so you can see what your
machine does with it.

[tn]: https://github.com/julienXX/terminal-notifier

## License

BSD 3-Clause, see [LICENSE](LICENSE).

The release archives also carry [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt),
the licenses of every module linked into the binary. It is generated from the
module graph by `make notices` and CI fails when it is out of date, so a new
dependency cannot ship without its notice.
