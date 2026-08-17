# Status Feed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Publish tunnel state and traffic samples from the running TUI over a read-only unix socket, so a native macOS menu bar application can display status, draw graphs and raise notifications with no privilege of its own.

**Architecture:** A new `internal/wire` holds the JSON vocabulary, extracted from `internal/cli` so `status --json` and the feed cannot drift. A new `internal/feed` binds `/var/run/tun-manager.sock`, hands it to the pre-sudo user, and fans views out to connected clients; clients may ask to watch a tunnel's counters or to force a refresh, and nothing else. `internal/tui` gains a `Feed` interface it publishes to from a `tea.Cmd`, keeping `Update` pure.

**Tech Stack:** Go 1.26, standard library only (`net`, `encoding/json`, `bufio`, `os`, `sync`). No new dependency, no cgo.

**Spec:** `docs/specs/2026-08-17-status-feed-design.md` — read it before Task 1. The plan argues from it.

## Global Constraints

Copied from `AGENTS.md` and the spec. Every task's requirements implicitly include this section.

- **Everything is tested.** Write the test first, run it, watch it fail for the reason you expect, then make it pass.
- **Untested code is documented twice**: a `NOT TESTED:` comment on the code naming a section, and that `###` section under `## Deliberately untested` in `docs/coverage-gaps.md`. `make markers-check` enforces the link. **This plan aims for zero new markers.**
- **Tests are hermetic.** Socket paths, config paths and run dirs point at `t.TempDir()`. Never read what the machine happens to have.
- **Fixtures are invented.** Addresses from RFC 5737 (`192.0.2.0/24`, `198.51.100.0/24`, `203.0.113.0/24`) and RFC 3849 (`2001:db8::/32`). Tunnel names `alpha`, `bravo`, `charlie`, `delta`. Users `operator` / `/home/operator`.
- **Secrets.** `PrivateKey` and `PresharedKey` are never parsed, stored or printed. Peer public keys are not published on this wire either.
- **No control verb on the socket, ever.** No message may start or stop a tunnel. Adding one reopens every question the design avoids.
- **Git.** Never rewrite pushed history. One commit per task, on top of what exists, so the next push stays a fast-forward. Check with `git merge-base --is-ancestor origin/main HEAD`.
- **Language.** Code, comments, commit messages and docs in English US.
- **Done means:** `make all` and `make cover` both green, and you report what they printed. The floor is `COVERAGE_MIN := 99` in the `Makefile`.
- **Socket constants:** path `/var/run/tun-manager.sock`, mode `0600`, schema version `1`, send queue 16 messages, sample interval 1s, refresh floor 2s.

---

### Task 1: Extract the JSON vocabulary into `internal/wire`

A pure refactor. `internal/cli` already emits the exact shape the feed needs; moving it out means one definition instead of two that drift. `status --json` must produce byte-identical output afterwards, and its existing tests are the proof.

**Files:**
- Create: `internal/wire/wire.go`
- Create: `internal/wire/wire_test.go`
- Modify: `internal/cli/status.go:16-36` (delete `jsonTunnel`/`jsonView`), `:46-73` (`writeJSON`), `:92` and `:98-105` (`endpoint`)

**Interfaces:**
- Consumes: `app.View`, `app.Row`, `wg.Down` — all existing.
- Produces: `wire.View`, `wire.Tunnel`, `wire.Context`, `wire.Of(app.View) View`, `wire.Endpoint(app.Row) string`.

- [ ] **Step 1: Write the failing test**

`internal/wire/wire_test.go`:

```go
package wire

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

var taken = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

func TestAViewCarriesItsContextAndEveryTunnel(t *testing.T) {
	got := Of(app.View{
		Context: netctx.Context{Name: "office", Interface: "en0", Address: "198.51.100.42"},
		Taken:   taken,
		Rows: []app.Row{{
			Tunnel: wgconf.Tunnel{Name: "alpha", Endpoint: "alpha.example:51820", CheckIP: "192.0.2.1"},
			Group:  "needed",
			Health: wg.Up,
			Peer:   wg.Peer{Device: "utun7", LastHandshake: taken, RxBytes: 2048, TxBytes: 512},
		}},
	})

	if got.Context.Name != "office" || got.Context.Interface != "en0" {
		t.Errorf("context = %+v, want the one from the view", got.Context)
	}
	if len(got.Tunnels) != 1 {
		t.Fatalf("tunnels = %+v, want one", got.Tunnels)
	}
	tun := got.Tunnels[0]
	if tun.Name != "alpha" || tun.Group != "needed" || tun.Health != "up" {
		t.Errorf("tunnel = %+v, want alpha/needed/up", tun)
	}
	if tun.Device != "utun7" || tun.RxBytes != 2048 || tun.TxBytes != 512 {
		t.Errorf("tunnel = %+v, want the live counters", tun)
	}
	if tun.LastHandshake != taken.Format(time.RFC3339) {
		t.Errorf("last_handshake = %q, want RFC 3339", tun.LastHandshake)
	}
}

func TestATunnelThatNeverHandshookHasNoHandshake(t *testing.T) {
	// An empty string is omitted from the JSON; a zero timestamp would render
	// as the year 1, which reads like a real reading.
	got := Of(app.View{Rows: []app.Row{{
		Tunnel: wgconf.Tunnel{Name: "charlie"},
		Health: wg.Down,
	}}})

	if got.Tunnels[0].LastHandshake != "" {
		t.Errorf("last_handshake = %q, want nothing", got.Tunnels[0].LastHandshake)
	}
}

func TestHealthIsWhatSaysATunnelIsCarryingNothing(t *testing.T) {
	// Counters are always present, zero included: the consumer draws blank
	// because the tunnel is down, not because a field went missing.
	out, err := json.Marshal(Of(app.View{Rows: []app.Row{{
		Tunnel: wgconf.Tunnel{Name: "charlie", Endpoint: "charlie.example:51820"},
		Health: wg.Down,
	}}}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"health":"down"`, `"rx_bytes":0`, `"tx_bytes":0`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("%s missing from %s", want, out)
		}
	}
}

func TestTheLiveEndpointWinsOverTheConfiguredOne(t *testing.T) {
	// wg-quick resolves a DNS endpoint; the resolved one is what traffic uses.
	got := Endpoint(app.Row{
		Tunnel: wgconf.Tunnel{Endpoint: "alpha.example:51820"},
		Health: wg.Up,
		Peer:   wg.Peer{Endpoint: "192.0.2.10:51820"},
	})

	if got != "192.0.2.10:51820" {
		t.Errorf("endpoint = %q, want the resolved one", got)
	}
}

func TestADownTunnelKeepsItsConfiguredEndpoint(t *testing.T) {
	got := Endpoint(app.Row{
		Tunnel: wgconf.Tunnel{Endpoint: "charlie.example:51820"},
		Health: wg.Down,
		Peer:   wg.Peer{Endpoint: "192.0.2.10:51820"},
	})

	if got != "charlie.example:51820" {
		t.Errorf("endpoint = %q, want the configured one", got)
	}
}

