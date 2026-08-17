# Status feed — design

A read-only stream of tunnel state, published by the running TUI over a unix
socket, so that a native macOS menu bar application can show status, draw
graphs and raise notifications without any privilege of its own.

## Why

`tun-manager` runs entirely as root and is driven from a terminal. That is the
right shape for controlling tunnels, and this design does not change it. What it
does not give is an answer to "are my tunnels up?" without switching to a
terminal window and reading a table.

A menu bar item answers that at a glance. It cannot be the same program: an
AppKit application launched from Finder runs as the user, and running AppKit as
root breaks TCC, notifications and the window server connection.

So the two are split by what they need, not by what they show:

| | needs root | does |
|---|---|---|
| `tun-manager` | yes | reads the control sockets, runs `wg-quick` |
| `Tun Manager.app` | no | subscribes, displays, notifies |

The application never starts or stops a tunnel. That is the whole reason this
design is small: with no control verb on the wire there is no authorisation
question to answer, no privileged helper to install, and no way to turn the
socket into a way of cutting somebody's VPN.

## Non-goals

- **No daemon.** Nothing is installed in `/Library/LaunchDaemons`. The publisher
  is the `sudo tun-manager` process itself. When it is not running, the menu bar
  shows that it is not running.
- **No control.** No message on this socket can bring a tunnel up or down. Adding
  one later would reopen every question this design avoids.
- **No network.** The socket is a unix socket. A real iPhone cannot subscribe to
  it, by construction. Reaching one would take a different transport and a
  different threat model.
- **The application is not built here.** This specifies the Go side and the wire
  contract it honours. The Swift side is described only as far as the contract
  constrains it.

## Inherited constraints

- The publisher runs as root, so anything it creates on disk is root-owned until
  it is given away. `internal/privdrop` already resolves the pre-sudo user and
  chowns files; the socket uses the same path.
- `Update` is pure. Publishing is a side effect and therefore happens in a
  `tea.Cmd`, the way notifications already do in `internal/tui/update.go`.
- Everything is tested, and untested code is documented twice. The design below
  is arranged so that the whole package is reachable from a test: the socket
  path is configuration, and the thing that reads counters is an interface.
- `PrivateKey` and `PresharedKey` are never parsed. Nothing on this wire could
  carry them. Peer public keys are not secret but are not published either: the
  application has no use for them.

## Architecture

```
sudo tun-manager                      root, foreground, unchanged in behaviour
  ├── internal/tui        drives everything, as today
  └── internal/feed       listener, started with the TUI, stopped with it
        │
        │  /var/run/tun-manager.sock   srw------- <pre-sudo user>
        ▼
Tun Manager.app                       the user's session, no privilege
```

`internal/feed` depends on `internal/wire` for the JSON vocabulary, on
`internal/app` for the view and sample types, and on `internal/privdrop` for the
ownership handover. Nothing depends on `feed` except the composition root and
`internal/tui`, which holds it behind an interface.

## The socket

**Path.** `/var/run/tun-manager.sock`, overridable through `feed_socket` in the
configuration. `/var/run` is cleared on reboot, which disposes of a socket left
behind by a crash for free.

**Ownership.** Created by root, then `chown`ed to the pre-sudo user's uid and
gid and set to `0600`. Only the person who started `tun-manager` can connect.
When there is no pre-sudo user — a real root login rather than `sudo` — the
socket stays root-owned, and the application will not be able to read it. That
is correct: there is no user session to serve.

**Stale sockets.** A socket file left by a killed process makes `bind` fail with
`EADDRINUSE`. The listener unlinks the path before binding. This is safe here
because two `tun-manager` processes cannot usefully coexist anyway — they would
both be driving the same tunnels.

**Shutdown.** The listener closes on context cancellation, refuses further
connections, sends `bye` to every client, and removes the socket file.

**Failure is not fatal.** If the socket cannot be created, the TUI logs it in the
log pane and carries on. Losing the menu bar must never cost you the ability to
bring a tunnel up.

## Protocol

Newline-delimited JSON, one object per line, in both directions. A line that
does not parse, or whose `type` is unknown, is ignored rather than fatal: it is
how a newer application talks to an older publisher.

### Server to client

`hello`, sent once on connect, before anything else.

```json
{"type":"hello","schema":1,"version":"v0.2.0"}
```

`schema` is the wire contract's version and changes only when an existing field
changes meaning or disappears. Adding a field does not bump it. A client that
does not recognise the schema disconnects and says so.

