# What is not tested, and why

The Go side's rule applies here too: anything untested is argued for in two
places, a `NOT TESTED:` comment on the code and a section here.

## Deliberately untested

### the keychain

`Sources/TunManagerFeed/Storage/PinnedKeys.swift` — the three calls into
Security.framework that write, read and delete a generic password item.

A test that exercised them would write into the keychain of whoever ran the
suite and read back what a previous run had left there, which is a test that
changes the machine it runs on; one running in CI would find no keychain to
talk to at all. What is worth testing is not the storage but the decisions
around it — when a key is pinned, what it is compared against, and what happens
when the two differ — and those are in `LinkMachine`, exercised against a store
that lives in memory.

`NoPinnedKeys` is the same shape with nothing behind it, for a run that should
pin nothing.

### the menu bar target

`Sources/TunManagerMenuBar/` — four files: `main.swift`, `AppDelegate.swift`,
`StatusItemController.swift`, `MenuRenderer` (folded into the controller).

Testing them would mean driving `NSApplication` and `NSStatusBar` from a suite
that has no window server session, and asserting on the contents of an
`NSMenu` — which would test AppKit rather than this program.

What makes that acceptable is where the decisions live. None of them are in
these files:

| decision | where it is, and tested |
|---|---|
| which glyph, and whether it is dimmed | `StatusGlyph.of(state:snapshot:)` |
| which rows, in what order, under what headers | `MenuModelBuilder.build(...)` |
| which details to show, and which to omit | `MenuModelBuilder`, per field |
| how a byte count and an age are written | `Formatting` |
| when to reconnect, and how soon | `LinkMachine`, `ReconnectPolicy` |
| what a line on the wire means | `FeedDecoder` |
| what to say about a publisher that did not prove itself | `PublisherWarning` |
| whether that panel is offered, and when it opens by itself | `PublisherWarning.of`, `WarningPrompt` |

The rule that keeps it that way: **an `if` in the AppKit layer is a bug of
placement.** If one appears, the decision belongs in `TunManagerFeed`, where a
test can reach it.

The split is enforced by the build graph rather than by discipline —
`TunManagerFeed` does not link AppKit, so a decision cannot drift into an
untested file without the compiler noticing.
