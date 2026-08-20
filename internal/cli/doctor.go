package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
)

// Status is the outcome of one environment check.
type Status int

const (
	// Pass means the check found nothing wrong.
	Pass Status = iota
	// Warn means the program works but in a degraded way.
	Warn
	// Fail means the program cannot work.
	Fail
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "ok"
	case Warn:
		return "warn"
	default:
		return "FAIL"
	}
}

// Check is one line of the doctor report.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Option adjusts what Doctor expects of the environment.
type Option func(*expectations)

type expectations struct{ rootNeeded bool }

// RootNotNeeded says this run reads a simulator rather than the machine.
//
// Without it doctor reports a missing root as a failure, and a demo whose own
// diagnostic exits non-zero is one nobody believes the rest of.
func RootNotNeeded() Option { return func(e *expectations) { e.rootNeeded = false } }

// Doctor inspects the environment and reports what would stop tun-manager from
// working.
func Doctor(cfg *profile.Config, priv *profile.Privileged, u privdrop.User, euid int, version string, opts ...Option) []Check {
	want := expectations{rootNeeded: true}
	for _, opt := range opts {
		opt(&want)
	}

	checks := []Check{
		{Name: "version", Status: Pass, Detail: version},
		checkRoot(euid, want.rootNeeded),
		checkWgQuick(priv.WgQuick),
		checkConfigDir(cfg.ConfigDir),
		checkRunDir(priv.RunDir),
		checkConfigFile(cfg),
	}
	// Skipped for a simulated run. Its config_dir is a directory of fixtures in
	// a checked-out repository, owned by whoever cloned it and holding no key -
	// demanding root of it would be demanding that a demo be run as root, which
	// is the thing the simulator exists to avoid.
	if want.rootNeeded {
		checks = append(checks, Permissions(cfg, u)...)
	}
	return append(checks, []Check{
		checkGroups(cfg),
		checkNotifications(u),
		checkFeed(priv, u),
	}...)
}

// Simulation reports what about this run is invented, or false when nothing is.
//
// Separate from Doctor because it is about the flags rather than the machine,
// and because a report that does not say it is looking at a simulator is a
// trap: every other line below it would then describe something that does not
// exist.
//
// Warn rather than Pass. Nothing is broken, but nothing here is real either,
// and a green line saying so would be the wrong shape of reassurance.
func Simulation(wgSocket string, fakePing bool) (Check, bool) {
	var what []string
	if wgSocket != "" {
		what = append(what, "wireguard state from "+wgSocket)
	}
	if fakePing {
		what = append(what, "round trips are invented, nothing is sent")
	}
	if len(what) == 0 {
		return Check{}, false
	}
	return Check{
		Name:   "simulated",
		Status: Warn,
		Detail: strings.Join(what, "; "),
	}, true
}

// PrivilegedFile reports whether the root-only half of the configuration could
// be read, and why not when it could not.
//
// Separate from Doctor, like Simulation, because it is the one check whose
// failure means every line below it describes built-in defaults rather than
// this machine — and because doctor is the one command that must still run
// when it fails. Everything else refuses to start.
func PrivilegedFile(path string, priv *profile.Privileged, err error, euid int) Check {
	const name = "privileged config"

	switch {
	case err == nil:
		return feedKeyDetail(name, path, priv)
	case euid != 0 && errors.Is(err, fs.ErrPermission):
		// The file is 0600 and root's, so a plain user cannot read it. That is
		// the design working, not a fault, and reporting it as a failure would
		// teach people to chmod it.
		return Check{
			Name: name, Status: Warn,
			Detail: path + " is readable by root only, as it should be: `sudo tun-manager doctor` to check it",
		}
	default:
		return Check{Name: name, Status: Fail, Detail: err.Error()}
	}
}

