# The WireGuard simulator

`internal/tools/wgsim` stands in for a machine with tunnels on it, so tun-manager
can be tried — and photographed — without anybody's real network in the picture.

Everything it reports is invented. The names are the repository's placeholders
and the addresses come from the ranges reserved for documentation, RFC 5737 for
IPv4 and RFC 3849 for IPv6, so no fixture can name a real host.

## The short way

```sh
make demo
```

It writes the `.conf` files, starts the simulator, and prints the two commands to
run — one for the terminal, one for the window. Stop it with `^C`, then:

```sh
rm -rf /tmp/tm-demo
```

**No `sudo`.** Nothing a simulated run reads is root-only, and asking for a
password to read `/tmp` is how a demo ends up never run. `tun-manager doctor`
says so on the root line rather than failing it.

## The long way

Three programs, three windows.

```sh
# 1. the simulator
go run ./internal/tools/wgsim \
    --config configs/config.example.yaml \
    --config-dir configs/wireguard \
    --wg-socket /tmp/tm-demo/wireguard

# 2. tun-manager, pointed at it
bin/tun-manager \
    --config /tmp/tm-demo/config.yaml \
    --config-dir configs/wireguard \
    --wg-socket /tmp/tm-demo/wireguard \
    --feed-socket /tmp/tm-demo/feed.sock \
    --wg-quick configs/demo/wg-quick-stub.sh \
    --fake-ping

# 3. the menu bar application, pointed at tun-manager
"macos/build/Tun Manager (dev).app/Contents/MacOS/tun-manager-menubar" \
    --socket /tmp/tm-demo/feed.sock
```

The configuration in step 2 is `configs/config.example.yaml`, copied as it is —
`make demo` does that for you. What used to be a `wg_quick` key in it is now the
`--wg-quick` flag: the real one lives in
`/private/wireguard/config/tun-manager.yaml`, which only root can read, and a
simulated run is not root.

`configs/demo/wg-quick-stub.sh` refuses every call and says why. Without it, `s`
or `space` pressed during a demo would reach the machine's real tunnels through
the real `wg-quick`.

## Its flags

| flag | |
|---|---|
| `--config-dir DIR` | where to write the `.conf` files. Required. |
| `--wg-socket DIR` | where to bind the UAPI sockets and write the `<name>.name` files. Required unless `--write-only`. |
| `--config FILE` | a configuration to check the tunnel names against. Optional, and worth giving: see below. |
| `--write-only` | write the `.conf` files and exit, without serving anything. |

`--config` is a guard rather than an input. The simulator's tunnels are written
into its own source; if the configuration lists a name it does not invent, the
demo shows a table with a missing row and the reason is two files away. Given the
flag, it refuses at startup and names the file.

## What it serves

WireGuard's userspace API is **one socket per interface**, and the interface's
identity is the socket's file name — there is no verb for "list the devices".
That is why `--wg-socket` names a directory rather than a socket.

Beside the sockets go the `<name>.name` files `wg-quick` writes on darwin, which
map a tunnel name to the interface carrying it. tun-manager needs them: peer
public keys are not unique enough to tell two configs apart when both reach the
same server.

```
/tmp/tm-demo/wireguard/
    alpha.name      utun4
    utun4.sock      the UAPI socket for utun4
    ...
```

A tunnel that is **down** has neither. That absence is exactly what tun-manager
reads as down, which is why `echo` below produces no files at all.

## The tunnels

Five, fixed in `internal/tools/wgsim/main.go`. One of each thing worth showing:

| | state | why it is there |
|---|---|---|
| `alpha` | up | an ordinary tunnel carrying traffic |
| `bravo` | up | another, at a different rate, so the two chart lines stay apart |
| `delta` | up | reached over IPv6 |
| `charlie` | stale | handshook four minutes ago, past `wg.StaleAfter`, and its check address answers nothing |
| `echo` | down | no interface, no socket, no name file |

Counters climb while it runs, at a rate of their own per tunnel, so the rate
charts have something to draw. Handshakes are counted back from **now** rather
than from when the simulator started: what is fixed about a tunnel here is how
long ago it handshook, not when — an instant pinned at startup drifts every
tunnel into stale three minutes into the demo.

The configured endpoint and the one on the socket are deliberately different for
`charlie` and `delta`. That is what really happens: a `.conf` holds a DNS name
and wireguard-go reports the address it resolved to. The table prefers the
resolved one, which is more use than a name when something is wrong.

To change the demo, edit the table and run `make demo-configs`.

## The generated .conf files

`configs/wireguard/*.conf` are written by the simulator and **committed**, so
they can be read on the forge without running anything.

```sh
make demo-configs        # rewrite them
make demo-configs-check  # fail if they have drifted from the generator
```

The check is part of `make all`, the same guard `THIRD-PARTY-NOTICES.txt` has.
Editing a `.conf` by hand is not how they are changed; editing the table is.

They carry no `PrivateKey`. tun-manager's parser never reads one — it knows only
`PublicKey` — so leaving it out keeps every key, however invented, out of the
repository. It also means `wg-quick` refuses the files, which is what stops a
demo fixture from ever bringing anything up.

Peer public keys are derived from the tunnel name, so the key in the `.conf` and
the key on the socket cannot drift apart, and regenerating produces the same
bytes every time.

## Two rules

**Never point `--wg-socket` at `/var/run/wireguard`.** Its sockets are
indistinguishable from a live tunnel's and the real `wg` would find them. The
simulator refuses that path outright, but the rule is the point rather than the
guard.

**Never bind the feed on `/var/run/tun-manager.sock`.** A stand-in there is
indistinguishable from the real thing: the menu bar shows invented tunnels with
no sign that they are invented, and one left running outlives the session that
started it. This is not hypothetical — a fixture tunnel called `loose` was once
reported as a bug in the tunnel list, because from the outside that is exactly
what it looked like.

Prefer `--socket` to `defaults write ... FeedSocket` for the same reason: half of
that failure was the key outliving the publisher, and an argument cannot.

## When the table is empty

- **No tunnels at all** — `--config-dir` is not where tun-manager is reading.
  Both programs must be given the same one. `tun-manager doctor` prints the
  directory it looked in and how many `.conf` files it found.
- **Every tunnel down** — `--wg-socket` disagrees between the two, or the
  simulator is not running. `doctor` prints the run directory too.
- **Every tunnel stale** — the simulator has been running long enough that its
  handshakes have aged out. It should not happen; if it does, the ages are being
  pinned rather than counted back.
- **Every latency a red ×** — `--fake-ping` was left out, so real probes are
  going to addresses reserved for documentation, which answer nothing.
- **A tunnel in the list that does not exist** — something is still bound to a
  socket somewhere. Check `lsof -U | grep tm-demo`, and see the two rules above.