`state`, sent on connect right after `hello` if a view is already known, and
then on every refresh — the periodic one, the one after a batch, and the one a
client asked for.

```json
{"type":"state",
 "taken":"2026-08-17T10:00:00Z",
 "context":{"name":"office","interface":"en0","address":"198.51.100.42"},
 "tunnels":[
   {"name":"alpha","group":"needed","health":"up","device":"utun7",
    "endpoint":"192.0.2.10:51820","check_ip":"10.20.30.1",
    "last_handshake":"2026-08-17T09:59:48Z","rx_bytes":1258291,"tx_bytes":4096},
   {"name":"charlie","group":"extra","health":"down",
    "endpoint":"charlie.example:51820","rx_bytes":0,"tx_bytes":0}
 ]}
```

**This is the shape `status --json` already emits.** `internal/cli` has carried
that vocabulary since the CLI was written; a feed inventing a second dialect for
the same data would leave two things to keep in step forever. The types move to
`internal/wire`, `cli` and `feed` both build from there, and the application can
be developed against `sudo tun-manager status --json` before a single line of
socket code is read.

`health` is one of `up`, `stale`, `down`, and **it is the authority**. A tunnel
that is down still carries `rx_bytes: 0`; the consumer draws blank because the
tunnel is down, not because a field was missing. That is the rule the table
already follows, and it keeps optional fields out of the decoder.

`sample`, sent once a second for each watched tunnel.

```json
{"type":"sample","tunnel":"alpha","at":"2026-08-17T10:00:01Z","rx":1259000,"tx":4200}
```

These are the **cumulative counters**, not rates, exactly as `app.Sample`
returns them. Turning two readings into a rate is the consumer's job, and the
consumer has to do it anyway to survive a missed sample. A tunnel that is down
produces no `sample` at all.

`bye`, sent once before the publisher goes away.

```json
{"type":"bye"}
```

### Client to server

`watch` starts sampling a tunnel, `unwatch` stops it.

```json
{"type":"watch","tunnel":"alpha"}
{"type":"unwatch","tunnel":"alpha"}
```

Sampling costs a control-socket read per second per watched tunnel, which is why
it is asked for rather than always on — the same rule the TUI's `g` pane
follows. Watches are per connection: a client that disconnects stops its
watches. Watching an unknown tunnel is ignored.

`refresh` asks for a fresh view.

```json
{"type":"refresh"}
```

Without it a menu bar opened between two ticks would show state up to
`refresh_interval` old — five minutes by default. The publisher ignores a
refresh arriving within two seconds of the last one, so a client cannot turn
this into a way of hammering `wgctrl`.

## Semantics that are easy to get wrong

**Sampling is refcounted across clients, not per client.** The sampler reads the
union of every watched tunnel once a second and delivers each reading only to
the clients that asked for that tunnel. Two menu bars watching `alpha` cost one
read, not two.

**The ticker runs only while something is watched.** It starts when the watch set
becomes non-empty and stops when it empties. A pane nobody is looking at costs
nothing, which is the rule the TUI graph already follows.

**A sample needs a view first.** The sampler needs a `wgconf.Tunnel` to read, and
it gets those from the last published view. A `watch` arriving before the first
`state` is remembered and starts producing samples once a view is known.

**A slow client is dropped, not buffered.** Each connection has its own send
queue of sixteen messages. If it fills, the connection is closed. The publisher
must never block on a client: a stalled menu bar cannot be allowed to freeze the
program that manages the tunnels. Sixteen messages is several seconds of
sampling; a client that cannot keep up with one message a second is not going to
recover.

**The feed never samples the TUI's graph for it.** `internal/tui` keeps its own
sampling loop for the `g` pane, untouched. Merging them would mean one loop with
two independent open/close conditions, in the file that is already the most
complex in the repository. The duplicated read is one socket read per second.

## Go changes

### New package `internal/feed`

```go
// Sampler reads one tunnel's counters. *app.App satisfies it.
type Sampler interface {
	Sample(tun wgconf.Tunnel) (app.Sample, bool)
}

type Server struct {
	Path    string          // socket path
	Owner   privdrop.User   // who the socket is handed to
	Sampler Sampler

	// Interval between readings while a tunnel is watched; one second in
	// production, short enough in tests that nothing waits on a real clock.
	Interval time.Duration
	Now      func() time.Time
}

// Listen binds the socket and hands it to Owner. Call before Serve.
func (s *Server) Listen() error

// Serve accepts connections until ctx is cancelled, then says bye and
// removes the socket.
func (s *Server) Serve(ctx context.Context) error

// Publish fans a view out to every client. Safe from any goroutine.
func (s *Server) Publish(v app.View)

// Requests yields what clients asked for that the feed cannot do itself.
func (s *Server) Requests() <-chan Request
```

