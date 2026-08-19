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
	"strings"
	"syscall"
	"time"

	"ledez.net/tun-manager/internal/app"
	"ledez.net/tun-manager/internal/cli"
	"ledez.net/tun-manager/internal/feed"
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
  sudo tun-manager import NAME FILE   add a .conf and list it in the all group
  sudo tun-manager backup             archive the configuration and every .conf
  tun-manager doctor                  check the environment
  tun-manager notify                  post a sample notification
  tun-manager version                 print the build version

Flags, before the command:
  --config PATH       read this configuration instead of the user's
  --config-dir DIR    read the .conf files from here
  --feed-socket PATH  bind the status feed here
  --wg-socket DIR     read WireGuard from the UAPI sockets in this directory,
                      and look there for the interface-name files too
  --fake-ping         invent the round trips instead of measuring them

The last two exist for the demo: internal/tools/wgsim serves a directory of
sockets that look like tunnels, and its addresses answer nothing. The doctor
command says so when either is in use.

Configuration: ~/.config/tun-manager/config.yaml
`

// overrides are the flags that move where the program looks, or what it
// believes. Every one of them exists so the program can be run against
// internal/tools/wgsim instead of against a machine with tunnels on it.
//
// They are held together rather than spread over env because they are applied
// together, once, in run - which is what stops one of them being honoured by
// `status` and forgotten by `doctor`.
type overrides struct {
	// config is the configuration file to read, replacing the one under the
	// pre-sudo user's home.
	config string
	// configDir replaces config_dir.
	configDir string
	// feedSocket replaces feed_socket.
	feedSocket string
	// wgSocket is the directory of UAPI sockets to read, and the directory the
	// interface-name files are looked for in. One flag for both because that is
	// how the real /var/run/wireguard is: sockets and names side by side.
	wgSocket string
	// fakePing answers probes without sending anything.
	fakePing bool
}

// apply puts the overrides onto a configuration that has just been read.
func (o overrides) apply(cfg *profile.Config) *profile.Config {
	if o.configDir != "" {
		cfg.ConfigDir = o.configDir
	}
	if o.feedSocket != "" {
		cfg.FeedSocket = o.feedSocket
	}
	if o.wgSocket != "" {
		cfg.RunDir = o.wgSocket
	}
	return cfg
}

// env is everything the commands touch outside of themselves. Holding it in one
// place keeps the dispatch and the flag parsing testable without root, a
// WireGuard socket or a terminal.
type env struct {
	out  io.Writer
	euid int

	// now is the clock, so that a test can pin the timestamp an archive is
	// named after.
	now func() time.Time

	// config loads the user configuration; doctor needs it without root.
	config func() (*profile.Config, privdrop.User, error)
	// build opens the WireGuard control socket and assembles the application.
	build func() (*app.App, error)
	// notifier is optional; without one the TUI posts no notification.
	notifier *notify.Notifier
	// interactive runs the TUI. It is a field so tests never start one.
	interactive func(context.Context, *app.App, *notify.Notifier, *feed.Server) error

	// flags are the overrides parsed off the command line, before the command.
	flags overrides
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
	e := &env{
		out:         os.Stdout,
		euid:        os.Geteuid(),
		now:         time.Now,
		interactive: runTUI,
	}
	// Methods rather than package functions: both read the flags, and both
	// stay replaceable by a test.
	e.config = e.loadConfig
	e.build = e.buildApp
	return e
}

func (e *env) run(args []string) error {
	args, err := e.parseFlags(args)
	if err != nil {
		return err
	}

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
	case "notify":
		// About the desktop session rather than the tunnels, so it has no more
		// reason to ask for a password than the notifications themselves do.
		return e.runNotify()
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
	case "import":
		return e.runImport(args)
	case "backup":
		return e.runBackup(args)
	default:
		return fmt.Errorf("unknown command %q\n\n%s", command, usage)
	}
}

// parseFlags takes the flags that come before the command and returns what is
// left, starting at the command.
//
// flag stops at the first argument that is not a flag, so a command's own flags
// travel on untouched: `--config X status --json` leaves ["status", "--json"].
//
// The overrides are then wrapped around e.config once, here, rather than
// applied at each command. Applying them at the call sites is how one of them
// ends up honoured by `status` and forgotten by `doctor`.
func (e *env) parseFlags(args []string) ([]string, error) {
	fs := newFlagSet(appName)
	fs.StringVar(&e.flags.config, "config", "", "configuration file to read")
	fs.StringVar(&e.flags.configDir, "config-dir", "", "directory of .conf files")
	fs.StringVar(&e.flags.feedSocket, "feed-socket", "", "where to bind the status feed")
	fs.StringVar(&e.flags.wgSocket, "wg-socket", "", "directory of WireGuard UAPI sockets")
	fs.BoolVar(&e.flags.fakePing, "fake-ping", false, "invent round trips instead of measuring them")

	if err := fs.Parse(args); err != nil {
		// -h and --help reach here as flag.ErrHelp rather than as a command,
		// because the flag set sees them first. Handing back "help" keeps the
		// usage printed from the one place that prints it.
		if errors.Is(err, flag.ErrHelp) {
			return []string{"help"}, nil
		}
		return nil, fmt.Errorf("%w\n\n%s", err, usage)
	}

	overrides := e.flags
	base := e.config
	e.config = func() (*profile.Config, privdrop.User, error) {
		cfg, u, err := base()
		if err != nil {
			return cfg, u, err
		}
		return overrides.apply(cfg), u, nil
	}
	return fs.Args(), nil
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

// loadConfig reads the configuration, from --config when one was given.
func (e *env) loadConfig() (*profile.Config, privdrop.User, error) {
	u, err := privdrop.Current()
	if err != nil {
		return nil, privdrop.User{}, err
	}
	path := e.flags.config
	if path == "" {
		path = configPath(u)
	}
	cfg, err := profile.Load(path)
	if err != nil {
		return nil, u, err
	}
	return cfg, u, nil
}

func (e *env) buildApp() (*app.App, error) {
	cfg, _, err := e.config()
	if err != nil {
		return nil, err
	}

	// A chosen directory of UAPI sockets rather than wherever wgctrl looks.
	// Same protocol and same parsing below either way; only the directory
	// differs, so the demo exercises the path that ships.
	if e.flags.wgSocket != "" {
		return e.assemble(cfg, wg.NewReaderIn(e.flags.wgSocket)), nil
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
	return e.assemble(cfg, reader), nil
}

// assemble wires the application around whichever reader it was given.
func (e *env) assemble(cfg *profile.Config, reader wg.Reader) *app.App {
	var pinger probe.Pinger = probe.New()
	if e.flags.fakePing {
		// The demo's check addresses reach nothing, so a real probe would take
		// the timeout and then fail on every row.
		pinger = probe.Simulated{}
	}

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
	}
}

func runTUI(ctx context.Context, a *app.App, n *notify.Notifier, f *feed.Server) error {
	return tui.Run(ctx, a, n, f)
}

// runImport adds a WireGuard configuration to the ones tun-manager manages.
func (e *env) runImport(args []string) error {
	fs := newFlagSet("import")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: sudo tun-manager import <name> <file.conf>")
	}

	cfg, u, err := e.config()
	if err != nil {
		return err
	}
	return cli.Import(e.out, cfg, u, fs.Arg(0), fs.Arg(1))
}

// runBackup archives everything that would be painful to lose.
func (e *env) runBackup(args []string) error {
	fs := newFlagSet("backup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: sudo tun-manager backup")
	}

	cfg, _, err := e.config()
	if err != nil {
		return err
	}
	_, err = cli.Backup(e.out, cfg, e.now())
	return err
}

func (e *env) runDoctor() error {
	cfg, u, err := e.config()
	if err != nil {
		return err
	}

	checks := cli.Doctor(cfg, u, e.euid, version)
	if simulated, ok := cli.Simulation(e.flags.wgSocket, e.flags.fakePing); ok {
		// First, not last: everything below it describes whatever the flags
		// pointed at rather than this machine.
		checks = append([]cli.Check{simulated}, checks...)
	}
	if err := cli.WriteDoctor(e.out, checks); err != nil {
		return err
	}
	if !cli.AllPassed(checks) {
		return errors.New("some checks failed")
	}
	return nil
}

// runNotify posts a sample notification and says what carried it, so that
// whether the icon shows up can be seen rather than assumed.
func (e *env) runNotify() error {
	n := e.notifier
	if n == nil {
		cfg, u, err := e.config()
		if err != nil {
			return err
		}
		built := notify.New(u, cfg.Notify)
		n = &built
	}

	command, postErr := n.Preview(context.Background())

	// Two different reasons for no icon, and telling them apart is the whole
	// point of this command.
	var icon string
	switch tool := filepath.Base(command); {
	case !strings.Contains(tool, "terminal-notifier"):
		icon = "no icon    " + tool + " has no clause for one; install terminal-notifier"
	case n.Icon == "":
		icon = "no icon    it could not be written to the cache"
	default:
		icon = "icon       " + n.Icon
	}

	report := fmt.Sprintf("notify: command %s\nnotify: %s\n", command, icon)
	if postErr == nil {
		report += "notify: posted\n"
	}
	if _, err := io.WriteString(e.out, report); err != nil {
		return err
	}
	if postErr != nil {
		return fmt.Errorf("post a notification with %s: %w", command, postErr)
	}
	return nil
}

func (e *env) runTUI() error {
	a, err := e.build()
	if err != nil {
		return err
	}

	notifier := e.notifier
	var owner privdrop.User
	if _, u, cfgErr := e.config(); cfgErr == nil {
		owner = u
		if notifier == nil {
			n := notify.New(u, a.Config.Notify)
			notifier = &n
		}
	}

	ctx, stop := signalled()
	defer stop()

	f, served := e.startFeed(ctx, a, owner)
	if f != nil {
		// Cancelling before waiting is the whole point: closing the socket
		// first would break the accept loop out with clients still connected
		// and the process gone before goodbye was sent. stop is safe twice.
		defer func() {
			stop()
			<-served
		}()
	}
	return e.interactive(ctx, a, notifier, f)
}

// startFeed opens the status socket, or returns nil having said why.
//
// Losing the menu bar must never cost you the ability to bring a tunnel up, so
// a feed that cannot start is reported and stepped over. The alternate screen
// puts this back on the terminal when the interface exits, and `doctor` says
// the same thing at any time.
//
// The returned channel closes once Serve has returned, so the caller can wait
// for the goodbye and the socket removal it is responsible for rather than
// racing them.
func (e *env) startFeed(ctx context.Context, a *app.App, owner privdrop.User) (*feed.Server, <-chan struct{}) {
	if !a.Config.Feed {
		return nil, nil
	}

	f := &feed.Server{
		Path:    a.Config.FeedSocket,
		Owner:   owner,
		Sampler: a,
		Version: version,
	}
	if err := f.Listen(); err != nil { //nolint:contextcheck // Listen takes no context; Serve below does
		fmt.Fprintf(e.out, "%s: status feed unavailable: %v\n", appName, err) //nolint:errcheck
		return nil, nil
	}

	served := make(chan struct{})
	go func() {
		defer close(served)
		// Serve says goodbye and removes the socket on its way out. Its error
		// is not actionable here: nothing is left to run once it returns.
		_ = f.Serve(ctx)
	}()
	return f, served
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
