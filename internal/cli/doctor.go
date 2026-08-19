package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"ledez.net/tun-manager/internal/feed"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/profile"
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
func Doctor(cfg *profile.Config, u privdrop.User, euid int, version string, opts ...Option) []Check {
	want := expectations{rootNeeded: true}
	for _, opt := range opts {
		opt(&want)
	}

	return []Check{
		{Name: "version", Status: Pass, Detail: version},
		checkRoot(euid, want.rootNeeded),
		checkWgQuick(cfg.WgQuick),
		checkConfigDir(cfg.ConfigDir),
		checkRunDir(cfg.RunDir),
		checkConfigFile(cfg),
		checkGroups(cfg),
		checkNotifications(u),
		checkFeed(cfg, u),
	}
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

func checkWgQuick(path string) Check {
	info, err := os.Stat(path)
	if err != nil {
		return Check{Name: "wg-quick", Status: Fail, Detail: fmt.Sprintf("%s: %v", path, err)}
	}
	if info.Mode()&0o111 == 0 {
		return Check{Name: "wg-quick", Status: Fail, Detail: path + " is not executable"}
	}
	return Check{Name: "wg-quick", Status: Pass, Detail: path}
}

func checkConfigDir(dir string) Check {
	matches, err := filepath.Glob(filepath.Join(dir, "*.conf"))
	if err != nil {
		return Check{Name: "config dir", Status: Fail, Detail: fmt.Sprintf("%s: %v", dir, err)}
	}
	if len(matches) == 0 {
		return Check{Name: "config dir", Status: Fail, Detail: "no *.conf in " + dir}
	}
	return Check{Name: "config dir", Status: Pass, Detail: fmt.Sprintf("%d tunnel(s) in %s", len(matches), dir)}
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
func checkFeed(cfg *profile.Config, u privdrop.User) Check {
	if !cfg.Feed {
		return Check{
			Name: "status feed", Status: Warn,
			Detail: "disabled (feed: false)",
		}
	}

	dir := filepath.Dir(cfg.FeedSocket)
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
		Detail: fmt.Sprintf("%s, mode %o, readable by %s", cfg.FeedSocket, feed.SocketMode, owner),
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