`Request` carries only `Kind: "refresh"` today. It is a type rather than a bare
channel of nothing so that a second verb does not change every signature.

### New package `internal/wire`

The JSON vocabulary, extracted from `internal/cli` where it already lives as
`jsonView` / `jsonTunnel`, and used by both `status --json` and the feed. The
extraction is a pure refactor: the bytes `status --json` produces do not change,
and its tests are the proof.

Wire types are declared there and built from `app.View`. They are not `app.View`
with tags: marshalling internal structs onto a wire makes every refactor a
breaking protocol change, and it is how a field nobody meant to publish gets
published.

### `internal/tui`

Three additions, all following patterns already in the file:

- A `Feed` interface in the model — `Publish(app.View)` — so the TUI can be
  tested against a recorder, and so `New(nil, nil)` keeps working.
- In `onView`, a `tea.Cmd` that publishes, sitting beside the notification
  command that is already there. `Update` stays pure.
- `nextRequest(<-chan feed.Request) tea.Cmd`, the same shape as the existing
  `nextEvent`, turning a refresh request into the command that `r` already runs.

### `internal/profile`

```yaml
feed: true                            # default
feed_socket: /var/run/tun-manager.sock
```

`feed` defaults to true, like `notify`. The socket is `0600` and owned by one
person; there is nothing to protect by making it opt-in, and an application that
needs configuration before it works once is an application nobody runs twice.

### `internal/notify`

Unchanged. `notify: false` already turns osascript off, through
`notify.New(u, cfg.Notify)`. Whoever runs the menu bar application sets it and
lets the application notify instead, with its own bundle icon. Nothing in the
code needs to know which of the two is doing it.

### `internal/cli`

`doctor` gains a line: whether the socket path is writable, and who it would be
handed to. It is the first thing to check when the menu bar shows nothing.

## Testing

Everything in `internal/feed` is reachable, and the plan is for no `NOT TESTED:`
marker to come out of this work.

| What | How |
|---|---|
| bind, chown, mode | socket in `t.TempDir()`, `os.Stat` the result |
| stale socket | create a file at the path first, then `Listen` |
| `hello` / `state` / `bye` | connect with `net.Dial`, read lines, compare |
| `status --json` is byte-identical after the extraction | its existing tests, unchanged |
| `watch` / `unwatch` | a fake `Sampler` counting calls |
| refcount across clients | two connections, one tunnel, one read per second |
| ticker stops | unwatch everything, assert the fake stops being called |
| watch before first view | watch, then publish, then assert samples start |
| slow client dropped | a connection that never reads, publish past the queue |
| unknown message | send garbage, assert the connection survives |
| refresh floor | two refreshes in a row, assert one request |
| TUI publishes | a recorder `Feed`, assert the command publishes the view |
| TUI honours requests | push a request, assert the refresh command comes back |

The clock is injected, so nothing waits on real time except the sampler's
interval, which is a field.

## The application, as far as this contract constrains it

Not built here. Recorded so the contract is not designed against a fantasy.

- **Not sandboxed.** A sandboxed application cannot open a unix socket outside
  its container. Developer ID signing, or unsigned for local use.
- **Reconnects with backoff.** `tun-manager` is a foreground process that comes
  and goes; that is the normal case, not an error. Absent publisher renders as a
  dimmed icon, not an alert.
- **Notifies from `state`.** It diffs successive views the way `notify.Diff`
  does. `UNUserNotificationCenter` gives it the bundle icon for free, which is
  what the `-contentImage` workaround exists to fake today.
- **Watches on demand.** `watch` when the graph popover opens, `unwatch` when it
  closes. Rates are computed from the cumulative counters it receives.

## What this costs

One new package, three small additions to the TUI, two configuration keys. No
cgo, so the build stays pure Go, cross-compilation and GoReleaser are untouched
and the coverage floor holds. The Swift application is outside the Go tree and
outside the gate.

The honest limitation, stated plainly: **the menu bar is only alive while
`sudo tun-manager` is running.** That is the price of not installing a daemon,
and it is the right price here — the program was always meant to be left open.