func TestAViewWithNoRowsMarshalsAsAnEmptyListNotNull(t *testing.T) {
	// A decoder that gets null where it expects a list is a decoder that
	// crashes on a machine with no tunnels configured.
	out, err := json.Marshal(Of(app.View{}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(out), `"tunnels":[]`) {
		t.Errorf("got %s, want an empty list", out)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

```sh
go test ./internal/wire/ -run . -v
```

Expected: build failure, `undefined: Of`, `undefined: Endpoint`.

- [ ] **Step 3: Write the implementation**

`internal/wire/wire.go`:

```go
// Package wire is the JSON vocabulary tun-manager speaks to anything outside
// itself: `status --json` on one side, the status feed on the other.
//
// It exists so there is one definition rather than two. The same view rendered
// two ways by two packages is two things to keep in step, and they drift.
//
// These types are not app.View with tags. Marshalling internal structs onto a
// wire turns every refactor into a breaking protocol change, and it is how a
// field nobody meant to publish gets published.
package wire

import (
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/wg"
)

// Context is the network the machine is on.
type Context struct {
	Name      string `json:"name"`
	Interface string `json:"interface,omitempty"`
	Address   string `json:"address,omitempty"`
}

// Tunnel is one tunnel, seen from outside.
//
// Health is the authority on whether the tunnel is carrying anything. The
// counters are always present, zero included: a consumer draws blank because
// the tunnel is down, not because a field went missing.
type Tunnel struct {
	Name          string `json:"name"`
	Group         string `json:"group"`
	Health        string `json:"health"`
	Device        string `json:"device,omitempty"`
	Endpoint      string `json:"endpoint,omitempty"`
	CheckIP       string `json:"check_ip,omitempty"`
	LastHandshake string `json:"last_handshake,omitempty"`
	RxBytes       int64  `json:"rx_bytes"`
	TxBytes       int64  `json:"tx_bytes"`
}

// View is the whole picture at one instant.
type View struct {
	Context Context   `json:"context"`
	Taken   time.Time `json:"taken"`
	Tunnels []Tunnel  `json:"tunnels"`
}

// Of renders a view for the wire.
func Of(v app.View) View {
	out := View{
		Context: Context{
			Name:      v.Context.Name,
			Interface: v.Context.Interface,
			Address:   v.Context.Address,
		},
		Taken: v.Taken,
		// Made rather than declared: a nil slice marshals as null, and a
		// decoder that gets null where it expects a list is a decoder that
		// crashes on a machine with no tunnels configured.
		Tunnels: make([]Tunnel, 0, len(v.Rows)),
	}

	for _, r := range v.Rows {
		t := Tunnel{
			Name:     r.Tunnel.Name,
			Group:    r.Group,
			Health:   r.Health.String(),
			Device:   r.Peer.Device,
			Endpoint: Endpoint(r),
			CheckIP:  r.Tunnel.CheckIP,
			RxBytes:  r.Peer.RxBytes,
			TxBytes:  r.Peer.TxBytes,
		}
		if !r.Peer.LastHandshake.IsZero() {
			t.LastHandshake = r.Peer.LastHandshake.Format(time.RFC3339)
		}
		out.Tunnels = append(out.Tunnels, t)
	}
	return out
}

// Endpoint prefers the live endpoint, which is the resolved one, and falls back
// to the configured one for tunnels that are down.
func Endpoint(r app.Row) string {
	if r.Health != wg.Down && r.Peer.Endpoint != "" {
		return r.Peer.Endpoint
	}
	return r.Tunnel.Endpoint
}
```

- [ ] **Step 4: Run the test and watch it pass**

```sh
go test ./internal/wire/ -v
```

Expected: PASS, six tests.

- [ ] **Step 5: Point `internal/cli` at the new package**

In `internal/cli/status.go`: delete the `jsonTunnel` and `jsonView` declarations and the `endpoint` function, add `"ledez.net/tun-manager/internal/wire"` to the imports, drop the now-unused `"time"` and `"ledez.net/tun-manager/internal/wg"` imports, and replace `writeJSON`:

```go
func writeJSON(w io.Writer, view app.View) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(wire.Of(view))
}
```

In `writeTable`, the last column becomes `format.OrNone(wire.Endpoint(r))`.

- [ ] **Step 6: Prove `status --json` did not move**

```sh
go test ./internal/cli/ -v
```

Expected: PASS, unchanged. **These tests are the whole safety net for this task — if any of them needed editing, the refactor was not pure and you changed the contract. Stop and re-read the diff.**

- [ ] **Step 7: Full gate**

```sh
make all && make cover
```

Expected: green, total at or above 99%.

- [ ] **Step 8: Commit**

```sh
git add internal/wire internal/cli/status.go
git commit -m "refactor: give status --json and the coming feed one JSON vocabulary

internal/cli carried the JSON shape of a view, and it is the same data
the status feed has to publish. Two definitions of one thing drift; this
moves them to internal/wire so there is one.

Pure refactor: status --json produces the same bytes, and its tests are
unchanged, which is the proof.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: The feed's message types

The five envelopes that travel on the socket. Pure data and pure functions, so they lock the contract down before any socket exists.

**Files:**
- Create: `internal/feed/message.go`
- Create: `internal/feed/message_test.go`

**Interfaces:**
- Consumes: `wire.View`, `wire.Of` (Task 1).
- Produces: `feed.Schema` (const `1`), and the unexported `helloMsg`, `stateMsg`, `sampleMsg`, `byeMsg`, `clientMsg` used by every later task in this package.

- [ ] **Step 1: Write the failing test**

`internal/feed/message_test.go`:

```go
package feed

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
	"ledez.net/tun-manager/internal/wire"
)

var taken = time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)

func TestHelloAnnouncesTheSchemaAndTheVersion(t *testing.T) {
	// A client that does not know the schema has to be able to say so before
	// it misreads a field that changed meaning.
	out, err := json.Marshal(helloMsg{Type: "hello", Schema: Schema, Version: "v0.2.0"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if got := string(out); got != `{"type":"hello","schema":1,"version":"v0.2.0"}` {
		t.Errorf("hello = %s", got)
	}
}

func TestStateCarriesTheViewFlatBesideItsType(t *testing.T) {
	// The view's fields sit at the top level rather than under a "view" key:
	// one object per line, and the type is just another field of it.
	out, err := json.Marshal(stateMsg{Type: "state", View: wire.Of(app.View{
		Taken: taken,
		Rows: []app.Row{{
			Tunnel: wgconf.Tunnel{Name: "alpha"},
			Health: wg.Up,
			Peer:   wg.Peer{Device: "utun7", RxBytes: 2048},
		}},
	})})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	got := string(out)
	for _, want := range []string{`"type":"state"`, `"tunnels":[`, `"name":"alpha"`, `"rx_bytes":2048`} {
		if !strings.Contains(got, want) {
			t.Errorf("%s missing from %s", want, got)
		}
	}
	if strings.Contains(got, `"View"`) {
		t.Errorf("the view is nested rather than flat: %s", got)
	}
}

func TestASampleCarriesCumulativeCountersNotARate(t *testing.T) {
	// Rates are the consumer's job: it has to compute them anyway to survive a
	// sample that never arrived.
	out, err := json.Marshal(sampleMsg{
		Type: "sample", Tunnel: "alpha", At: taken, Rx: 1259000, Tx: 4200,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	want := `{"type":"sample","tunnel":"alpha","at":"2026-08-17T10:00:00Z","rx":1259000,"tx":4200}`
	if got := string(out); got != want {
		t.Errorf("sample  = %s\nwant    = %s", got, want)
	}
}

func TestByeIsJustItsType(t *testing.T) {
	out, err := json.Marshal(byeMsg{Type: "bye"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	if got := string(out); got != `{"type":"bye"}` {
		t.Errorf("bye = %s", got)
	}
}

func TestAClientMessageCarriesAtMostATunnel(t *testing.T) {
	var m clientMsg
	if err := json.Unmarshal([]byte(`{"type":"watch","tunnel":"alpha"}`), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Type != "watch" || m.Tunnel != "alpha" {
		t.Errorf("message = %+v, want a watch on alpha", m)
	}

	var refresh clientMsg
	if err := json.Unmarshal([]byte(`{"type":"refresh"}`), &refresh); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if refresh.Type != "refresh" || refresh.Tunnel != "" {
		t.Errorf("message = %+v, want a bare refresh", refresh)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

```sh
go test ./internal/feed/ -v
```

Expected: build failure, `undefined: helloMsg`, `undefined: Schema`.

- [ ] **Step 3: Write the implementation**

`internal/feed/message.go`:

```go
// Package feed publishes tunnel state over a unix socket, so that a program
// with no privileges can show it.
//
// Everything here is read-only by construction: no message a client can send
// starts or stops a tunnel. That is what keeps the socket cheap to reason
// about — there is no authorisation to design, because there is nothing to
// authorise.
package feed

import (
	"time"

	"ledez.net/tun-manager/internal/wire"
)

// Schema is the version of the wire contract. It changes only when an existing
// field changes meaning or disappears; adding a field does not bump it, because
// a client that has never heard of a field ignores it.
const Schema = 1

// helloMsg is the first line on every connection.
type helloMsg struct {
	Type    string `json:"type"`
	Schema  int    `json:"schema"`
	Version string `json:"version"`
}

// stateMsg is a whole view. The view is embedded rather than nested: one JSON
// object per line, with the type as one more field of it.
type stateMsg struct {
	Type string `json:"type"`
	wire.View
}

// sampleMsg is one reading of a watched tunnel's cumulative counters.
type sampleMsg struct {
	Type   string    `json:"type"`
	Tunnel string    `json:"tunnel"`
	At     time.Time `json:"at"`
	Rx     int64     `json:"rx"`
	Tx     int64     `json:"tx"`
}

// byeMsg is the last line before the publisher goes away, so that a client can
// tell a shutdown from a crash.
type byeMsg struct {
	Type string `json:"type"`
}

// clientMsg is anything a client sends. There is one shape for all of them:
// the vocabulary is three verbs wide and will not grow a payload.
type clientMsg struct {
	Type   string `json:"type"`
	Tunnel string `json:"tunnel,omitempty"`
}
```

- [ ] **Step 4: Run the test and watch it pass**

```sh
go test ./internal/feed/ -v
```

Expected: PASS, five tests.

- [ ] **Step 5: Commit**

```sh
git add internal/feed
git commit -m "feat(feed): declare the messages that travel on the status socket

Five envelopes, locked down before any socket exists: hello carries the
schema so a client can refuse a contract it does not know, state carries
a whole view flat beside its type, sample carries cumulative counters
rather than rates, bye separates a shutdown from a crash, and one client
shape covers the three verbs a client is allowed.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: The listener — bind, hand over, clean up

The socket's whole lifecycle, with no clients yet. Root creates it and gives it away; only the person who ran `sudo tun-manager` can connect.

**Files:**
- Create: `internal/feed/server.go`
- Create: `internal/feed/server_test.go`

**Interfaces:**
- Consumes: `privdrop.User` (fields `Username`, `UID`, `GID`, `Demotable`), `app.Sample`, `wgconf.Tunnel`.
- Produces: `feed.Sampler` interface, `feed.Server` struct with fields `Path`, `Owner`, `Sampler`, `Version`, `Interval`, `Now`; methods `Listen() error` and `Close() error`; constants `SocketMode`, `sendQueue`, `sampleInterval`, `refreshFloor`.

- [ ] **Step 1: Write the failing test**

`internal/feed/server_test.go`:

```go
package feed

import (
	"os"
	"path/filepath"
	"testing"

	"ledez.net/tun-manager/internal/privdrop"
)

// socketPath returns a path in a temporary directory. A unix socket path is
// limited to about a hundred bytes, and a long TMPDIR overflows it, so the
// directory is kept shallow.
func socketPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "f.sock")
}

func TestListenCreatesTheSocketReadableByNobodyElse(t *testing.T) {
	// The socket carries what tunnels exist and where they connect to. It is
	// for one person: whoever started the program.
	s := &Server{Path: socketPath(t)}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer s.Close()

	info, err := os.Stat(s.Path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != SocketMode {
		t.Errorf("mode = %o, want %o", got, SocketMode)
	}
	if info.Mode()&os.ModeSocket == 0 {
		t.Errorf("mode = %v, want a socket", info.Mode())
	}
}

func TestTheSocketIsHandedToThePreSudoUser(t *testing.T) {
	// Root creates it and the user's session has to open it. Chowning to the
	// current uid is a real chown that an unprivileged test can make.
	s := &Server{
		Path: socketPath(t),
		Owner: privdrop.User{
			Username: "operator", UID: os.Getuid(), GID: os.Getgid(), Demotable: true,
		},
	}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer s.Close()
}

func TestHandingTheSocketOverIsFatalWhenItFails(t *testing.T) {
	// Leaving a root-owned socket behind would look like it worked and then
	// serve nobody.
	s := &Server{
		Path:  socketPath(t),
		Owner: privdrop.User{Username: "root", UID: 0, GID: 0, Demotable: true},
	}

	err := s.Listen()

	if err == nil {
		s.Close()
		t.Fatal("Listen succeeded while chowning to root as an ordinary user")
	}
	if _, statErr := os.Stat(s.Path); !os.IsNotExist(statErr) {
		t.Errorf("the socket outlived a failed Listen")
	}
}

func TestASocketWithNoOneToHandItToStaysWhereItIs(t *testing.T) {
	// A real root login rather than sudo: there is no user session to serve,
	// so the socket stays root-owned rather than failing to start.
	s := &Server{Path: socketPath(t), Owner: privdrop.User{Demotable: false, UID: 0}}

	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	s.Close()
}

func TestAStaleSocketIsReplacedRatherThanRefused(t *testing.T) {
	// A killed process leaves the file behind and bind fails with EADDRINUSE.
	// Two tun-managers cannot usefully coexist anyway, so the path is ours.
	path := socketPath(t)
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	s := &Server{Path: path}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen over a stale socket: %v", err)
	}
	s.Close()
}

func TestListenReportsAPathItCannotBind(t *testing.T) {
	s := &Server{Path: filepath.Join(t.TempDir(), "no-such-dir", "f.sock")}

	if err := s.Listen(); err == nil {
		s.Close()
		t.Fatal("Listen succeeded on a path with no directory")
	}
}

func TestCloseRemovesTheSocket(t *testing.T) {
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Errorf("stat after Close = %v, want the socket gone", err)
	}
}

func TestClosingAServerThatNeverListenedIsHarmless(t *testing.T) {
	// The composition root closes whatever it built, including on the path
	// where Listen failed.
	if err := (&Server{Path: "/nonexistent"}).Close(); err != nil {
		t.Errorf("Close = %v, want nothing to do", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	_ = s.Close()
	if err := s.Close(); err != nil {
		t.Errorf("second Close = %v, want nothing to do", err)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

```sh
go test ./internal/feed/ -run 'Listen|Socket|Close|Stale|Handing' -v
```

Expected: build failure, `undefined: Server`, `undefined: SocketMode`.

- [ ] **Step 3: Write the implementation**

`internal/feed/server.go`:

```go
package feed

import (
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/wgconf"
)

const (
	// SocketMode is what the socket is chmodded to. The feed says which
	// tunnels exist and where they connect; it is for one person.
	SocketMode os.FileMode = 0o600

	// sendQueue is how many messages a client may fall behind by. Sixteen is
	// several seconds of sampling: a client that cannot keep up with one
	// message a second is not going to recover.
	sendQueue = 16

	// sampleInterval is how often a watched tunnel's counters are read.
	sampleInterval = time.Second

	// refreshFloor is the shortest gap between two refreshes a client can ask
	// for. Without it a client could turn the menu bar into a way of hammering
	// the WireGuard control socket.
	refreshFloor = 2 * time.Second
)

// Sampler reads one tunnel's cumulative counters. *app.App satisfies it.
type Sampler interface {
	Sample(tun wgconf.Tunnel) (app.Sample, bool)
}

// Request is something a client asked for that the feed cannot do itself.
type Request struct{ Kind string }

// RequestRefresh asks whoever owns the refresh to take a fresh view.
const RequestRefresh = "refresh"

// Server publishes views and samples to whoever connects.
//
// The zero value is not usable: Path is required, and Listen must be called
// before Serve.
type Server struct {
	// Path is where the socket is bound.
	Path string
	// Owner is who the socket is handed to. When it is not demotable the
	// socket stays root-owned: there is no user session to serve.
	Owner privdrop.User
	// Sampler reads counters for watched tunnels.
	Sampler Sampler
	// Version is reported in the hello line.
	Version string

	// Interval between readings while a tunnel is watched. Zero means
	// sampleInterval; tests set it short so nothing waits on a real clock.
	Interval time.Duration
	// Now is the clock the refresh floor reads. Zero means time.Now.
	Now func() time.Time

	ln net.Listener

	mu     sync.Mutex
	closed bool
}

func (s *Server) interval() time.Duration {
	if s.Interval > 0 {
		return s.Interval
	}
	return sampleInterval
}

func (s *Server) clock() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Listen binds the socket and hands it to Owner. Call it before Serve.
//
// Any failure after the bind removes the socket again: a path left behind
// would look like a feed that works and serve nobody.
func (s *Server) Listen() error {
	// A socket left by a killed process makes bind fail with EADDRINUSE. Two
	// tun-manager processes cannot usefully coexist - they would both be
	// driving the same tunnels - so the path is ours to take.
	if err := os.Remove(s.Path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale socket %s: %w", s.Path, err)
	}

	ln, err := net.Listen("unix", s.Path)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.Path, err)
	}

	if err := os.Chmod(s.Path, SocketMode); err != nil {
		return s.abandon(ln, fmt.Errorf("chmod %s: %w", s.Path, err))
	}
	if s.Owner.Demotable {
		if err := os.Chown(s.Path, s.Owner.UID, s.Owner.GID); err != nil {
			return s.abandon(ln, fmt.Errorf("hand %s to %s: %w", s.Path, s.Owner.Username, err))
		}
	}

	s.ln = ln
	return nil
}

// abandon undoes a half-built listener and returns the reason it was given.
func (s *Server) abandon(ln net.Listener, cause error) error {
	ln.Close()
	os.Remove(s.Path)
	return cause
}

// Close stops listening and removes the socket. It is safe to call on a server
// that never listened, and safe to call twice.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed || s.ln == nil {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ln := s.ln
	s.mu.Unlock()

	err := ln.Close()
	if rmErr := os.Remove(s.Path); rmErr != nil && !os.IsNotExist(rmErr) && err == nil {
		err = rmErr
	}
	return err
}
```

- [ ] **Step 4: Run the test and watch it pass**

```sh
go test ./internal/feed/ -v
```

Expected: PASS, all of Task 2's and Task 3's tests.

- [ ] **Step 5: Commit**

```sh
git add internal/feed
git commit -m "feat(feed): bind the status socket and hand it to the real user

Root creates the socket, so it is root-owned until it is given away, and
the session that has to open it is the pre-sudo user's. Same handover
internal/privdrop already does for files.

A failed handover removes the socket rather than leaving it: a
root-owned path left behind looks like a feed that works and serves
nobody. A stale path from a killed process is taken over rather than
refused - two tun-managers cannot usefully coexist anyway.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Serve — accept, hello, state, bye

Clients connect, are greeted, receive every view published from then on, and are told when the publisher goes away.

**Files:**
- Modify: `internal/feed/server.go` (add the client type, `Serve`, `Publish`, `add`, `read`, `sendTo`, `drop`, `shutdown`)
- Create: `internal/feed/serve_test.go`

**Interfaces:**
- Consumes: everything from Tasks 2 and 3.
- Produces: `(*Server).Serve(ctx context.Context) error`, `(*Server).Publish(v app.View)`.

- [ ] **Step 1: Write the failing test**

`internal/feed/serve_test.go`:

```go
package feed

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

// serving starts a server on a temporary socket and stops it when the test
// ends. The interval is short so no test waits on a real second.
func serving(t *testing.T, sampler Sampler) *Server {
	t.Helper()

	s := &Server{
		Path:     socketPath(t),
		Sampler:  sampler,
		Version:  "v0.0.0-test",
		Interval: 5 * time.Millisecond,
	}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after the context was cancelled")
		}
	})
	return s
}

// conn is a client connection with a line reader on it.
type conn struct {
	net.Conn
	lines *bufio.Scanner
}

func dial(t *testing.T, s *Server) *conn {
	t.Helper()

	c, err := net.Dial("unix", s.Path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })

	// Every read is bounded: a test that hangs waiting for a line nobody sends
	// is a test that has to be killed by the suite timeout.
	if err := c.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatalf("deadline: %v", err)
	}
	return &conn{Conn: c, lines: bufio.NewScanner(c)}
}

// next reads one message and returns it decoded into a map, which is what a
// consumer in another language sees.
func (c *conn) next(t *testing.T) map[string]any {
	t.Helper()

	if !c.lines.Scan() {
		t.Fatalf("no line: %v", c.lines.Err())
	}
	var msg map[string]any
	if err := json.Unmarshal(c.lines.Bytes(), &msg); err != nil {
		t.Fatalf("decode %q: %v", c.lines.Bytes(), err)
	}
	return msg
}

func (c *conn) send(t *testing.T, line string) {
	t.Helper()

	if _, err := c.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write %s: %v", line, err)
	}
}

func aView(names ...string) app.View {
	v := app.View{Taken: taken}
	for _, n := range names {
		v.Rows = append(v.Rows, app.Row{
			Tunnel: wgconf.Tunnel{Name: n, Endpoint: n + ".example:51820"},
			Health: wg.Up,
			Peer:   wg.Peer{Device: "utun7", LastHandshake: taken},
		})
	}
	return v
}

func TestAClientIsGreetedWithTheSchemaItMustUnderstand(t *testing.T) {
	c := dial(t, serving(t, nil))

	msg := c.next(t)

	if msg["type"] != "hello" {
		t.Fatalf("first line = %v, want hello", msg)
	}
	if msg["schema"] != float64(Schema) {
		t.Errorf("schema = %v, want %d", msg["schema"], Schema)
	}
	if msg["version"] != "v0.0.0-test" {
		t.Errorf("version = %v, want the server's", msg["version"])
	}
}

func TestAPublishedViewReachesEveryClient(t *testing.T) {
	s := serving(t, nil)
	first, second := dial(t, s), dial(t, s)
	first.next(t)
	second.next(t)

	s.Publish(aView("alpha"))

	for i, c := range []*conn{first, second} {
		msg := c.next(t)
		if msg["type"] != "state" {
			t.Fatalf("client %d got %v, want state", i, msg)
		}
		tunnels, _ := msg["tunnels"].([]any)
		if len(tunnels) != 1 {
			t.Errorf("client %d got %v, want one tunnel", i, msg["tunnels"])
		}
	}
}

func TestAClientArrivingLateIsToldWhatIsAlreadyKnown(t *testing.T) {
	// A menu bar opened after the program started must not sit blank until the
	// next refresh, which is five minutes away by default.
	s := serving(t, nil)
	s.Publish(aView("alpha", "bravo"))

	c := dial(t, s)

	if msg := c.next(t); msg["type"] != "hello" {
		t.Fatalf("first line = %v, want hello", msg)
	}
	msg := c.next(t)
	if msg["type"] != "state" {
		t.Fatalf("second line = %v, want the state already known", msg)
	}
	if tunnels, _ := msg["tunnels"].([]any); len(tunnels) != 2 {
		t.Errorf("tunnels = %v, want both", msg["tunnels"])
	}
}

func TestAClientArrivingBeforeAnyViewGetsOnlyHello(t *testing.T) {
	// There is nothing to say yet, and an empty state would read as "no
	// tunnels" rather than "not known yet".
	s := serving(t, nil)
	c := dial(t, s)
	c.next(t)

	s.Publish(aView("alpha"))

	if msg := c.next(t); msg["type"] != "state" {
		t.Errorf("second line = %v, want the first real state", msg)
	}
}

func TestShuttingDownSaysGoodbye(t *testing.T) {
	// A client has to tell a publisher that quit from one that crashed: one is
	// "tun-manager is not running", the other is worth retrying immediately.
	s := &Server{Path: socketPath(t), Interval: 5 * time.Millisecond}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()

	c := dial(t, s)
	c.next(t)
	cancel()

	if msg := c.next(t); msg["type"] != "bye" {
		t.Errorf("last line = %v, want bye", msg)
	}
	if err := <-done; err != nil {
		t.Errorf("Serve: %v", err)
	}
}

func TestServeRemovesTheSocketOnItsWayOut(t *testing.T) {
	s := &Server{Path: socketPath(t)}
	if err := s.Listen(); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	dial(t, s)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}

	if _, err := net.Dial("unix", s.Path); err == nil {
		t.Error("the socket still accepts connections after Serve returned")
	}
}

func TestServeWithoutListenIsAnError(t *testing.T) {
	// Rather than a nil dereference three frames deep.
	if err := (&Server{Path: "/nonexistent"}).Serve(context.Background()); err == nil {
		t.Error("Serve without Listen succeeded")
	}
}

func TestALineThatMakesNoSenseDoesNotEndTheConnection(t *testing.T) {
	// It is how a newer application talks to an older publisher, not an attack.
	s := serving(t, nil)
	c := dial(t, s)
	c.next(t)

	c.send(t, "this is not json")
	c.send(t, `{"type":"nonsense"}`)
	s.Publish(aView("alpha"))

	if msg := c.next(t); msg["type"] != "state" {
		t.Errorf("got %v, want the connection still serving", msg)
	}
}

func TestAClientThatHangsUpIsForgotten(t *testing.T) {
	// Publishing to a closed connection must not wedge the publisher.
	s := serving(t, nil)
	c := dial(t, s)
	c.next(t)
	c.Close()

	// Two publishes: the first discovers the connection is gone, the second
	// proves the server carried on.
	s.Publish(aView("alpha"))
	s.Publish(aView("alpha", "bravo"))

	other := dial(t, s)
	if msg := other.next(t); msg["type"] != "hello" {
		t.Errorf("got %v, want the server still accepting", msg)
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

```sh
go test ./internal/feed/ -run 'Client|Publish|Shutting|Serve|Line' -v
```

Expected: build failure, `undefined: Serve`, `undefined: Publish`.

- [ ] **Step 3: Write the implementation**

Add to `internal/feed/server.go`. Extend the `Server` struct's guarded block, then add the rest:

```go
	mu       sync.Mutex
	closed   bool
	clients  map[*client]struct{}
	view     app.View
	haveView bool
```

```go
// client is one connection, with the queue its writer drains.
//
// A client's watch set lives under the server's mutex rather than one of its
// own: the sampler reads every client's set at once, and a second lock ordering
// is a second thing to get wrong.
type client struct {
	conn  net.Conn
	out   chan any
	watch map[string]bool
}

// Serve accepts connections until ctx is cancelled, then says goodbye to
// everyone and removes the socket.
func (s *Server) Serve(ctx context.Context) error {
	if s.ln == nil {
		return fmt.Errorf("feed: Serve on %s before Listen", s.Path)
	}

	// Closing the listener is what unblocks Accept; there is no deadline on it.
	go func() {
		<-ctx.Done()
		s.shutdown()
	}()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept on %s: %w", s.Path, err)
		}
		s.add(conn)
	}
}

// Publish fans a view out to every client and remembers it for whoever
// connects next. Safe to call from any goroutine.
func (s *Server) Publish(v app.View) {
	s.mu.Lock()
	s.view, s.haveView = v, true
	msg := stateMsg{Type: "state", View: wire.Of(v)}
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()

	for _, c := range clients {
		s.sendTo(c, msg)
	}
}

func (s *Server) add(conn net.Conn) {
	c := &client{conn: conn, out: make(chan any, sendQueue), watch: map[string]bool{}}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		conn.Close()
		return
	}
	if s.clients == nil {
		s.clients = map[*client]struct{}{}
	}
	s.clients[c] = struct{}{}
	view, have := s.view, s.haveView
	s.mu.Unlock()

	go c.write()
	go s.read(c)

	s.sendTo(c, helloMsg{Type: "hello", Schema: Schema, Version: s.Version})
	if have {
		// Whoever connects between two refreshes must not sit blank until the
		// next one, which is five minutes away by default.
		s.sendTo(c, stateMsg{Type: "state", View: wire.Of(view)})
	}
}

// write drains the queue onto the connection. Encode appends a newline, which
// is the framing.
func (c *client) write() {
	defer c.conn.Close()

	enc := json.NewEncoder(c.conn)
	for msg := range c.out {
		if err := enc.Encode(msg); err != nil {
			return
		}
	}
}

// read turns each line a client sends into an action. A line that does not
// parse, or whose type is unknown, is ignored: it is how a newer application
// talks to an older publisher.
func (s *Server) read(c *client) {
	defer s.drop(c)

	lines := bufio.NewScanner(c.conn)
	for lines.Scan() {
		var msg clientMsg
		if err := json.Unmarshal(lines.Bytes(), &msg); err != nil {
			continue
		}
		s.onMessage(c, msg)
	}
}

// onMessage grows in Tasks 6 and 7. Nothing here can act on a tunnel.
func (s *Server) onMessage(c *client, msg clientMsg) {}

// sendTo queues one message, dropping the client if it has fallen too far
// behind.
//
// The queue is only ever closed by whoever removed the client from s.clients
// while holding the mutex, so a send that finds the client live cannot land on
// a closed channel.
func (s *Server) sendTo(c *client, msg any) {
	s.mu.Lock()
	if _, live := s.clients[c]; !live {
		s.mu.Unlock()
		return
	}
	select {
	case c.out <- msg:
		s.mu.Unlock()
		return
	default:
	}
	s.mu.Unlock()

	s.drop(c)
}

// drop forgets a client and lets its writer finish.
func (s *Server) drop(c *client) {
	s.mu.Lock()
	if _, live := s.clients[c]; !live {
		s.mu.Unlock()
		return
	}
	delete(s.clients, c)
	s.mu.Unlock()

	close(c.out)
	c.conn.Close()
}

// shutdown says goodbye to everyone, stops listening and removes the socket.
func (s *Server) shutdown() {
	s.mu.Lock()
	clients := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	s.clients = map[*client]struct{}{}
	s.mu.Unlock()

	for _, c := range clients {
		// Queued rather than written: the writer owns the connection, and the
		// bye goes out behind whatever it has not flushed yet.
		select {
		case c.out <- byeMsg{Type: "bye"}:
		default:
		}
		close(c.out)
	}

	s.Close()
}
```

Add `"bufio"`, `"context"` and `"encoding/json"` to the imports, plus `"ledez.net/tun-manager/internal/wire"`.

- [ ] **Step 4: Run the test and watch it pass**

```sh
go test ./internal/feed/ -race -count=2 -v
```

Expected: PASS. `-race` and `-count=2` are not optional here: this is the first task with goroutines, and a data race or a leak that only shows on the second run is exactly what this catches.

- [ ] **Step 5: Commit**

```sh
git add internal/feed
git commit -m "feat(feed): greet clients, fan views out, say goodbye

A client that connects between two refreshes is told what is already
known rather than sitting blank for the five minutes until the next one.
A client that hangs up is forgotten on the next publish rather than
wedging the publisher.

Goodbye matters: it is how the menu bar tells a tun-manager that quit
from one that crashed, and only one of those is worth retrying at once.

A line that does not parse is ignored rather than fatal. It is how a
newer application talks to an older publisher.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: A slow client is dropped, never buffered

The publisher must never wait on a consumer. A stalled menu bar cannot be allowed to freeze the program that manages the tunnels.

**Files:**
- Create: `internal/feed/backpressure_test.go`
- Modify: `internal/feed/server.go` only if the test finds the policy is not already right

**Interfaces:**
- Consumes: `sendQueue`, `(*Server).Publish`, `(*Server).sendTo`, `(*Server).drop` (Tasks 3 and 4).
- Produces: nothing new. This task is a behaviour lock, not an API.

- [ ] **Step 1: Write the failing test**

`internal/feed/backpressure_test.go`:

```go
package feed

import (
	"net"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
)

func TestAClientThatNeverReadsIsDroppedRatherThanWaitedFor(t *testing.T) {
	// The program that manages the tunnels must not block on the program that
	// draws them. Sixteen messages is several seconds of sampling; a client
	// further behind than that is not coming back.
	s := serving(t, nil)

	silent, err := net.Dial("unix", s.Path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer silent.Close()

	// Far past the queue, and each Publish must return promptly.
	done := make(chan struct{})
	go func() {
		for range sendQueue * 4 {
			s.Publish(aView("alpha"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish blocked on a client that never reads")
	}

	if got := s.clientCount(); got != 0 {
		t.Errorf("clients = %d, want the silent one dropped", got)
	}
}

func TestDroppingOneClientLeavesTheOthersServed(t *testing.T) {
	s := serving(t, nil)

	silent, err := net.Dial("unix", s.Path)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer silent.Close()

	attentive := dial(t, s)
	attentive.next(t)

	for range sendQueue * 4 {
		s.Publish(aView("alpha"))
	}

	if msg := attentive.next(t); msg["type"] != "state" {
		t.Errorf("got %v, want the attentive client still served", msg)
	}
}

func TestPublishingWithNobodyConnectedIsHarmless(t *testing.T) {
	s := serving(t, nil)

	s.Publish(aView("alpha"))

	if got := s.clientCount(); got != 0 {
		t.Errorf("clients = %d, want none", got)
	}
}

func TestAViewPublishedBeforeServeIsStillRemembered(t *testing.T) {
	// Publish is safe on a server whose Serve has not started: the composition
	// root starts them in the order it likes.
	s := &Server{Path: socketPath(t)}

	s.Publish(app.View{})

	if !s.haveView {
		t.Error("the view was not remembered")
	}
}
```

- [ ] **Step 2: Add the test-only accessor and run the test**

`clientCount` is a helper the tests need and nothing else does, so it lives beside the code it reads, in `internal/feed/server.go`:

```go
// clientCount reports how many clients are connected. It exists for the tests:
// backpressure is only observable as a client that is no longer there.
func (s *Server) clientCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients)
}
```

```sh
go test ./internal/feed/ -race -run 'Slow|Dropped|Drop|Publishing|Remembered' -v
```

Expected: PASS. The policy was built in Task 4; this task is what proves it and what stops someone "fixing" it later into a blocking send.

If it FAILS — if `Publish` blocks, or the client survives — the bug is in `sendTo`: the send must be non-blocking with a `default` that drops, never a bare `c.out <- msg`.

- [ ] **Step 3: Full gate**

```sh
make all && make cover
```

- [ ] **Step 4: Commit**

```sh
git add internal/feed
git commit -m "test(feed): pin the policy that a slow client is dropped

Sixteen queued messages is several seconds of sampling. A client further
behind than that is not recovering, and the alternative to dropping it is
that the program which manages the tunnels waits on the program that
draws them.

Written as a test rather than left implicit so that a later blocking send
fails here instead of in front of somebody whose VPN will not come up.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Watch, unwatch, and the sampling loop

Counters are read once a second per watched tunnel, for as long as somebody is looking — the rule the TUI's `g` pane already follows.

**Files:**
- Modify: `internal/feed/server.go` (`onMessage`, the sampling loop, `drop`)
- Create: `internal/feed/sample_test.go`

**Interfaces:**
- Consumes: `Sampler` (Task 3), `clientMsg` (Task 2), `app.View.Row(string) (app.Row, bool)`.
- Produces: no exported API. Behaviour: `{"type":"watch","tunnel":"…"}` starts `sample` messages, `{"type":"unwatch","tunnel":"…"}` stops them.

- [ ] **Step 1: Write the failing test**

`internal/feed/sample_test.go`:

```go
package feed

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/wgconf"
)

// countingSampler answers every reading and records who was asked.
type countingSampler struct {
	reads atomic.Int64

	mu   sync.Mutex
	seen map[string]int
}

func newSampler() *countingSampler {
	return &countingSampler{seen: map[string]int{}}
}

func (c *countingSampler) Sample(tun wgconf.Tunnel) (app.Sample, bool) {
	n := c.reads.Add(1)
	c.mu.Lock()
	c.seen[tun.Name]++
	c.mu.Unlock()
	return app.Sample{At: taken, Rx: 1000 + n, Tx: 500 + n}, true
}

func (c *countingSampler) count(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.seen[name]
}

// deadSampler is a tunnel that is down: no counters to read.
type deadSampler struct{}

func (deadSampler) Sample(wgconf.Tunnel) (app.Sample, bool) { return app.Sample{}, false }

// eventually waits for a condition rather than for a duration.
func eventually(t *testing.T, why string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

func TestWatchingATunnelStartsItsSamples(t *testing.T) {
	s := serving(t, newSampler())
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t) // hello
	c.next(t) // state

	c.send(t, `{"type":"watch","tunnel":"alpha"}`)

	msg := c.next(t)
	if msg["type"] != "sample" {
		t.Fatalf("got %v, want a sample", msg)
	}
	if msg["tunnel"] != "alpha" {
		t.Errorf("tunnel = %v, want alpha", msg["tunnel"])
	}
	if _, ok := msg["rx"]; !ok {
		t.Errorf("sample = %v, want a counter", msg)
	}
}

func TestNothingIsSampledUntilSomebodyWatches(t *testing.T) {
	// A reading a second for a graph nobody is looking at is a reading wasted.
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	dial(t, s)

	time.Sleep(50 * time.Millisecond) // ten intervals

	if got := sampler.reads.Load(); got != 0 {
		t.Errorf("reads = %d, want none with nobody watching", got)
	}
}

func TestUnwatchingStopsTheReadings(t *testing.T) {
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)
	c.send(t, `{"type":"watch","tunnel":"alpha"}`)
	eventually(t, "the first reading", func() bool { return sampler.reads.Load() > 0 })

	c.send(t, `{"type":"unwatch","tunnel":"alpha"}`)

	// Let whatever was already in flight land, then hold still.
	time.Sleep(50 * time.Millisecond)
	settled := sampler.reads.Load()
	time.Sleep(50 * time.Millisecond)
	if got := sampler.reads.Load(); got != settled {
		t.Errorf("reads went %d -> %d, want the loop stopped", settled, got)
	}
}

func TestTwoClientsWatchingOneTunnelCostOneReading(t *testing.T) {
	// The union is sampled, not each client's list: two menu bars on the same
	// tunnel are one control-socket read, not two.
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))

	for _, c := range []*conn{dial(t, s), dial(t, s)} {
		c.next(t)
		c.next(t)
		c.send(t, `{"type":"watch","tunnel":"alpha"}`)
	}

	eventually(t, "several rounds of sampling", func() bool { return sampler.count("alpha") >= 4 })

	// Every read went to alpha and nowhere else; the count above is rounds,
	// not clients.
	if got := sampler.count("bravo"); got != 0 {
		t.Errorf("bravo was read %d time(s), want none", got)
	}
}

func TestEachClientOnlyHearsAboutWhatItWatches(t *testing.T) {
	s := serving(t, newSampler())
	s.Publish(aView("alpha", "bravo"))

	watcher := dial(t, s)
	watcher.next(t)
	watcher.next(t)
	watcher.send(t, `{"type":"watch","tunnel":"alpha"}`)

	bystander := dial(t, s)
	bystander.next(t)
	bystander.next(t)

	if msg := watcher.next(t); msg["tunnel"] != "alpha" {
		t.Errorf("watcher got %v, want alpha", msg)
	}

	// The bystander asked for nothing, so the next thing it hears is a state.
	s.Publish(aView("alpha", "bravo"))
	if msg := bystander.next(t); msg["type"] != "state" {
		t.Errorf("bystander got %v, want no samples it never asked for", msg)
	}
}

func TestAWatchSurvivesUntilAViewNamesTheTunnel(t *testing.T) {
	// A client may watch before the first view lands. The watch waits rather
	// than being thrown away, or the menu bar's first graph is always empty.
	sampler := newSampler()
	s := serving(t, sampler)
	c := dial(t, s)
	c.next(t)

	c.send(t, `{"type":"watch","tunnel":"alpha"}`)
	time.Sleep(30 * time.Millisecond)
	if got := sampler.reads.Load(); got != 0 {
		t.Fatalf("reads = %d, want none before a view names the tunnel", got)
	}

	s.Publish(aView("alpha"))

	eventually(t, "sampling to start once the tunnel is known",
		func() bool { return sampler.reads.Load() > 0 })
}

func TestWatchingATunnelNobodyHasHeardOfIsIgnored(t *testing.T) {
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)

	c.send(t, `{"type":"watch","tunnel":"nowhere"}`)
	time.Sleep(50 * time.Millisecond)

	if got := sampler.reads.Load(); got != 0 {
		t.Errorf("reads = %d, want none for a tunnel that is not in the view", got)
	}
}

func TestATunnelThatIsDownProducesNoSample(t *testing.T) {
	// No counters is a fact, not a failure, and a zero reading would draw as a
	// tunnel doing nothing rather than a tunnel that is not there.
	s := serving(t, deadSampler{})
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)
	c.send(t, `{"type":"watch","tunnel":"alpha"}`)

	time.Sleep(50 * time.Millisecond)
	s.Publish(aView("alpha"))

	if msg := c.next(t); msg["type"] != "state" {
		t.Errorf("got %v, want no sample for a tunnel that is down", msg)
	}
}

func TestAClientLeavingStopsTheReadingsItAskedFor(t *testing.T) {
	sampler := newSampler()
	s := serving(t, sampler)
	s.Publish(aView("alpha"))
	c := dial(t, s)
	c.next(t)
	c.next(t)
	c.send(t, `{"type":"watch","tunnel":"alpha"}`)
	eventually(t, "the first reading", func() bool { return sampler.reads.Load() > 0 })

	c.Close()

	eventually(t, "the loop to stop with the last watcher", func() bool {
		settled := sampler.reads.Load()
		time.Sleep(40 * time.Millisecond)
		return sampler.reads.Load() == settled
	})
}
```

- [ ] **Step 2: Run the test and watch it fail**

```sh
go test ./internal/feed/ -race -run 'Watch|Sampl|Reading' -v
```

Expected: FAIL. `TestWatchingATunnelStartsItsSamples` times out on the read deadline — `onMessage` does nothing yet.

- [ ] **Step 3: Write the implementation**

Add `sampling chan struct{}` to the `Server` struct's guarded block, then in `internal/feed/server.go` replace `onMessage` and add the loop:

```go
// onMessage acts on one line from a client. Nothing here can act on a tunnel:
// the vocabulary is watch, unwatch and refresh, and no more.
func (s *Server) onMessage(c *client, msg clientMsg) {
	switch msg.Type {
	case "watch":
		if msg.Tunnel == "" {
			return
		}
		s.mu.Lock()
		c.watch[msg.Tunnel] = true
		s.mu.Unlock()
		s.retick()
	case "unwatch":
		s.mu.Lock()
		delete(c.watch, msg.Tunnel)
		s.mu.Unlock()
		s.retick()
	}
}

// retick starts the sampling loop when the first tunnel is watched and stops it
// when the last one is released. A timer waking every second for a graph nobody
// is looking at is a timer waking for nothing.
func (s *Server) retick() {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch watched := s.watchedLocked(); {
	case watched && s.sampling == nil:
		stop := make(chan struct{})
		s.sampling = stop
		go s.sampleLoop(stop)
	case !watched && s.sampling != nil:
		close(s.sampling)
		s.sampling = nil
	}
}

// watchedLocked reports whether anybody is watching anything. The caller holds
// the mutex.
func (s *Server) watchedLocked() bool {
	for c := range s.clients {
		if len(c.watch) > 0 {
			return true
		}
	}
	return false
}

func (s *Server) sampleLoop(stop <-chan struct{}) {
	t := time.NewTicker(s.interval())
	defer t.Stop()

	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.sampleOnce()
		}
	}
}

// sampleOnce reads the union of what is watched and delivers each reading only
// to the clients that asked for that tunnel.
func (s *Server) sampleOnce() {
	s.mu.Lock()
	view := s.view
	watchers := map[string][]*client{}
	for c := range s.clients {
		for name := range c.watch {
			watchers[name] = append(watchers[name], c)
		}
	}
	s.mu.Unlock()

	for name, cs := range watchers {
		row, known := view.Row(name)
		if !known {
			// Either the tunnel is gone or no view has named it yet. The watch
			// stands; the next round looks again.
			continue
		}
		sample, taken := s.Sampler.Sample(row.Tunnel)
		if !taken {
			// A tunnel that is down has no counters. That is a fact rather
			// than a failure, and a zero would draw as a tunnel doing nothing.
			continue
		}
		msg := sampleMsg{
			Type: "sample", Tunnel: name,
			At: sample.At, Rx: sample.Rx, Tx: sample.Tx,
		}
		for _, c := range cs {
			s.sendTo(c, msg)
		}
	}
}
```

`drop` must release the client's watches. Add `s.retick()` as its last statement:

```go
	close(c.out)
	c.conn.Close()
	// The client's watches went with it; the loop stops if it held the last.
	s.retick()
```

And `shutdown` must stop the loop. Insert this immediately before its `s.Close()`:

```go
	s.mu.Lock()
	if s.sampling != nil {
		close(s.sampling)
		s.sampling = nil
	}
	s.mu.Unlock()
```

Do **not** set `s.closed` here. `Close` returns early when it is already set, so
setting it first would leave the socket file on disk —
`TestServeRemovesTheSocketOnItsWayOut` from Task 4 is what catches that. `Close`
is the only thing that sets `closed`.

- [ ] **Step 4: Run the test and watch it pass**

```sh
go test ./internal/feed/ -race -count=2 -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```sh
git add internal/feed
git commit -m "feat(feed): sample a tunnel only while somebody is watching it

A reading a second costs a control-socket read, so it is asked for rather
than always on - the rule the TUI graph pane already follows. The loop
starts with the first watch and stops with the last, including when the
last watcher was a client that hung up.

The union is sampled, not each client's list: two menu bars on the same
tunnel are one read. A watch that names a tunnel no view has mentioned
yet waits rather than being discarded, or the first graph after a start
is always empty.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 7: Refresh on request, with a floor

Without it the menu bar shows state up to `refresh_interval` old — five minutes by default.

**Files:**
- Modify: `internal/feed/server.go` (`onMessage`, `Requests`, the struct)
- Create: `internal/feed/refresh_test.go`

**Interfaces:**
- Consumes: `Request`, `RequestRefresh`, `refreshFloor`, `(*Server).clock` (Task 3).
- Produces: `(*Server).Requests() <-chan Request`. Task 8 reads it from the TUI.

- [ ] **Step 1: Write the failing test**

`internal/feed/refresh_test.go`:

```go
package feed

import (
	"testing"
	"time"
)

func TestAClientCanAskForAFreshView(t *testing.T) {
	// A menu bar opened between two ticks would otherwise show state up to
	// five minutes old.
	s := serving(t, nil)
	c := dial(t, s)
	c.next(t)

	c.send(t, `{"type":"refresh"}`)

	select {
	case req := <-s.Requests():
		if req.Kind != RequestRefresh {
			t.Errorf("request = %+v, want a refresh", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request arrived")
	}
}

func TestRefreshesInQuickSuccessionAreCollapsed(t *testing.T) {
	// Reading the whole system is not free, and a client is not allowed to
	// turn the menu bar into a way of hammering it.
	now := taken
	s := serving(t, nil)
	s.Now = func() time.Time { return now }
	c := dial(t, s)
	c.next(t)

	c.send(t, `{"type":"refresh"}`)
	<-s.Requests()
	c.send(t, `{"type":"refresh"}`)

	select {
	case req := <-s.Requests():
		t.Errorf("a second request got through at once: %+v", req)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestARefreshIsAllowedAgainOnceTheFloorHasPassed(t *testing.T) {
	now := taken
	s := serving(t, nil)
	s.Now = func() time.Time { return now }
	c := dial(t, s)
	c.next(t)

	c.send(t, `{"type":"refresh"}`)
	<-s.Requests()

	now = now.Add(refreshFloor + time.Second)
	c.send(t, `{"type":"refresh"}`)

	select {
	case req := <-s.Requests():
		if req.Kind != RequestRefresh {
			t.Errorf("request = %+v, want a refresh", req)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no request arrived after the floor had passed")
	}
}

func TestRequestsNobodyReadsDoNotWedgeTheFeed(t *testing.T) {
	// Nothing guarantees the interface is listening. A refresh that cannot be
	// delivered is dropped, because the next one carries the same meaning.
	now := taken
	s := serving(t, nil)
	s.Now = func() time.Time { return now }
	c := dial(t, s)
	c.next(t)

	for i := range 5 {
		now = now.Add(refreshFloor + time.Second)
		c.send(t, `{"type":"refresh"}`)
		_ = i
	}

	// The feed is still serving.
	s.Publish(aView("alpha"))
	if msg := c.next(t); msg["type"] != "state" {
		t.Errorf("got %v, want the feed still running", msg)
	}
}

func TestRequestsIsUsableBeforeServeStarts(t *testing.T) {
	// The composition root wires the channel into the interface before it
	// starts accepting anything.
	if (&Server{Path: "/nonexistent"}).Requests() == nil {
		t.Error("Requests returned nil")
	}
}
```

- [ ] **Step 2: Run the test and watch it fail**

```sh
go test ./internal/feed/ -race -run Refresh -v
```

Expected: build failure, `s.Requests undefined`.

- [ ] **Step 3: Write the implementation**

Add to the `Server` struct's guarded block:

```go
	requests    chan Request
	lastRefresh time.Time
```

And in `internal/feed/server.go`:

```go
// Requests yields what clients asked for that the feed cannot do itself. It is
// a Request rather than a bare signal so a second verb does not change every
// signature.
//
// The channel is buffered by one and written without blocking: a refresh that
// cannot be delivered is dropped, because the next one carries the same
// meaning and nothing guarantees anybody is listening.
func (s *Server) Requests() <-chan Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requestsLocked()
}

func (s *Server) requestsLocked() chan Request {
	if s.requests == nil {
		s.requests = make(chan Request, 1)
	}
	return s.requests
}
```

Add the case to `onMessage`:

```go
	case "refresh":
		s.mu.Lock()
		now := s.clock()
		if !s.lastRefresh.IsZero() && now.Sub(s.lastRefresh) < refreshFloor {
			s.mu.Unlock()
			return
		}
		s.lastRefresh = now
		reqs := s.requestsLocked()
		s.mu.Unlock()

		select {
		case reqs <- Request{Kind: RequestRefresh}:
		default:
		}
```

- [ ] **Step 4: Run the test and watch it pass**

```sh
go test ./internal/feed/ -race -count=2 -v
```

Expected: PASS.

- [ ] **Step 5: Full gate**

```sh
make all && make cover
```

Expected: green. `internal/feed` should be at 100%; if it is not, run `go tool cover -func=coverage.out | grep feed` and cover what is left rather than reaching for a `NOT TESTED:` marker.

- [ ] **Step 6: Commit**

```sh
git add internal/feed
git commit -m "feat(feed): let a client ask for a fresh view, at most every 2s

A menu bar opened between two ticks would otherwise show state up to
refresh_interval old - five minutes by default. Reading the whole system
is not free, so the floor is what stops a client turning that into a way
of hammering the WireGuard control socket.

The request is dropped rather than queued when nobody is listening: the
next one carries the same meaning, and nothing guarantees the interface
is reading.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 8: The interface publishes, and honours a refresh request

`Update` stays pure: publishing is a side effect, so it is a `tea.Cmd`, exactly like the notification that already sits there.

**Files:**
- Modify: `internal/tui/model.go` (the `Feed` interface, two fields, `Init`, `nextRequest`)
- Modify: `internal/tui/update.go` (`onView` publishes, a `requestMsg` case)
- Modify: `internal/tui/run.go` (`Run` takes a feed)
- Modify: `internal/tui/run_test.go` (the one `tui.Run` call site)
- Create: `internal/tui/feed_test.go`

**Interfaces:**
- Consumes: `feed.Request`, `feed.RequestRefresh` (Task 7); `app.View`.
- Produces: `tui.Feed` interface (`Publish(app.View)`); `tui.Run(ctx context.Context, a *app.App, n *notify.Notifier, f *feed.Server, opts ...Option) error`.

- [ ] **Step 1: Write the failing test**

`internal/tui/feed_test.go`:

```go
package tui

import (
	"sync"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
)

// recorder is a Feed that keeps what it was given.
type recorder struct {
	mu    sync.Mutex
	views []app.View
}

func (r *recorder) Publish(v app.View) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.views = append(r.views, v)
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.views)
}

// run executes a command and everything it batches, so that a side effect
// hidden in a tea.Cmd can be observed.
func run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			run(c)
		}
	}
}

func TestEveryViewIsPublished(t *testing.T) {
	rec := &recorder{}
	m := loadedModel(threeRows...)
	m.feed = rec

	_, cmd := m.Update(viewMsg{view: viewOf(row("alpha", profile.GroupNeeded, wg.Up))})
	run(cmd)

	if got := rec.count(); got != 1 {
		t.Errorf("published %d view(s), want one per refresh", got)
	}
}

func TestPublishingHappensInACommandRatherThanInUpdate(t *testing.T) {
	// Update performs no I/O. A view published from inside it would be a side
	// effect in the one function this program keeps pure.
	rec := &recorder{}
	m := loadedModel(threeRows...)
	m.feed = rec

	_, cmd := m.Update(viewMsg{view: viewOf(row("alpha", profile.GroupNeeded, wg.Up))})

	if got := rec.count(); got != 0 {
		t.Fatalf("published %d view(s) during Update, want none until the command runs", got)
	}
	run(cmd)
	if got := rec.count(); got != 1 {
		t.Errorf("published %d view(s) after the command, want one", got)
	}
}

func TestAFailedRefreshPublishesNothing(t *testing.T) {
	// There is no view to publish, and the last good one is better than an
	// empty one.
	rec := &recorder{}
	m := loadedModel(threeRows...)
	m.feed = rec

	_, cmd := m.Update(viewMsg{err: errRefresh})
	run(cmd)

	if got := rec.count(); got != 0 {
		t.Errorf("published %d view(s) from a failed refresh, want none", got)
	}
}

func TestAnInterfaceWithoutAFeedIsUnaffected(t *testing.T) {
	m := loadedModel(threeRows...)

	next, cmd := m.Update(viewMsg{view: viewOf(row("alpha", profile.GroupNeeded, wg.Up))})
	run(cmd)

	if len(next.(Model).view.Rows) != 1 {
		t.Error("the view was not taken")
	}
}

func TestARequestedRefreshIsTakenLikeAnyOther(t *testing.T) {
	reqs := make(chan feed.Request, 1)
	m := loadedModel(threeRows...)
	m.requests = reqs

	next, cmd := m.Update(requestMsg{req: feed.Request{Kind: feed.RequestRefresh}, from: reqs})

	if !next.(Model).refreshing {
		t.Error("refreshing = false, want the refresh started")
	}
	if cmd == nil {
		t.Error("cmd = nil, want the refresh and the next request listened for")
	}
}

func TestARequestArrivingDuringARefreshIsDropped(t *testing.T) {
	// A second read of the whole system while the first is out gains nothing.
	reqs := make(chan feed.Request, 1)
	m := loadedModel(threeRows...)
	m.requests = reqs
	m.refreshing = true

	_, cmd := m.Update(requestMsg{req: feed.Request{Kind: feed.RequestRefresh}, from: reqs})

	if cmd == nil {
		t.Error("cmd = nil, want the next request still listened for")
	}
}

func TestListeningForRequestsEndsWithTheChannel(t *testing.T) {
	reqs := make(chan feed.Request)
	close(reqs)

	if msg := nextRequest(reqs)(); msg != nil {
		t.Errorf("msg = %#v, want nothing once the feed is gone", msg)
	}
}

func TestAnInterfaceWithNoFeedListensForNothing(t *testing.T) {
	if cmd := nextRequest(nil); cmd != nil {
		t.Error("cmd is not nil, want no listener without a feed")
	}
}
```

Add near the top of `internal/tui/feed_test.go`, or reuse whatever the package already has for a refresh error:

```go
var errRefresh = errors.New("read the control socket: permission denied")
```

- [ ] **Step 2: Run the test and watch it fail**

```sh
go test ./internal/tui/ -run 'Publish|Request|Feed' -v
```

Expected: build failure, `m.feed undefined`, `undefined: requestMsg`, `undefined: nextRequest`.

- [ ] **Step 3: Write the implementation**

In `internal/tui/model.go`, add the message beside the others in the `type (…)` block:

```go
	// requestMsg is something the feed's clients asked for. The channel travels
	// with it, the way a batch's does.
	requestMsg struct {
		req  feed.Request
		from <-chan feed.Request
	}
```

Add the interface and the two fields:

```go
// Feed is what the interface publishes views to. It is an interface so the
// interface can be tested without a socket, and so a program built without one
// works unchanged.
type Feed interface {
	Publish(app.View)
}
```

In `Model`, beside `notifier`:

```go
	// feed publishes each view for whoever is watching from outside. Nil when
	// the feed is switched off, which is the ordinary case.
	feed     Feed
	requests <-chan feed.Request
```

Extend `Init`:

```go
// Init triggers the first refresh, starts the periodic tick, and starts
// listening to the feed if there is one.
func (m Model) Init() tea.Cmd {
	return tea.Batch(m.refresh(), m.tick(), nextRequest(m.requests))
}
```

And add the listener beside `nextEvent`:

```go
// nextRequest turns the next thing a feed client asked for into a message, and
// is re-issued for as long as the feed keeps talking. Same seam as nextEvent:
// the feed runs on its own, the interface only reads what it says.
func nextRequest(reqs <-chan feed.Request) tea.Cmd {
	if reqs == nil {
		return nil
	}
	return func() tea.Msg {
		req, ok := <-reqs
		if !ok {
			return nil
		}
		return requestMsg{req: req, from: reqs}
	}
}
```

In `internal/tui/update.go`, add the case to `Update`:

```go
	case requestMsg:
		// Listening again first: whatever this request turns into, the next one
		// still has to be heard.
		listen := nextRequest(msg.from)
		if m.refreshing {
			return m, listen
		}
		m.refreshing = true
		return m, tea.Batch(m.refresh(), m.heartbeat(), listen)
```

In `onView`, after the notification command and before `m.view = msg.view`:

```go
	if m.feed != nil {
		f, published := m.feed, msg.view
		cmd = tea.Batch(cmd, func() tea.Msg {
			f.Publish(published)
			return nil
		})
	}
```

`tea.Batch` drops nil commands, so this composes with the notification command whether or not there was one.

In `internal/tui/run.go`:

```go
// Run starts the interactive interface and blocks until the user quits or the
// context is cancelled. A nil feed means nothing is published.
func Run(ctx context.Context, a *app.App, n *notify.Notifier, f *feed.Server, opts ...Option) error {
	programOpts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithContext(ctx)}
	for _, o := range opts {
		o(&programOpts)
	}

	m := New(a, n)
	if f != nil {
		// Assigned through the concrete type rather than passed as an
		// interface: a nil *feed.Server in an interface is not a nil
		// interface, and every publish would panic.
		m.feed = f
		m.requests = f.Requests()
	}

	_, err := tea.NewProgram(m, programOpts...).Run()
	if ctx.Err() != nil {
		return nil //nolint:nilerr // deliberate: cancellation is not an error
	}
	return err
}
```

Update the `tui.Run` call in `internal/tui/run_test.go` to pass `nil` for the feed.

- [ ] **Step 4: Run the tests and watch them pass**

```sh
go test ./internal/tui/ -race -count=1 -v 2>&1 | tail -20
```

Expected: PASS, including every existing test.

- [ ] **Step 5: Commit**

```sh
git add internal/tui
git commit -m "feat(tui): publish every view to the feed, and honour a refresh

Publishing is a side effect, so it happens in a tea.Cmd rather than in
Update - the same place, and for the same reason, as the notification
that already sits there. A failed refresh publishes nothing: the last
good view beats an empty one.

Listening to the feed's requests reuses the seam nextEvent already
established, where the channel travels with the message. A request that
lands while a refresh is out is dropped, because a second read of the
whole system gains nothing.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

---

### Task 9: Configuration, composition root, doctor and README

The feed becomes something a person can switch on, diagnose and read about.

**Files:**
- Modify: `internal/profile/profile.go` (two keys, two defaults)
- Modify: `internal/profile/profile_test.go` (defaults and parsing)
- Modify: `main.go:63-66` (the `interactive` field), `:186-188` (`runTUI`), `:246-262` (`(*env).runTUI`)
- Modify: `main_test.go` (the `interactive` stub's signature)
- Modify: `internal/cli/doctor.go` (a `feed` check), `internal/cli/doctor_test.go`
- Modify: `README.md`

**Interfaces:**
- Consumes: `feed.Server`, `feed.SocketMode` (Task 3); `tui.Run` (Task 8).
- Produces: `profile.DefaultFeedSocket`, `profile.Config.Feed`, `profile.Config.FeedSocket`; `cli.Doctor` gains one `Check` named `status feed`.

- [ ] **Step 1: Write the failing configuration test**

Add to `internal/profile/profile_test.go`:

```go
func TestTheFeedIsOnByDefault(t *testing.T) {
	// The socket is 0600 and owned by one person, so there is nothing to
	// protect by making it opt-in, and an application that needs configuration
	// before it works once is an application nobody runs twice.
	cfg := Default()

	if !cfg.Feed {
		t.Error("Feed = false, want the feed available without configuration")
	}
	if cfg.FeedSocket != DefaultFeedSocket {
		t.Errorf("FeedSocket = %q, want %q", cfg.FeedSocket, DefaultFeedSocket)
	}
}

func TestTheFeedCanBeSwitchedOff(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("feed: false\nfeed_socket: /tmp/other.sock\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Feed {
		t.Error("Feed = true, want it off")
	}
	if cfg.FeedSocket != "/tmp/other.sock" {
		t.Errorf("FeedSocket = %q, want the configured path", cfg.FeedSocket)
	}
}
```

Match the surrounding tests' way of calling `Load` — read the file before writing these, and use whatever helper the package already has for writing a config.

- [ ] **Step 2: Run it and watch it fail**

```sh
go test ./internal/profile/ -run Feed -v
```

Expected: build failure, `cfg.Feed undefined`.

- [ ] **Step 3: Add the configuration**

In `internal/profile/profile.go`, beside the other defaults:

```go
// DefaultFeedSocket is where the status feed binds. /var/run is cleared on
// reboot, which disposes of a socket left behind by a crash for free.
const DefaultFeedSocket = "/var/run/tun-manager.sock"
```

In `Config`, after `Notify`:

```go
	// Feed publishes state on a unix socket for a menu bar application to
	// read. Nothing on that socket can start or stop a tunnel.
	Feed bool `yaml:"feed"`
	// FeedSocket is where that socket is bound.
	FeedSocket string `yaml:"feed_socket"`
```

In `Default()`, after `Notify: true`:

```go
		Feed:       true,
		FeedSocket: DefaultFeedSocket,
```

Check how `Load` fills unset fields — if it unmarshals over `Default()`, `feed: false` works as written; if it unmarshals into a zero `Config` and then fills blanks, add `FeedSocket` to whatever does that filling, and confirm the `feed: false` test passes rather than being overwritten back to true.

- [ ] **Step 4: Run it and watch it pass**

```sh
go test ./internal/profile/ -v
```

- [ ] **Step 5: Write the failing doctor test**

Add to `internal/cli/doctor_test.go`, following the shape of the tests already there:

```go
func TestDoctorReportsWhereTheFeedWouldBind(t *testing.T) {
	// It is the first thing to look at when the menu bar shows nothing.
	cfg := profile.Default()
	cfg.Feed = true
	cfg.FeedSocket = filepath.Join(t.TempDir(), "f.sock")

	check := findCheck(t, Doctor(cfg, privdrop.User{Username: "operator", Demotable: true}, 0, "v0.0.0"), "status feed")

	if check.Status != Pass {
		t.Errorf("status = %v (%s), want Pass on a writable directory", check.Status, check.Detail)
	}
	if !strings.Contains(check.Detail, "operator") {
		t.Errorf("detail = %q, want who the socket is handed to", check.Detail)
	}
}

func TestDoctorSaysSoWhenTheFeedIsOff(t *testing.T) {
	cfg := profile.Default()
	cfg.Feed = false

	check := findCheck(t, Doctor(cfg, privdrop.User{}, 0, "v0.0.0"), "status feed")

	if check.Status != Warn {
		t.Errorf("status = %v, want Warn: the program works, just without a feed", check.Status)
	}
}

func TestDoctorFailsWhenTheFeedHasNowhereToBind(t *testing.T) {
	cfg := profile.Default()
	cfg.Feed = true
	cfg.FeedSocket = filepath.Join(t.TempDir(), "no-such-dir", "f.sock")

	check := findCheck(t, Doctor(cfg, privdrop.User{}, 0, "v0.0.0"), "status feed")

	if check.Status != Fail {
		t.Errorf("status = %v (%s), want Fail", check.Status, check.Detail)
	}
}
```

If `findCheck` does not exist in the package's tests, add it:

```go
func findCheck(t *testing.T, checks []Check, name string) Check {
	t.Helper()

	for _, c := range checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check named %q in %+v", name, checks)
	return Check{}
}
```

- [ ] **Step 6: Run it and watch it fail**

```sh
go test ./internal/cli/ -run Feed -v
```

Expected: FAIL, `no check named "status feed"`.

- [ ] **Step 7: Add the check**

In `internal/cli/doctor.go`, add `checkFeed(cfg, u)` to the slice `Doctor` returns, after the notifications check, and:

```go
// checkFeed reports whether the status feed can bind, and who would be allowed
// to read it. It is the first thing to look at when the menu bar shows nothing.
func checkFeed(cfg *profile.Config, u privdrop.User) Check {
	if !cfg.Feed {
		return Check{
			Name: "status feed", Status: Warn,
			Detail: "disabled (feed: false)",
		}
	}

	dir := filepath.Dir(cfg.FeedSocket)
	info, err := os.Stat(dir)
	if err != nil {
		return Check{Name: "status feed", Status: Fail, Detail: fmt.Sprintf("%s: %v", dir, err)}
	}
	if !info.IsDir() {
		return Check{Name: "status feed", Status: Fail, Detail: dir + " is not a directory"}
	}

	owner := "root"
	if u.Demotable {
		owner = u.Username
	}
	return Check{
		Name: "status feed", Status: Pass,
		Detail: fmt.Sprintf("%s, mode %o, readable by %s", cfg.FeedSocket, feed.SocketMode, owner),
	}
}
```

- [ ] **Step 8: Run it and watch it pass**

```sh
go test ./internal/cli/ -v
```

- [ ] **Step 9: Wire the composition root**

In `main.go`, widen the `interactive` field and `runTUI`:

```go
	// interactive runs the TUI. It is a field so tests never start one.
	interactive func(context.Context, *app.App, *notify.Notifier, *feed.Server) error
```

```go
func runTUI(ctx context.Context, a *app.App, n *notify.Notifier, f *feed.Server) error {
	return tui.Run(ctx, a, n, f)
}
```

Replace `(*env).runTUI`:

```go
func (e *env) runTUI() error {
	a, err := e.build()
	if err != nil {
		return err
	}

	notifier := e.notifier
	var owner privdrop.User
	if _, u, cfgErr := e.config(); cfgErr == nil {
		owner = u
		if notifier == nil {
			n := notify.New(u, a.Config.Notify)
			notifier = &n
		}
	}

	ctx, stop := signalled()
	defer stop()

	f := e.startFeed(ctx, a, owner)
	if f != nil {
		defer f.Close()
	}
	return e.interactive(ctx, a, notifier, f)
}

// startFeed opens the status socket, or returns nil having said why.
//
// Losing the menu bar must never cost you the ability to bring a tunnel up, so
// a feed that cannot start is reported and stepped over. The alternate screen
// puts this back on the terminal when the interface exits, and `doctor` says
// the same thing at any time.
func (e *env) startFeed(ctx context.Context, a *app.App, owner privdrop.User) *feed.Server {
	if !a.Config.Feed {
		return nil
	}

	f := &feed.Server{
		Path:    a.Config.FeedSocket,
		Owner:   owner,
		Sampler: a,
		Version: version,
	}
	if err := f.Listen(); err != nil {
		fmt.Fprintf(e.out, "%s: status feed unavailable: %v\n", appName, err)
		return nil
	}
	go f.Serve(ctx) //nolint:errcheck // reported by doctor; not worth failing the TUI over
	return f
}
```

Update the `interactive` stub in `main_test.go` to the four-argument signature, and add a test that the feed is skipped when it is switched off:

```go
func TestTheInterfaceStartsWithoutAFeedWhenItIsOff(t *testing.T) {
	e := testEnv(t)
	var got *feed.Server
	e.interactive = func(_ context.Context, _ *app.App, _ *notify.Notifier, f *feed.Server) error {
		got = f
		return nil
	}

	if err := e.run(nil); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got != nil {
		t.Errorf("feed = %+v, want none when feed is off", got)
	}
}
```

Build `testEnv` so its configuration has `Feed: false` — follow whatever the file already does to construct an `env`, and set `Feed: true` with a `FeedSocket` in `t.TempDir()` in a second test that asserts a feed is passed and that the socket exists.

- [ ] **Step 10: Run everything**

```sh
make all && make cover
```

Expected: green, total at or above 99%. If `startFeed`'s error branch is not covered, point `FeedSocket` at a directory that does not exist in a test rather than adding a marker.

- [ ] **Step 11: Document it**

In `README.md`, after the keys table and its refresh paragraph, add:

```markdown
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
tunnel a client asked to watch, and a `bye` on the way out.

**Nothing on that socket can start or stop a tunnel.** A client may watch a
tunnel's counters and ask for a refresh, and that is the whole vocabulary.

It is only alive while `tun-manager` is: there is no daemon. Run
`sudo tun-manager doctor` to see where the socket would bind and who could
read it.
```

- [ ] **Step 12: Commit**

```sh
git add -A
git commit -m "feat: switch the status feed on, diagnose it and document it

Two configuration keys, on by default: the socket is 0600 and owned by
one person, so there is nothing to protect by making it opt-in, and an
application that needs configuration before it works once is an
application nobody runs twice.

A feed that cannot start is reported and stepped over rather than fatal.
Losing the menu bar must never cost you the ability to bring a tunnel up.
doctor says where the socket would bind and who could read it, which is
the first thing to look at when the menu bar shows nothing.

Co-Authored-By: Claude Opus 5 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 13: Confirm the push stays a fast-forward**

```sh
git status -sb
git merge-base --is-ancestor origin/main HEAD && echo "fast-forward OK"
```

---

## After the plan

The Go side is complete: `sudo tun-manager` publishes, and the contract is testable from the shell.

```sh
sudo tun-manager &
nc -U /var/run/tun-manager.sock
{"type":"watch","tunnel":"alpha"}
```

The menu bar application is a separate piece of work in a separate repository, written against this contract and against `tun-manager status --json`, which now emits the same vocabulary.
