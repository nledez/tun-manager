# Coverage gaps

What the test suite does not reach, and why. 56 of 893 statements, so 93.7%.

Regenerate the numbers with:

```sh
make cover                              # the total, and the floor it must clear
go tool cover -func=coverage.out        # per function
go tool cover -html=coverage.out        # per statement, in a browser
```

`internal/tools` is excluded from the profile: it holds a build-time generator,
not shipped code, and `make notices-check` exercises it end to end on every run.

## Open

The gaps worth closing. Everything below this list is deliberate.

- [ ] `tui.onView` — the notification branch. `m.notifier` is nil in every TUI
      test, so `notify.Diff` is tested but its wiring into the update loop is
      not. 6 statements.
- [ ] `tui.onKey` — the `n` key. Every other binding is exercised. 1 statement.
- [ ] `tui.cells` — the live endpoint replacing the configured one, and the
      selection mark. 2 statements.
- [ ] `tui.logPane` — the pane truncating past a third of the screen, and the
      styling of a failed entry. 2 statements.
- [ ] `tui.Model` command closures — the `a == nil` guards in `refresh`, `ping`
      and `operate`. The tests build `New(nil, nil)` but never run the returned
      commands. 3 statements, low value: the guards exist for those tests.
- [ ] `tui.toggleTargets` — the error path when a tunnel disappears mid-batch.
      1 statement.
- [ ] `privdrop.Resolve` — a `user.User` with a non-numeric uid or gid. The fake
      directory only returns well-formed entries. 2 statements.
- [ ] `profile.GroupOf` — the `default` fallback of an override whose
      `group_when` has no key for the current context. The sample configuration
      always defines both. 1 statement.
- [ ] `cli.writeTable` — a failing writer. `WriteResults` has this test, using a
      closed pipe; `WriteStatus` does not. 1 statement.

That is 19 statements, which would put the total near 96%.

## Closed

- [x] `netctx.System` — the host calls are fields now, so a test can make
      either fail. The package is at 100%.
- [x] `notify` — the notification command is a configurable path now, so the
      tests exercise the real `privdrop.CommandContext` against a script that
      records its arguments. The package is at 100%.
- [x] `wg.Read`, `wg.Close` — a `Client` interface stands in for
      `*wgctrl.Client`. Mocking the client rather than the socket is the right
      boundary: whether wgctrl works is not this suite's business.

## Root, hardware, GUI

One statement: the error branch of `wg.NewReader`, wrapping a failure of
`wgctrl.New()`.

`wgctrl.New()` succeeds for any user — it only records where to look — so on
darwin that branch does not fire. Reaching the root-only sockets under
`/var/run/wireguard` is `Read`'s problem, which is where the "are you root?"
hint lives and where a fake client exercises both outcomes. That `NewReader`
works without privileges is itself pinned by a test, so a future wgctrl that
changed this would be caught rather than silently reporting the wrong problem.

This section used to hold fourteen statements. The three system calls behind
them — the WireGuard client, `net.Interfaces`, and `osascript` — are now
injected: an interface for the first, function fields for the second, a
configurable binary path for the third. The tests check that tun-manager
handles what each returns, which is the boundary that matters; whether the
system call itself works is not this suite's business.

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
from 93.7% to 93.8%: one case changes, `app.Up`, whose "unknown tunnel" branch
is covered from `main_test.go`. The rest of this document holds either way.

**Statements, not behaviour.** 93.7% counts statements executed, not outcomes
asserted. A line reached by a test that checks nothing counts the same as one
pinned by three assertions. The floor in the Makefile guards against coverage
rotting, not against tests that do not test.
