// Command tun-manager watches and drives the WireGuard tunnels of this machine.
//
// It runs entirely as root: the WireGuard control sockets under
// /var/run/wireguard are root-only, and wg-quick needs to rewrite the routing
// table. Start it with `sudo tun-manager`.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/cli"
	"ledez.net/tun-manager/internal/notify"
	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/probe"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/tui"
	"ledez.net/tun-manager/internal/wg"
)

const appName = "tun-manager"

// version is stamped at build time with -ldflags; a local build keeps "dev".
var version = "dev"

const usage = `tun-manager — WireGuard tunnel manager

Usage:
  sudo tun-manager                    interactive TUI (default)
  sudo tun-manager status [--json]    print the current state
  sudo tun-manager up <name>...       bring tunnels up
  sudo tun-manager up --group NAME    bring a whole group up (needed, extra, all)
  sudo tun-manager down <name>...     bring tunnels down
  sudo tun-manager down --all         bring every tunnel down
  tun-manager doctor                  check the environment
  tun-manager version                 print the build version

Configuration: ~/.config/tun-manager/config.yaml
`

// env is everything the commands touch outside of themselves. Holding it in one
// place keeps the dispatch and the flag parsing testable without root, a
// WireGuard socket or a terminal.
type env struct {
	out  io.Writer
	euid int

	// config loads the user configuration; doctor needs it without root.
	config func() (*profile.Config, privdrop.User, error)
	// build opens the WireGuard control socket and assembles the application.
	build func() (*app.App, error)
	// notifier is optional; without one the TUI posts no notification.
	notifier *notify.Notifier
	// interactive runs the TUI. It is a field so tests never start one.
	interactive func(context.Context, *app.App, *notify.Notifier) error
}

// NOT TESTED: this calls os.Exit, so covering it means starting a subprocess to
// confirm that Go can start a program and that a non-zero return becomes a
// non-zero exit code. Everything it reaches is covered: newEnv wires the
// process, run dispatches, and main_test.go drives both directly.
// See docs/coverage-gaps.md, "main".
func main() {
	if err := newEnv().run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", appName, err)
		os.Exit(1)
	}
}

func newEnv() *env {
	return &env{
		out:         os.Stdout,
		euid:        os.Geteuid(),
		config:      loadConfig,
		build:       build,
		interactive: runTUI,
	}
}

func (e *env) run(args []string) error {
	command := "tui"
	if len(args) > 0 {
		command, args = args[0], args[1:]
	}

	switch command {
	case "help", "-h", "--help":
		_, err := fmt.Fprint(e.out, usage)
		return err
	case "version":
		_, err := fmt.Fprintf(e.out, "%s %s\n", appName, version)
		return err
	case "doctor":
		// doctor is the one command that must work as a plain user: telling you
		// that you are not root is part of its job.
		return e.runDoctor()
	}

	if e.euid != 0 {
		return errors.New("this needs root: run `sudo tun-manager` (see `tun-manager doctor`)")
	}

	switch command {
	case "tui":
		return e.runTUI()
	case "status":
		return e.runStatus(args)
	case "up":
		return e.runUp(args)
	case "down":
		return e.runDown(args)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usage)
	}
}

// signalled returns a context cancelled by an interrupt, so a long batch of
// wg-quick runs can be stopped between two tunnels.
func signalled() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// configPath resolves the configuration file under the pre-sudo user's home,
// not under /var/root where sudo's env_reset points HOME.
func configPath(u privdrop.User) string {
	return filepath.Join(u.ConfigDir(appName), "config.yaml")
}

func loadConfig() (*profile.Config, privdrop.User, error) {
	u, err := privdrop.Current()
	if err != nil {
		return nil, privdrop.User{}, err
	}
	cfg, err := profile.Load(configPath(u))
	if err != nil {
		return nil, u, err
	}
	return cfg, u, nil
}

