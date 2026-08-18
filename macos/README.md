# Tun Manager.app

A menu bar item showing what `tun-manager` knows about your tunnels.

It is a **read-only** consumer of the status feed: it subscribes to the unix
socket `tun-manager` publishes and displays what arrives. Nothing it can send
starts or stops a tunnel, which is why it needs no privileges of its own.

## Building

```sh
make test          # the suite, no window server needed
make app           # assemble and sign build/Tun Manager.app
make run           # the above, then open it
```

There is no Xcode project. Sources and the package manifest are text, so a
change to this application reviews like a change to the rest of the repository.

## Running

The socket exists only while `sudo tun-manager` is running — there is no
daemon. With nothing to listen to, the item is a dimmed slashed shield and the
menu says so; that is the normal resting state and not a fault.

On a socket somewhere other than the default:

```sh
defaults write net.ledez.tun-manager.menubar FeedSocket /path/to.sock
```

`sudo tun-manager doctor` prints the path in use.

## Signing, and what it means

The default is an ad-hoc signature, so a fresh clone builds on any Mac with no
keychain set up. For one that survives being moved:

```sh
make app SIGN_IDENTITY="Apple Development: you (TEAMID)"
```

**This application cannot be given to anybody.** Notarisation needs a
`Developer ID Application` certificate, which this project does not have. Built
locally it launches fine; the moment it travels through a zip, AirDrop or a
download it acquires the quarantine attribute and Gatekeeper refuses it with
"damaged and can't be opened" — the misleading message rather than the honest
one. If you move it to another machine of your own:

```sh
xattr -dr com.apple.quarantine "/Applications/Tun Manager.app"
```

`--timestamp` is passed at signing on purpose: without it the signature stops
validating the day the certificate expires, and the application you built today
would refuse to launch a year from now with nothing to explain why.

There is no `spctl` check in the Makefile, and that is deliberate. Without a
Developer ID it could never pass, and a permanently red gate is one somebody
eventually deletes.

## Layout

```
Sources/TunManagerFeed/    the library: protocol, link, presentation, transport
Sources/TunManagerMenuBar/ the AppKit layer, deliberately untested
Tests/                     everything worth testing
```

The split is the point. `TunManagerFeed` does not link AppKit, so a decision
cannot drift into an untested file without the compiler noticing. See
`docs/coverage-gaps.md`.