// feedKeyDetail renders the line for a file that was read, which is where the
// feed key's fingerprint belongs.
//
// The fingerprint is the whole reason this line is worth reading twice: it is
// what somebody compares against the menu bar application's About window when
// it says the publisher has changed. Only the fingerprint is shown, never the
// seed - a report is printed, piped and pasted into issues.
func feedKeyDetail(name, path string, priv *profile.Privileged) Check {
	seed := priv.FeedKey.Reveal()
	if seed == "" {
		return Check{
			Name: name, Status: Warn,
			Detail: path + " has no feed key: the menu bar application cannot tell this publisher " +
				"from another. `sudo tun-manager init-privileged --force` writes one",
		}
	}

	fingerprint, err := feed.FingerprintOfSeed(seed)
	if err != nil {
		// The message names what is wrong with the key, never the key.
		return Check{
			Name: name, Status: Warn,
			Detail: fmt.Sprintf("%s: %v. `sudo tun-manager init-privileged --force` writes a new one", path, err),
		}
	}
	return Check{
		Name: name, Status: Pass,
		Detail: fmt.Sprintf("%s %04o root:root, feed key %s", path, TunnelFileMode.Perm(), fingerprint),
	}
}

func checkRoot(euid int, needed bool) Check {
	switch {
	case euid == 0:
		return Check{Name: "running as root", Status: Pass, Detail: "euid 0"}
	case !needed:
		return Check{
			Name:   "running as root",
			Status: Pass,
			Detail: fmt.Sprintf("euid %d, and root is not needed: this run reads a simulator", euid),
		}
	default:
		return Check{
			Name:   "running as root",
			Status: Fail,
			Detail: fmt.Sprintf("euid %d: the WireGuard control sockets are root-only, run `sudo tun-manager`", euid),
		}
	}
}

// checkWgQuick reports on the binary root runs to bring a tunnel up.
//
// The refusals come from wg.CheckExecutable, which is what the controller
// itself calls: one implementation, so the diagnostic cannot disagree with the
// program about what is acceptable.
//
// What this adds is the part that cannot be refused. Homebrew installs under
// the user who ran it, so on an ordinary Mac /opt/homebrew/bin/wg-quick and the
// directory holding it belong to uid 501 rather than to root. Any process
// running as that user can replace what root executes at the next `sudo
// tun-manager up`, and no mode check closes that - the owner is the attacker in
// the model this program is written against. Refusing it would refuse the
// installation the README documents, so it is reported instead, in the one
// place with room to say what it means.
func checkWgQuick(path string) Check {
	const name = "wg-quick"

	if err := wg.CheckExecutable(path); err != nil {
		return Check{Name: name, Status: Fail, Detail: err.Error()}
	}
	if reach, ok := reachableByAnybodyButRoot(path); ok {
		return Check{
			Name: name, Status: Warn,
			Detail: fmt.Sprintf(
				"%s: any process running as that user can replace what root executes at the next "+
					"`sudo tun-manager up`. Homebrew installs under the user who ran it, so this is "+
					"the ordinary state of a brew install; copying wg-quick somewhere root owns and "+
					"pointing wg_quick at it is what closes it", reach),
		}
	}
	return Check{Name: name, Status: Pass, Detail: path + ", root all the way up"}
}

// reachableByAnybodyButRoot names the first thing on the way to a binary that
// somebody other than root can change, or reports that there is none.
//
// Both the name given and what it resolves to are walked: a link in a directory
// root owns, pointing into one it does not, reads as safe and is not.
func reachableByAnybodyButRoot(path string) (string, bool) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		// NOT TESTED: wg.CheckExecutable resolved this path a moment ago, and
		// is called first by the only caller.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
		resolved = path
	}

	for _, start := range []string{path, resolved} {
		for name := start; ; name = filepath.Dir(name) {
			info, statErr := os.Lstat(name)
			if statErr != nil {
				// NOT TESTED: same window as above.
				// See docs/coverage-gaps.md, "filesystem races in the permission code".
				break
			}
			if uid, _ := ownerOf(name, info); uid != ownerRoot {
				return fmt.Sprintf("%s is owned by uid %d rather than root", name, uid), true
			}
			if info.Mode().Perm()&0o020 != 0 {
				return fmt.Sprintf("%s is %04o, so its group can write it", name, info.Mode().Perm()), true
			}
			if name == filepath.Dir(name) {
				break
			}
		}
	}
	return "", false
}

