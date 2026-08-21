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

## What `tun-manager` checks

The trust boundary above says where untrusted input comes from. This says what
is done about it, so that a defence which disappears is a paragraph that stops
being true rather than nothing at all.

**It starts no process but `wg-quick`.** It used to start one more — `osascript`,
demoted to the user who ran `sudo`, to post a notification — and that path is
gone: `Tun Manager.app` raises notifications from a session that is already the
right one. Two questions went with it. `sudo` on macOS does not reset `PATH`, so
a tool looked up by name as root is a name chosen by whoever typed `sudo`; and a
script composed for an interpreter has to be escaped correctly forever. Neither
has to be answered now.

**Reading the user's configuration ends.** Symbolic links are followed there on
purpose — a `~/.config` full of links into a dotfiles repository is ordinary —
so what stops a link from being useful is that the thing at the end of it must
be a regular file of a sane size. Without that, a FIFO holds a root process open
at startup and `/dev/zero` reads until the machine gives out. The parser's
complaint is rewritten too: "cannot unmarshal !!str `hunter2...`" hands over
seven characters of a file the reader may not be allowed to open. Line numbers
and the names of settings survive; values do not.

**A setting is never quietly ignored.** A key that moved to the privileged file
is refused by name, saying where it went. A `refresh_interval` below a second is
raised — a refresh reads the WireGuard control sockets as root — and `doctor`
says so, with both numbers. A `.conf` whose name is not a usable tunnel name is
skipped, and the log pane names the file.

**Nothing read from a file is drawn as it is.** A terminal is an interpreter and
a menu draws what it is given: an escape sequence in a context name repaints the
screen, a bidirectional override draws one address as another, and a ten
thousand character name takes the table apart. Every value out of a file or out
of a command's output passes through one function first, on both sides. What is
sent back to `tun-manager` — the tunnel to watch, the one to probe — is always
the name it knows and never the cleaned one.

## Residual risks, accepted knowingly

These are not oversights. They are the places where the cost of closing the gap
was judged higher than the gap. Each says what an attacker gains, because a risk
without that is a warning nobody can act on.

**Groups and overrides stay on the user's side.** An attacker running as you can
edit them, and so change which tunnels come up when you press `s`, or which
group is "needed". That is a routing change, not a privilege escalation: the
tunnels it can choose between are the ones root already has configuration files
for, and it cannot add one. It is worth knowing that traffic could be sent
through a tunnel you did not intend, or kept out of one you did.

**The status socket is readable by you.** It is bound by root and then handed to
whoever ran `sudo tun-manager` — owned by them, mode `0600` — which is what lets
the menu bar application open it. Any other process running as you can open it
too, and read tunnel names, endpoints, handshake ages and counters. It can also
ask for a probe, which makes a root process send packets: bounded by naming a
*tunnel* rather than an address, so nothing on that socket reaches anywhere
`tun-manager` was not already configured to reach, and floored at one round
every two seconds. Nothing there can start or stop a tunnel.

**The application's pin can be reached by you.** `Tun Manager.app` remembers the
publisher's key in the keychain rather than in defaults, because any process
running as you can write a defaults key. The keychain is better and not perfect:
a process running as you, with access to your keychain, could replace what is
pinned — and would then be trusted the next time that socket answers. What it
cannot do is answer on the socket as root, which is the other half of the check.

**Whoever can write the binary chooses what root does.** `sudo` reads a path and
runs whatever is at it, so the password protects nothing if that name can be
replaced without one. On a stock macOS `/usr/local/bin` is `root:wheel`, but
`/usr/local` does not exist until something creates it and whatever creates it
decides who owns it — Homebrew on Intel Macs hands those directories to the user
who ran the install. `ls -ld /usr/local/bin` answers it in one line; the README
says what to do with either answer. It is also why the release archives are
checksummed.

**A demo publisher is not checked for being root.** A publisher named with
`--socket` is exempt, because the simulator does not run as one — and that is
exactly why it is safe to talk to: it reaches nothing you could not reach
anyway. What it costs is that the exemption exists at all, so the application
says which one it is connected to, permanently, for as long as it is connected.

**The tunnels' own hooks run as root**, as [above](#the-tunnels-themselves).
Importing one is a decision a person makes with the file on the screen; a `.conf`
that arrives in that directory any other way was put there by root, and root
answers for it.

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