func build() (*app.App, error) {
	cfg, _, err := loadConfig()
	if err != nil {
		return nil, err
	}
	reader, err := wg.NewReader()
	// NOT TESTED: this branch. Opening the client succeeds for any user on
	// darwin - it only records where to look - so this guards against a
	// platform, or a future wgctrl, where it can fail. Reaching it would mean
	// threading an opener through here, a seam inside the seam env already
	// provides, for one defensive line; wg.NewReader is covered on both paths
	// in its own package. See docs/coverage-gaps.md, "build and the WireGuard
	// client".
	if err != nil {
		return nil, err
	}

	pinger := probe.New()
	return &app.App{
		Config:  cfg,
		Reader:  reader,
		Pinger:  pinger,
		Locator: wg.RunDirLocator{Dir: cfg.RunDir},
		Control: &wg.Controller{
			WgQuick: cfg.WgQuick,
			Runner:  wg.ExecRunner{},
			Pinger:  pinger,
		},
	}, nil
}

func runTUI(ctx context.Context, a *app.App, n *notify.Notifier) error {
	return tui.Run(ctx, a, n)
}

func (e *env) runDoctor() error {
	cfg, u, err := e.config()
	if err != nil {
		return err
	}

	checks := cli.Doctor(cfg, u, e.euid, version)
	if err := cli.WriteDoctor(e.out, checks); err != nil {
		return err
	}
	if !cli.AllPassed(checks) {
		return errors.New("some checks failed")
	}
	return nil
}

func (e *env) runTUI() error {
	a, err := e.build()
	if err != nil {
		return err
	}

	notifier := e.notifier
	if notifier == nil {
		if _, u, err := e.config(); err == nil {
			n := notify.New(u, a.Config.Notify)
			notifier = &n
		}
	}

	ctx, stop := signalled()
	defer stop()
	return e.interactive(ctx, a, notifier)
}

func (e *env) runStatus(args []string) error {
	fs := newFlagSet("status")
	asJSON := fs.Bool("json", false, "print machine-readable JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	a, err := e.build()
	if err != nil {
		return err
	}
	view, err := a.View()
	if err != nil {
		return err
	}
	return cli.WriteStatus(e.out, view, *asJSON)
}

func (e *env) runUp(args []string) error {
	fs := newFlagSet("up")
	group := fs.String("group", "", "bring a whole group up (needed, extra, all)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *group == "" && fs.NArg() == 0 {
		return errors.New("up: name at least one tunnel, or pass --group")
	}
	if *group != "" && fs.NArg() > 0 {
		return errors.New("--group and explicit tunnel names are mutually exclusive")
	}

	return e.act(func(ctx context.Context, a *app.App) ([]wg.Result, error) {
		if *group != "" {
			return a.UpGroup(ctx, *group)
		}
		view, err := a.View()
		if err != nil {
			return nil, err
		}
		return a.Up(ctx, view, fs.Args())
	})
}

func (e *env) runDown(args []string) error {
	fs := newFlagSet("down")
	all := fs.Bool("all", false, "bring every tunnel of the `all` group down")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*all && fs.NArg() == 0 {
		return errors.New("down: name at least one tunnel, or pass --all")
	}
	if *all && fs.NArg() > 0 {
		return errors.New("--all and explicit tunnel names are mutually exclusive")
	}

	return e.act(func(ctx context.Context, a *app.App) ([]wg.Result, error) {
		if *all {
			return a.DownAll(ctx)
		}
		view, err := a.View()
		if err != nil {
			return nil, err
		}
		return a.Down(ctx, view, fs.Args())
	})
}

// act opens the application, runs one batch of operations under a context an
// interrupt can cancel, and reports the outcome.
func (e *env) act(fn func(context.Context, *app.App) ([]wg.Result, error)) error {
	a, err := e.build()
	if err != nil {
		return err
	}

	ctx, stop := signalled()
	defer stop()

	results, err := fn(ctx, a)
	if err != nil {
		return err
	}
	return cli.WriteResults(e.out, results)
}

// newFlagSet keeps flag errors as returned values rather than as a process
// exit, so the caller decides what to print.
func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}
