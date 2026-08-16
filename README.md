# tun-manager

[![CI](https://github.com/nledez/tun-manager/actions/workflows/ci.yml/badge.svg)](https://github.com/nledez/tun-manager/actions/workflows/ci.yml)
[![Coverage Status](https://coveralls.io/repos/github/nledez/tun-manager/badge.svg?branch=main)](https://coveralls.io/github/nledez/tun-manager?branch=main)

A TUI and CLI for the WireGuard tunnels of a macOS laptop. One binary that
drives them and shows what is actually going on: handshake age, transferred
bytes, latency, and which network you are on.

```
 tun-manager  ctx: office (en0 198.51.100.42)                       next ⟳ 4m12s
 ───────────────────────────────────────────────────────────────────────────────
   NAME          GRP    STATE   HANDSHAKE  RX / TX        PING   ENDPOINT
 ▸ alpha         needed ● up    12s        1.2M / 840K    18ms   192.0.2.10:51820
   bravo         needed ● up    3m41s      220K / 96K     31ms   198.51.100.20:51821
   charlie       extra  ○ down                                   gateway.example:51824
 ───────────────────────────────────────────────────────────────────────────────
 r refresh · p ping · s down all · ␣ select · ⏎ toggle · n needed · l logs · ? help · q quit
```

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
make build              # ./bin/tun-manager
make install            # /usr/local/bin/tun-manager
mkdir -p ~/.config/tun-manager
cp configs/config.example.yaml ~/.config/tun-manager/config.yaml
```

The configuration file is where the groups live, so editing it is part of the
install. Without one the binary still runs and lists every tunnel, but none
belongs to a group and the group commands and keys have nothing to act on.

## Usage

```sh
sudo tun-manager                    # interactive TUI
sudo tun-manager status             # aligned table
sudo tun-manager status --json      # for tmux, starship, scripts
sudo tun-manager up alpha bravo
sudo tun-manager up --group needed  # resolved against the current network
sudo tun-manager down --all
tun-manager doctor                  # environment check, works without root
```

Closing the laptop:

```sh
sudo tun-manager down --all ; gum confirm "Restart VPN?" && sudo tun-manager
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
| `l` | log pane (command outcomes, errors) |
| `↑↓` `jk` | move · `?` help · `q` quit |

The table refreshes on its own every `refresh_interval` (5 minutes by default),
and right after every operation.

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

## Configuration

See [`configs/config.example.yaml`](configs/config.example.yaml). The part worth
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

## License

BSD 3-Clause, see [LICENSE](LICENSE).

The release archives also carry [THIRD-PARTY-NOTICES.txt](THIRD-PARTY-NOTICES.txt),
the licenses of every module linked into the binary. It is generated from the
module graph by `make notices` and CI fails when it is out of date, so a new
dependency cannot ship without its notice.
