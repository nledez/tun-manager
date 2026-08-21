# Security

`tun-manager` runs entirely as root. This file says what that means, where the
line between trusted and untrusted input is drawn, and what is deliberately left
on the wrong side of it.

## Reporting

**Please do not open a public issue for a security problem.** Use GitHub's
private vulnerability reporting: the **Security** tab of
[nledez/tun-manager](https://github.com/nledez/tun-manager/security), then
**Report a vulnerability**. It is private to the maintainers until an advisory
is published.

Useful in a report: what an attacker has to start with, what they end up able to
do, and the shortest sequence between the two. A proof of concept is welcome and
not required — a paragraph naming the file, the key and the call is enough to act
on.

## The threat model

**Who the attacker is.** A process already running as *you*, on your own
machine: a browser that has been got at, a hostile dependency in something you
installed, a binary you downloaded and ran once. It can read and write everything
your account can, and it is patient — it can sit and wait for the next time you
type `sudo tun-manager`.

**What it wants.** To become root. Not to read your tunnel list: it can nearly
always do that already, from `~/.config`, from your shell history, from the
processes it can see. The prize is that you will, at some point, run a program as
root that reads a file — and if it can choose what is in that file, it chooses
what root does.

**What is not in scope.** An attacker who is already root has won before this
program starts; nothing here defends against them. Neither does it defend
against somebody with physical access to an unlocked machine, or against a
`wg-quick` that was already malicious when root installed it. Those are real
risks, and they are somebody else's file.

## The trust boundary, in two files

The split is not about who is allowed to change a setting. It is about what a
setting can *do*.

### `/private/wireguard/config/tun-manager.yaml` — root-equivalent

Every key in it decides a path root executes, binds, unlinks or trusts: which
`wg-quick` is run, which directory the live interface names are read from,
where the status socket is bound and then removed, which key the publisher signs
with. Writing this file is the same as being root, and it is protected
accordingly:

- **The location is hard-coded.** It cannot be moved by a flag, an environment
  variable or the other configuration file. A path that cannot be chosen is a
  path that does not have to be defended.
- Opened with `O_NOFOLLOW`, and every check is made on the open descriptor
  rather than on the path. Checking a name and then opening it is two lookups of
  something that can change in between, which is the shape of the attack this
  file exists to stop.
- Refused unless it is owned by root and no looser than `0600`, and unless the
  directory holding it is owned by root and not writable by anybody else —
  whoever can write the directory can rename the file away and put their own in
  its place.
- A key this program does not know is refused rather than ignored, so a
  misspelling in the file that grants root cannot pass for a setting.
- A missing file is a refusal, not a set of defaults. Defaults that appear when a
  file cannot be read are defaults an attacker can arrange to get.

`sudo tun-manager init-privileged` writes it, `sudo tun-manager doctor` checks
it.

### `~/.config/tun-manager/config.yaml` — not trusted

It holds the refresh interval, network contexts, groups and per-tunnel
overrides. It is under your home directory, which means the
attacker above can rewrite it whenever it likes.

**Nothing in it may become a path that root executes, writes or removes.** That
is the whole rule. The keys that used to live here and could — `config_dir`,
`wg_quick`, `run_dir`, `feed`, `feed_socket` — moved to the privileged file, and
a configuration still carrying one is refused with a message saying where it went
rather than quietly ignored.

### The tunnels themselves

`.conf` files live in `/private/wireguard/config`, root-owned, `0600`. `wg-quick`
runs their `PostUp`/`PreUp` hooks **as root**, which is a feature of `wg-quick`
and not something this program can take away. What it does instead is refuse to
import one behind your back: `sudo tun-manager import` prints the whole file with
line numbers, keys redacted, hooks in red, and does nothing until you say yes.

## For contributors

Adding a configuration key? Answer this, out loud, in the pull request:

> **Does root use this value to touch the filesystem or start a process?**

If yes — a path, a directory, a command, a socket, anything that ends up in an
`exec`, an `open`, a `bind` or an `unlink` — it belongs in the privileged file,
and nowhere else. If no, it belongs in the user's file.

There is no third answer, and "it is only a name, the code joins it to a fixed
directory" is the answer that opens the hole: `..` is a name.

The same question applies to anything arriving on the status socket. A client
there may ask for a refresh, ask for a ping, and watch a tunnel's counters. It
names a *tunnel*, never an address — the address comes from that tunnel's
configuration — so nothing sent from outside can make root reach somewhere it was
not already configured to reach. Keep it that way.

## Residual risks, accepted knowingly

These are not oversights. They are the places where the cost of closing the gap
was judged higher than the gap.

**Nothing read from a file is drawn as it is.** A terminal is an interpreter,
and a menu draws what it is given: an escape sequence in a context name repaints
the screen, and a bidirectional override draws one address as another. Every
value that came from a configuration file or from the output of a command goes
through one function before it is shown, on both sides. What is sent back to
`tun-manager` — the tunnel to watch, the one to probe — is always the name it
knows, never the cleaned one.

**The user's configuration is read as root, and reading it ends.** Symbolic
links are followed there on purpose — a `~/.config` full of links into a
dotfiles repository is ordinary — so what stops a link from being useful is that
the thing at the end of it must be a regular file of a sane size, and that the
parser's complaint is rewritten before anybody sees it. Without those, a FIFO
holds the process open at startup, `/dev/zero` reads until the machine gives
out, and "cannot unmarshal !!str `hunter2...`" hands over seven characters of a
file the reader could not have opened themselves.

**Groups and overrides stay on the user's side.** An attacker running as you can
edit them, and so change which tunnels come up when you press `s`, or which
group is "needed". That is a routing change, not a privilege escalation: the
tunnels it can choose between are the ones root already has configuration files
for, and it cannot add one. It is worth knowing that traffic could be sent
through a tunnel you did not intend, or kept out of one you did.

**The status socket is readable by you.** It is bound by root and then handed to
whoever ran `sudo tun-manager` — owned by them, mode `0600` — which is what lets
the menu bar application open it. Any other process running as you can open it
too, and read tunnel names, endpoints, handshake ages and counters, and ask for
a probe. Nothing on that socket can start or stop a tunnel.

**The application's pin can be reached by you.** `Tun Manager.app` remembers the
publisher's key in the keychain rather than in defaults, because any process
running as you can write a defaults key. The keychain is better and not perfect:
a process running as you, with access to your keychain, could replace what is
pinned. What it cannot do is answer on the socket as root.

**Whoever can write the binary chooses what root does.** `sudo` reads a path and
runs whatever is at it, so the password protects nothing if that name can be
replaced without one. On a stock macOS `/usr/local/bin` is `root:wheel`, but
`/usr/local` does not exist until something creates it and whatever creates it
decides who owns it — Homebrew on Intel Macs hands those directories to the user
who ran the install. `ls -ld /usr/local/bin` answers it in one line; the README
says what to do with either answer. It is also why the release archives are
checksummed.

**`tun-manager` starts no process but `wg-quick`.** It used to start one more —
`osascript`, demoted to the user who ran `sudo`, to post a notification — and
that whole path is gone: `Tun Manager.app` raises notifications from a session
that is already the right one. What went with it is a program running as root
composing a script for an interpreter and starting a GUI process under somebody
else's identity. That was two questions to keep answering — `sudo` on macOS does
not reset `PATH`, so a tool looked up by name as root is a name chosen by whoever
typed `sudo`; and the script was text this program composed, so it had to be
escaped correctly forever. Neither has to be answered now.

**The demo publisher is not root, on purpose.** A publisher named with
`--socket` is not checked for being root, because the simulator does not run as
one — and that is exactly why it is safe: it can reach nothing you could not
reach anyway. The application says so, permanently, for as long as it is
connected to one.

## What the menu bar application checks

`Tun Manager.app` starts and stops nothing, needs no privileges and installs no
helper. It still has one question to answer — *is the thing on that socket the
`tun-manager` on this machine?* — and it answers it twice, on purpose:

- **Credentials.** After connecting, it asks the kernel who is on the other end
  (`LOCAL_PEERCRED`) and refuses anything that is not root. A credential that
  cannot be read is a refusal too.
- **A signature.** The publisher announces the public half of its key, the
  application sends thirty-two bytes of its own, and what comes back must be
  those bytes signed together with the protocol, the version and *the socket path
  the application dialled*. The key is remembered on first use, and every later
  connection is checked against what was remembered.

Neither replaces the other. A key can be copied out of a backup and replayed by
an ordinary process; being uid 0 cannot be copied. Remove the credentials check
and a stolen key is enough; remove the signature and any root-owned process on
the machine will do. When the check fails, nothing the publisher said is shown —
not the tunnels, not the counters, not a notification.
