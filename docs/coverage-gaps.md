# Coverage gaps

What the test suite does not reach, and why. 4 of 896 statements, so 99.6%.

Regenerate the numbers with:

```sh
make cover                              # the total, and the floor it must clear
go tool cover -func=coverage.out        # per function
go tool cover -html=coverage.out        # per statement, in a browser
```

`internal/tools` is excluded from the profile: it holds a build-time generator,
not shipped code, and `make notices-check` exercises it end to end on every run.

## What is left

**`main`, 3 statements.** It calls `os.Exit`, so covering it means starting a
subprocess to confirm that Go can start a program and that a non-zero return
becomes a non-zero exit. Everything it calls is covered: `newEnv` wires the
process and `run` dispatches, and both are driven directly.

**`build`, 1 statement** — the branch wrapping a failure of `wg.NewReader`.
`NewReader` succeeds for any user on darwin, so that branch guards against a
platform, or a future wgctrl, where opening the client can fail. Forcing it
would mean threading an opener through the composition root: a seam inside the
seam `env` already provides, for one line whose whole job is to be defensive.

Every other package is at 100%.

## What this file used to say

It listed three categories of code as out of reach. All three were overstated,
and recording that is more useful than quietly deleting them.

**"Root, hardware, GUI."** Fourteen statements: the WireGuard control sockets,
`net.Interfaces`, `osascript`. None of them needed a fake daemon or a fake
window server. Each is reached through one line that does nothing but call out,
and one line can be injected — an interface for the WireGuard client, function
fields for the host lookup, a configurable path for the notification command.
The tests check that tun-manager handles what each returns, which is the
boundary that matters; whether the system call itself works is not this suite's
business.

**"Entry point and composition root."** Nineteen statements. Only `main` needed
a subprocess. `loadConfig`, `build` and `runTUI` are ordinary functions a test
can call: `privdrop.Current` reads the environment, `profile.Load` returns the
defaults for a missing file, `wgctrl.New` needs no privilege, and a cancelled
context makes the interface return at once. They were uncovered because `env`
injects around them, not because they could not run.

**"Dependency errors that cannot be forced."** Sixteen statements, called
defensive dead code. Two of those claims were simply wrong:

- `filepath.Glob` "only fails on a malformed pattern, and both are literals" —
  but the pattern is built from `config_dir`, which comes from the user's YAML.
  A directory whose name contains `[` yields `syntax error in pattern`. That is
  user input reaching an error path, not dead code.
- `bufio.Scanner.Err()` was called unreachable because `ParseFile` takes a path
  rather than an `io.Reader`. A directory opens like a file and fails on the
  first read, which reaches it with no fake file system at all.

The lesson is narrow but worth keeping: "cannot be tested" turned out, nearly
every time, to mean "I have not found the handle yet".

## Two things found by covering the rest

Neither was a test written around behaviour that already worked.

**The cursor hid the selection.** A row is always selected under the cursor, so
the cursor mark overwrote the tick: pressing space changed nothing on screen
until you moved away, which reads as a key that does not work. The two marks
now share a three-wide column.

**The root hint was on the wrong branch.** `wgctrl.New` succeeds for any user,
so "are you root?" sat where it never fired, while the failure a user actually
meets — `Devices()` reaching the root-only sockets — said only "list wireguard
devices". The hint moved, and a test now pins that opening the client needs no
privilege, so a future wgctrl changing that would be caught rather than
misreported.

## Two things the number hides

**Cross-package attribution.** By default `go test` only credits the package
under test. Measured with `-coverpkg` over the whole module the total is the
same 99.6%, now that every branch is covered from the package that owns it.

**Statements, not behaviour.** 99.6% counts statements executed, not outcomes
asserted. A line reached by a test that checks nothing counts the same as one
pinned by three assertions. The floor in the Makefile guards against coverage
rotting, not against tests that do not test.
