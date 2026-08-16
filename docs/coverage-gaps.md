# Coverage gaps

What the test suite does not reach, and why. 4 of 896 statements, so 99.6%.

Regenerate the numbers with:

```sh
make cover                              # the total, and the floor it must clear
go tool cover -func=coverage.out        # per function
go tool cover -html=coverage.out        # per statement, in a browser
```

Every deliberate omission carries a `NOT TESTED:` comment **on the code
itself**, next to what is not tested, naming the section below that argues for
it. Whoever reads that code sees why there is no test without going looking for
one. Go has no coverage pragma, and neither the toolchain nor goveralls honours
one, so the marker is a convention rather than something a compiler enforces.
Find them all with:

```sh
grep -rn 'NOT TESTED:' --include='*.go' .
```

If you add one, add its section here. A marker whose reasoning lives only in a
commit message is an excuse, not a decision. The rule, and the rest of the
conventions, are in [`AGENTS.md`](../AGENTS.md).

## Deliberately untested

### main

`main` calls `os.Exit`, so covering it means starting a subprocess to confirm
that Go can start a program and that a non-zero return becomes a non-zero exit
code. Neither is in doubt, and a test that spawns the binary would be slower
than every other test combined.

Everything `main` reaches is covered: `newEnv` builds the process environment
and `run` dispatches, both driven directly from `main_test.go`, including the
root guard, the flag parsing, the mutually exclusive flags and every command.

Marker: on `main` in `main.go`. 3 statements.

### build and the WireGuard client

`build` wraps a failure of `wg.NewReader`. That call succeeds for any user on
darwin — the client only records where to look, and reaching the root-only
sockets under `/var/run/wireguard` is `Read`'s problem — so the branch guards
against a platform, or a future wgctrl, where opening can fail.

Forcing it would mean threading an opener through `build`, which is a seam
inside the seam `env` already provides, for one line whose whole job is to be
defensive. `wg.NewReader` itself is covered on both paths in its own package,
where the opener is injected.

Marker: on the branch in `build`, in `main.go`. 1 statement.

### The notices generator

`internal/tools/notices` has no unit tests and is excluded from the coverage
profile. It is a build-time generator: it never ships in the binary, and its
only output is `THIRD-PARTY-NOTICES.txt`.

`make notices-check` runs it and fails when its output differs from the file in
the tree, on every `make all` and in CI. That catches a regression, which is
what matters here, but it is worth being precise about what it does not catch:
if the generator missed a licence from the start, nothing would notice. The
file was reviewed once by hand against `go list -deps`; a dependency added
later is covered only insofar as the generator handles it the same way.

Marker: on the package clause in `internal/tools/notices/main.go`.

## Tested, but shallowly

Not omissions, and not marked in code. Recorded because coverage counts these
as done and they are the places a regression would most likely slip through.

The TUI rendering used to head this list — fifteen `strings.Contains` over
`View()`, no golden frame, nothing about alignment, width or colour. It now has
golden frames in `internal/tui/testdata`, regenerated with
`go test ./internal/tui/ -update`, plus assertions that every column starts
where its header does, that no line overflows the terminal at any width, that a
long value is cut rather than folded, and that the three tunnel states and a
failed log entry are told apart by colour. Writing them found three real
defects; see below.

**`wg.ExecRunner`.** Exercised against `/bin/echo`, `/bin/sh`, `/bin/sleep` and
a missing binary, which proves the argv leaves and the exit status comes back.
Nothing checks that `wg-quick` accepts the command line built for it: the argv
is asserted against a string written here, not against what `wg-quick` expects.

**The program against a real tunnel.** Nothing brings a tunnel up or down. All
the logic runs against doubles; that it works for real rests on running
`sudo tun-manager status` and reading the output.

## Not Go, so not covered at all

`scripts/release.sh` was exercised by hand in a throwaway clone with its own
origin — tag created and pushed, then refused on a replay, and refused again
with the tag present locally only. Nothing replays that automatically, so a
regression in a guard shows up when cutting a release.

`.goreleaser.yaml` is exercised by `make release-check`, which the `build` job
runs on every push and pull request: the whole pipeline short of publishing,
so a broken configuration fails there rather than after a tag is pushed. What
it still does not prove is the publishing itself — creating the release,
uploading the archives, the provenance attestation — which only a real tag
exercises.

`Makefile` and `.github/workflows/ci.yml` test themselves by running.

## What this file used to say

It listed three categories of code as out of reach. All three were overstated,
and recording that is more useful than quietly deleting them.

**"Root, hardware, GUI."** Fourteen statements: the WireGuard control sockets,
`net.Interfaces`, `osascript`. None needed a fake daemon or a fake window
server. Each is reached through one line that does nothing but call out, and one
line can be injected — an interface for the WireGuard client, function fields
for the host lookup, a configurable path for the notification command.

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

## Three things found by covering the rest

None was a test written around behaviour that already worked.

**The cursor hid the selection.** A row is always selected under the cursor, so
the cursor mark overwrote the tick: pressing space changed nothing on screen
until you moved away, which reads as a key that does not work. The two marks
now share a three-wide column.

**The table wrapped on a narrow terminal.** The endpoint column insisted on a
minimum width, so below about ninety columns a row ran one character past the
edge and folded onto the next line, taking the whole table out of alignment. The
footer never truncated at all. Padding was done with lipgloss's `Width`, which
folds a long value instead of cutting it, turning one row into two. Rows are now
cut to the terminal, the endpoint column takes what is left down to nothing, and
the header drops the countdown rather than overflow.

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