// checkConfigDir counts the tunnels, and says why when it cannot.
//
// os.ReadDir rather than filepath.Glob, which by design returns an error only
// for a malformed pattern: a directory it cannot read comes back as no matches
// at all. That mattered the day config_dir became 0700 and owned by root -
// running doctor without sudo then reported "no *.conf", telling the user their
// tunnels had disappeared when the truth was that this process could not look.
func checkConfigDir(dir string) Check {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Check{Name: "config dir", Status: Fail, Detail: fmt.Sprintf("%s: %v", dir, err)}
	}

	tunnels := 0
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".conf") {
			tunnels++
		}
	}
	if tunnels == 0 {
		return Check{Name: "config dir", Status: Fail, Detail: "no *.conf in " + dir}
	}
	return Check{Name: "config dir", Status: Pass, Detail: fmt.Sprintf("%d tunnel(s) in %s", tunnels, dir)}
}

// checkRunDir matters because the "<tunnel>.name" files it holds are the only
// way to tell apart two configs that share a peer public key.
func checkRunDir(dir string) Check {
	if _, err := os.Stat(dir); err != nil {
		return Check{
			Name:   "wireguard run dir",
			Status: Warn,
			Detail: fmt.Sprintf("%s unreadable, falling back to matching tunnels by peer public key: %v", dir, err),
		}
	}
	return Check{Name: "wireguard run dir", Status: Pass, Detail: dir}
}

func checkConfigFile(cfg *profile.Config) Check {
	if cfg.IsDefault {
		return Check{
			Name:   "configuration",
			Status: Warn,
			Detail: fmt.Sprintf("no file at %s, using built-in defaults", cfg.Path),
		}
	}
	return Check{Name: "configuration", Status: Pass, Detail: cfg.Path}
}

// checkGroups warns about a configuration that parses but cannot drive
// anything: no group means `down --all` and the s/n/e keys are no-ops.
func checkGroups(cfg *profile.Config) Check {
	if !cfg.HasGroups() {
		return Check{
			Name:   "groups",
			Status: Warn,
			Detail: "no group configured: `down --all` and the s/n/e keys have nothing to act on, see configs/config.example.yaml",
		}
	}

	var total int
	for _, members := range cfg.Groups {
		total += len(members)
	}
	return Check{
		Name:   "groups",
		Status: Pass,
		Detail: fmt.Sprintf("%d group(s), %d membership(s), %d override(s)", len(cfg.Groups), total, len(cfg.Overrides)),
	}
}

func checkNotifications(u privdrop.User) Check {
	if !u.Demotable {
		return Check{
			Name:   "notifications",
			Status: Warn,
			Detail: "no SUDO_USER: osascript cannot reach a GUI session, notifications are disabled",
		}
	}
	return Check{Name: "notifications", Status: Pass, Detail: "posted as " + u.Username}
}

// checkFeed reports whether the status feed can bind, and who would be allowed
// to read it. It is the first thing to look at when the menu bar shows nothing.
func checkFeed(priv *profile.Privileged, u privdrop.User) Check {
	if !priv.Feed {
		return Check{
			Name: "status feed", Status: Warn,
			Detail: "disabled (feed: false)",
		}
	}

	dir := filepath.Dir(priv.FeedSocket)
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
		Detail: fmt.Sprintf("%s, mode %o, readable by %s", priv.FeedSocket, feed.SocketMode, owner),
	}
}

// AllPassed reports whether no check failed. Warnings do not count as failures.
func AllPassed(checks []Check) bool {
	for _, c := range checks {
		if c.Status == Fail {
			return false
		}
	}
	return true
}

// WriteDoctor renders the report.
func WriteDoctor(w io.Writer, checks []Check) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range checks {
		// tabwriter keeps the first write error and returns it from Flush.
		fmt.Fprintf(tw, "%s\t%s\t%s\n", c.Status, c.Name, c.Detail) //nolint:errcheck
	}
	return tw.Flush()
}
