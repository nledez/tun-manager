# Tun Manager.app

A menu bar item showing what `tun-manager` knows about your tunnels.

It is a consumer of the status feed: it subscribes to the unix socket
`tun-manager` publishes and displays what arrives. Nothing it can send starts or
stops a tunnel, which is why it needs no privileges of its own. The one thing it
asks for rather than reads is a ping, and even then it names a tunnel while the
address probed comes from that tunnel's configuration.

## Pointing it somewhere else

```sh
"Tun Manager.app/Contents/MacOS/tun-manager-menubar" --socket /tmp/tm-demo/feed.sock
```

Or, to launch the bundle rather than the binary:

```sh
open "Tun Manager.app" --args --socket /tmp/tm-demo/feed.sock
```

The flag beats the `FeedSocket` defaults key, and leaves nothing behind — a key
left pointing at a demo publisher is a menu bar quietly listing tunnels that do
not exist.

`make -C .. demo` starts a publisher to point it at.

## Building

```sh
make test          # the suite, no window server needed
make app           # assemble and sign build/Tun Manager.app
make run           # the above, then open it
```

There is no Xcode project. Sources and the package manifest are text, so a
change to this application reviews like a change to the rest of the repository.

## Two flavours

```sh
make app                  # Tun Manager.app — the one that gets installed
make app FLAVOUR=dev      # Tun Manager (dev).app — cannot disturb it
```

They can run at the same time. The bundle identifier is what keeps them apart,
and it is not cosmetic: macOS keys the notification permission, the remembered
menu bar position and the `defaults` domain on it, so two builds sharing one
would share all three — including a `FeedSocket` pointed at a stand-in
publisher, which is exactly the confusion this avoids.

The development build carries the same artwork with its hue rotated, so it is
recognisably this application and not mistakable for the installed one in the
Dock or in Command-Tab. Its menu bar glyph is tinted rather than a template,
which breaks the rule the release follows on purpose: with two identical shields
up there, colour is the only thing that says which is which, and the cost of an
occasionally hard-to-read icon is paid by nobody but the person who built it.
`About Tun Manager…` says which flavour it is, so a screenshot carries the
answer too.

## Notifications

A tunnel changing health raises a notification. macOS asks for permission the
first time the application runs; refusing is remembered, and the application
then says so in the log rather than asking again on every start.

Only *changes* are announced. A tunnel appearing means a `.conf` was imported
and one disappearing means it was removed — neither is news about the network —
and the first view after a start has nothing to compare against, so it says
nothing rather than firing a banner per tunnel at launch.

The wording is the wording `tun-manager` itself uses in the terminal. Set
`notify: false` in the tun-manager configuration once you run this, or every
change is announced twice.

Notifications need a real signature: they are unreliable under an ad-hoc one.
`make app` prefers a Developer ID, then an Apple Development certificate, and
only falls back to ad-hoc when the keychain holds neither.

## Running

The socket exists only while `sudo tun-manager` is running — there is no
daemon. With nothing to listen to, the item is a dimmed slashed shield and the
menu says so; that is the normal resting state and not a fault.

On a socket somewhere other than the default:

```sh
defaults write net.ledez.tun-manager.menubar FeedSocket /path/to.sock
```

`sudo tun-manager doctor` prints the path in use.

## Signing

`make app` detects a **Developer ID Application** certificate in the keychain
and uses it. With none it signs ad-hoc, so a fresh clone builds on any Mac with
nothing set up. Pin one explicitly if you prefer:

```sh
make app SIGN_IDENTITY="Developer ID Application: Your Name (TEAMID)"
```

`--timestamp` is always passed. Without it a signature stops validating the day
its certificate expires — about a year for an Apple Development one — and the
application you built today would refuse to launch next year with nothing to
explain the refusal.

## Giving it to somebody

An ad-hoc or Apple Development signature is fine on the machine that made it.
The moment the bundle travels through a zip, a download or AirDrop it acquires
the quarantine attribute, and Gatekeeper refuses it as "damaged and can't be
opened" — the misleading message rather than the honest one.

Notarising is what removes that. It needs a paid Apple Developer Program
membership and two things set up once:

**1. A Developer ID Application certificate.** Xcode → Settings → Accounts →
your team → Manage Certificates → **+** → *Developer ID Application*. Only the
Account Holder can create one, and Apple allows five per account, so export the
private key to a `.p12` and keep it somewhere safe: losing it means burning one
of the five.

**2. Notarisation credentials in the keychain.**

```sh
xcrun notarytool store-credentials tun-manager \
  --apple-id you@example.com --team-id TEAMID \
  --password <app-specific-password>
```

The app-specific password comes from appleid.apple.com → Sign-In and Security.
An App Store Connect API key (`--key`, `--key-id`, `--issuer`) works too and is
the better choice for anything automated: it does not stop working when the
Apple ID password changes.

Then:

```sh
make notarize     # signs, submits, waits, staples, verifies
```

It refuses with an explanation if no Developer ID is in the keychain, rather
than producing a bundle that fails on somebody else's Mac.

`make verify` runs the checks on their own: `codesign --verify --strict`,
`spctl --assess`, and `stapler validate`. **`spctl` is deliberately not part of
`make app`** — without a Developer ID it can never pass, and a permanently red
gate is one somebody eventually deletes.

If you do move an un-notarised build to another machine of your own:

```sh
xattr -dr com.apple.quarantine "/Applications/Tun Manager.app"
```

## Layout

```
Sources/TunManagerFeed/    the library: protocol, link, presentation, transport
Sources/TunManagerMenuBar/ the AppKit layer, deliberately untested
Tests/                     everything worth testing
```

The split is the point. `TunManagerFeed` does not link AppKit, so a decision
cannot drift into an untested file without the compiler noticing. See
`docs/coverage-gaps.md`.
