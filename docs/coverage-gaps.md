# Coverage gaps

What the test suite does not reach, and why. 34 of 896 statements, so 96.2%.

Regenerate the numbers with:

```sh
make cover                              # the total, and the floor it must clear
go tool cover -func=coverage.out        # per function
go tool cover -html=coverage.out        # per statement, in a browser
```

`internal/tools` is excluded from the profile: it holds a build-time generator,
not shipped code, and `make notices-check` exercises it end to end on every run.

## Open

Nothing. Every gap this file listed has been closed, and seven of the eleven
packages are at 100%: `format`, `netctx`, `notify`, `privdrop`, `probe`, `tui`
and `wg`.

Two of them were closed by fixing what the missing test exposed rather than by
writing a test around the existing behaviour:

- The cursor overwrote the selection mark. Since a row is always selected under
  the cursor, pressing space changed nothing on screen until you moved away.
  The two marks now share a three-wide column.
- The "are you root?" hint sat on `NewReader`, which succeeds for any user.
  It moved to `Read`, where the root-only sockets are actually reached.

## Root, hardware, GUI

Nothing. This section used to hold fourteen statements.

The three system calls behind them are injected now: an interface for the
WireGuard client, function fields for `net.Interfaces` and `Interface.Addrs`, a
configurable binary path for `osascript`. Each is reached through one line that
does nothing but call out, and that line is itself injected — `wg.opener`
exists for exactly that reason.

The tests check that tun-manager handles what each returns, which is the
boundary that matters. Whether the system call works is not this suite's
business, and pretending to test it would only have tested the mock.

`internal/wg`, `internal/netctx` and `internal/notify` are at 100%, along with
`internal/format` and `internal/probe`.

## Entry point and composition root

19 statements.

`main`, `loadConfig`, `build` and the package-level `runTUI` are the wiring
itself. `build` also calls `wg.NewReader`, so it inherits the section above.

This is what the `env` struct was introduced for: the tests inject `config`,
`build` and `interactive`, so everything those functions assemble is covered —
the dispatch, the flag parsing, the mutual exclusions, the root guard, the TUI
being the default command. What is left is the assembly, and testing it would
mean spawning a subprocess to confirm that Go can start a program.

`tui.Run`'s final `return err` belongs here too: it is reached only when the
Bubble Tea event loop fails without the context being cancelled, and the tests
run it through `WithoutTerminal`, which succeeds.

## Dependency errors that cannot be forced

16 statements, nearly all `if err != nil { return err }`.

- `app.View` — `wgconf.LoadDir` and `netctx.Detect` failing. The `Reader` error
  is tested, because that one is injected.
- `app.UpGroup`, `DownAll`, `Toggle` — their `View()` error path.
- `filepath.Glob` in `wgconf.LoadDir` and `cli.checkConfigDir` — it only fails
  on a malformed pattern, and both are literals. Defensive dead code.
- `bufio.Scanner.Err()` in `wgconf.ParseFile` — a read error partway through a
  file, while `ParseFile` takes a path rather than an `io.Reader`.
- `profile.Load` — a read error other than "not exist", such as a permission
  denial.
- `main`'s `runDoctor`, `runStatus`, `runUp`, `runDown` and `act` — their error
  propagation.

Some of these could be reached by making the functions take readers instead of
paths. That is a design change to serve a metric, and the metric is not the
point.

## Two things the number hides

**Cross-package attribution.** By default `go test` only credits the package
under test. Measured with `-coverpkg` over the whole module, the total moves
from 96.2% to 96.3%: one case changes, `app.Up`, whose "unknown tunnel" branch
is covered from `main_test.go`. The rest of this document holds either way.

**Statements, not behaviour.** 96.2% counts statements executed, not outcomes
asserted. A line reached by a test that checks nothing counts the same as one
pinned by three assertions. The floor in the Makefile guards against coverage
rotting, not against tests that do not test.
