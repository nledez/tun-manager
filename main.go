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
  sudo tun-manager init-privileged    create the root-only half of the config
  sudo tun-manager import NAME FILE   add a .conf and list it in the all group
                                     (shows the file and asks; --yes skips)
  sudo tun-manager backup             archive the configuration and every .conf
  tun-manager doctor                  check the environment
  tun-manager notify                  post a sample notification
  tun-manager version                 print the build version

Flags, before the command (simulation only, refused under sudo):
  --config PATH       read this configuration instead of the user's
  --config-dir DIR    read the .conf files from here
  --feed-socket PATH  bind the status feed here
  --wg-quick PATH     run this instead of the configured wg-quick
  --wg-socket DIR     read WireGuard from the UAPI sockets in this directory,
                      and look there for the interface-name files too
  --fake-ping         invent the round trips instead of measuring them

Every one of these exists for the demo: internal/tools/wgsim serves a directory
of sockets that look like tunnels, and its addresses answer nothing. The doctor
command says so when either is in use.

Under sudo they are all refused. Each names something root would then read,
run, bind or unlink, and what root touches is decided by
/private/wireguard/config alone - not by whoever typed the command.

Configuration: ~/.config/tun-manager/config.yaml
               /private/wireguard/config/tun-manager.yaml (root only)
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
	// configDir replaces the fixed /private/wireguard/config. There is no
	// setting for it and no way to reach it under root: a simulated run reads
	// a directory of fixtures owned by whoever cloned the repository.
	configDir string
	// feedSocket replaces feed_socket.
	feedSocket string
	// wgSocket is the directory of UAPI sockets to read, and the directory the
	// interface-name files are looked for in. One flag for both because that is
	// how the real /var/run/wireguard is: sockets and names side by side.
	wgSocket string
	// wgQuick replaces the binary brought up and down with. A simulated run
	// cannot read the privileged file the real one comes from - that file is
	// root's - and pointing a demo at the real wg-quick would rewrite the
	// routing table of whoever is watching.
	wgQuick string
	// fakePing answers probes without sending anything.
	fakePing bool
}

// apply puts the overrides onto a user configuration that has just been read.
func (o overrides) apply(cfg *profile.Config) *profile.Config {
	if o.configDir != "" {
		cfg.ConfigDir = o.configDir
	}
	return cfg
}

// simulating reports whether the flags point the program at something other
// than the machine it is running on.
//
// It is what decides that the privileged file is not read: that file is root's,
// a simulated run is not root, and trying to read it would turn every demo into
// a permission error. The flags cannot be set under sudo, so the two cases
// never meet.
func (o overrides) simulating() bool {
	return o.config != "" || o.configDir != "" || o.feedSocket != "" ||
		o.wgSocket != "" || o.wgQuick != "" || o.fakePing
}

// applyRoot puts the overrides onto the root-only half.
//
// It runs whether that half came from the file or from the built-in values,
// which costs nothing: the flags it reads cannot be set under sudo, and the
// file is only ever read under sudo. The two never meet.
func (o overrides) applyRoot(priv *profile.Privileged) *profile.Privileged {
	if o.wgQuick != "" {
		priv.WgQuick = o.wgQuick
	}
	if o.wgSocket != "" {
		priv.RunDir = o.wgSocket
	}
	if o.feedSocket != "" {
		priv.FeedSocket = o.feedSocket
	}
	return priv
}

// env is everything the commands touch outside of themselves. Holding it in one
// place keeps the dispatch and the flag parsing testable without root, a
// WireGuard socket or a terminal.
type env struct {
	out io.Writer
	// in is where an answer to a question comes from. A field rather than
	// os.Stdin reached for directly, so a test can hand over a canned answer -
	// and so a command that asks something cannot be tested only by hand.
	in   io.Reader
	euid int

	// now is the clock, so that a test can pin the timestamp an archive is
	// named after.
	now func() time.Time

	// config loads the user configuration; doctor needs it without root.
	config func() (*profile.Config, privdrop.User, error)
	// privileged loads the half of the configuration only root can write. It
	// is a second loader rather than a second return value of the first
	// because doctor has to report why it failed while every other command
	// refuses to start, and because a test needs to stand in for a file it
	// cannot create.
	privileged func() (*profile.Privileged, error)
	// enforce refuses to go on when the .conf files, or the directory holding
	// them, can be read by somebody who is not root. A field so a test can
	// stand in for it: a fixture is owned by whoever runs the suite, and the
	// real check wants root on that side.
	enforce func(configDir string) error
	// privilegedPath is the file that loader reads. A field, not the constant
	// inlined, so a test can point at one it is allowed to write - and one
	// test asserts that a real env points at the constant.
	privilegedPath string
	// build opens the WireGuard control socket and assembles the application.
	build func() (*app.App, error)
	// notifier is optional; without one the TUI posts no notification.
	notifier *notify.Notifier
	// interactive runs the TUI. It is a field so tests never start one.
	interactive func(context.Context, *app.App, *notify.Notifier, *feed.Server, []string) error

	// flags are the overrides parsed off the command line, before the command.
	flags overrides
}

// exit and stderr are what main does at the end of a run that failed. They are
// variables so that a test can call main itself: os.Exit ends the process
// before the test can look at anything, and a failure that reaches nobody is
// the one thing this function is for.
var (
	exit             = os.Exit
	stderr io.Writer = os.Stderr
)

func main() {
	if err := newEnv().run(os.Args[1:]); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", appName, err) //nolint:errcheck // there is nowhere left to report it
		exit(1)
	}
}

func newEnv() *env {
	e := &env{
		out:            os.Stdout,
		in:             os.Stdin,
		euid:           os.Geteuid(),
		now:            time.Now,
		interactive:    runTUI,
		enforce:        cli.EnforcePermissions,
		privilegedPath: profile.PrivilegedPath,
	}
	// Methods rather than package functions: both read the flags, and both
	// stay replaceable by a test.
	e.config = e.loadConfig
	e.privileged = e.loadPrivileged
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

	// Root is needed for the real thing: the UAPI sockets under
	// /var/run/wireguard are root-only, and wg-quick rewrites the routing
	// table. Neither is true of a run pointed somewhere else - the sockets are
	// wherever --wg-socket says and are readable by whoever made them - so
	// asking for a password there would be asking for one to read /tmp.
	//
	// This is not a security boundary being lowered. It never was one: the
	// kernel refuses the real sockets to a plain user whatever this says, and
	// wg-quick fails on its own. What it is, is the difference between a demo
	// anybody can run and one nobody does.
	if e.euid != 0 && e.flags.wgSocket == "" {
		// The command is named back, because "run `sudo tun-manager`" is right
		// for the interface and wrong for everything else: it would start the
		// TUI rather than do what was asked for.
		return fmt.Errorf("this needs root: run `sudo tun-manager%s` (see `tun-manager doctor`)",
			asArgument(command))
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
	case "init-privileged":
		return e.runInitPrivileged(args)
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
	fs.StringVar(&e.flags.wgQuick, "wg-quick", "", "binary to bring tunnels up and down with")
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

	// Checked here, once, rather than at each site that reads a flag: a check
	// per site is how one of them ends up honoured by `status` and refused by
	// `doctor`.
	if err := refuseSimulationUnderRoot(fs, e.euid); err != nil {
		return nil, err
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

	basePrivileged := e.privileged
	e.privileged = func() (*profile.Privileged, error) {
		priv, err := basePrivileged()
		if err != nil {
			return nil, err
		}
		return overrides.applyRoot(priv), nil
	}
	return fs.Args(), nil
}

// simulationFlags are the flags that move where the program looks, or what it
// believes. Every one of them decides something root would then do: which file
// says what to run, which directory holds the .conf files wg-quick executes,
// which socket is unlinked and bound, whether a probe is real.
//
// They exist so the program can be run against internal/tools/wgsim by
// somebody who is not root, on a machine with no tunnels. That is also exactly
// why they are refused under sudo: as a plain user they point at fixtures the
// caller already owns, and as root they would point at anything.
var simulationFlags = map[string]bool{
	"config":      true,
	"config-dir":  true,
	"feed-socket": true,
	"wg-socket":   true,
	"wg-quick":    true,
	"fake-ping":   true,
}

// refuseSimulationUnderRoot rejects the flags that must not survive a sudo.
//
// fs.Visit walks the flags that were actually set, not every flag defined, so a
// run that passes none of them is not affected by any of this.
func refuseSimulationUnderRoot(fs *flag.FlagSet, euid int) error {
	if euid != 0 {
		return nil
	}

	var named []string
	fs.Visit(func(f *flag.Flag) {
		if simulationFlags[f.Name] {
			named = append(named, "--"+f.Name)
		}
	})
	if len(named) == 0 {
		return nil
	}

	return fmt.Errorf(
		"%s cannot be used under sudo: %s is a simulation flag, and honouring it as root "+
			"would let whoever typed the command choose what root reads, runs, binds or unlinks. "+
			"Run the demo without sudo (see docs/simulator.md), or drop the flag",
		strings.Join(named, ", "), plural(named))
}

// plural keeps the sentence above grammatical without two copies of it.
func plural(named []string) string {
	if len(named) == 1 {
		return "it"
	}
	return "each of them"
}

// asArgument renders a command for the refusal above, and renders the default
// one as nothing at all: `sudo tun-manager tui` is not what anybody types.
func asArgument(command string) string {
	if command == "tui" {
		return ""
	}
	return " " + command
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

// acting loads everything a command that touches the tunnels needs, and refuses
// when the layout holding them is readable by somebody else.
//
// One place rather than a call per command: a check at each site is how one of
// them ends up honoured by `status` and forgotten by `import`, and the one that
// forgets is the one that writes a key. Anything reaching a tunnel goes through
// here — the interface, status, up, down, import and backup — and doctor does
// not, because reporting what is wrong is its whole job.
//
// A simulated run is exempt. Its config_dir is a directory of fixtures in a
// checked-out repository, owned by whoever cloned it and holding no key;
// demanding root of it would mean demanding that the demo be run as root.
func (e *env) acting() (*profile.Config, *profile.Privileged, privdrop.User, error) {
	cfg, u, err := e.config()
	if err != nil {
		return nil, nil, u, err
	}
	priv, err := e.privileged()
	if err != nil {
		return nil, nil, u, err
	}
	if !e.flags.simulating() {
		if err := e.enforce(cfg.ConfigDir); err != nil {
			return nil, nil, u, err
		}
	}
	return cfg, priv, u, nil
}

// loadPrivileged reads the half of the configuration only root can write, or
// builds it from the flags when the run is a simulation.
//
// A failure is a failure: it never falls back to the built-in values. Defaults
// that appear when the file cannot be read are defaults somebody can arrange to
// get by making it unreadable.
func (e *env) loadPrivileged() (*profile.Privileged, error) {
	if e.flags.simulating() {
		// The flags stand in for the file, and parseFlags is what puts them on.
		priv := profile.DefaultPrivileged()
		priv.Path = "(simulated: no privileged configuration was read)"
		return priv, nil
	}
	return profile.LoadPrivileged(e.privilegedPath)
}

func (e *env) buildApp() (*app.App, error) {
	cfg, priv, _, err := e.acting()
	if err != nil {
		return nil, err
	}

	// A chosen directory of UAPI sockets rather than wherever wgctrl looks.
	// Same protocol and same parsing below either way; only the directory
	// differs, so the demo exercises the path that ships.
	if e.flags.wgSocket != "" {
		return e.assemble(cfg, priv, wg.NewReaderIn(e.flags.wgSocket)), nil
	}

	reader, err := newReader()
	if err != nil {
		// Opening the client only records where to look, so this guards a
		// platform, or a future wgctrl, where that can fail.
		return nil, err
	}
	return e.assemble(cfg, priv, reader), nil
}

// newReader opens the WireGuard control client. A variable so a test can make
// it fail: on darwin it does not, which would leave the branch above unwritten
// or unread, and neither is a good way to treat the code that decides whether
// the program can see the tunnels at all.
var newReader = wg.NewReader

// assemble wires the application around whichever reader it was given.
//
// It takes both halves of the configuration, and that is the point of the
// signature: what root executes and where it looks come from priv, and there is
// no way to build an App without having read it.
func (e *env) assemble(cfg *profile.Config, priv *profile.Privileged, reader wg.Reader) *app.App {
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
		Locator: wg.RunDirLocator{Dir: priv.RunDir},
		Control: &wg.Controller{
			WgQuick: priv.WgQuick,
			// The two rules the privileged configuration can ask for. Off, the
			// documented Homebrew installation works; on, a wg-quick root does
			// not own, or one reached through a link, is refused before it runs.
			Strict: wg.Strict{
				RootOwner: priv.WgQuickRootOwned,
				NoSymlink: priv.WgQuickNoSymlink,
			},
			Runner: wg.ExecRunner{},
			Pinger: pinger,
		},
	}
}

func runTUI(ctx context.Context, a *app.App, n *notify.Notifier, f *feed.Server, problems []string) error {
	return tui.Run(ctx, a, n, f, problems)
}

// runInitPrivileged lays out the root-only half of the configuration.
//
// It asks for root on its own account rather than leaning on the gate in run:
// that gate lets a simulated run through, and this command writes into
// /private/wireguard whatever the flags say.
func (e *env) runInitPrivileged(args []string) error {
	fs := newFlagSet("init-privileged")
	force := fs.Bool("force", false, "replace an existing configuration, keeping the old one beside it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: sudo tun-manager init-privileged [--force]")
	}
	if e.euid != 0 {
		return fmt.Errorf(
			"init-privileged writes %s, which belongs to root: run `sudo tun-manager init-privileged`",
			e.privilegedPath)
	}

	// Deliberately not through e.config: a machine being set up for the first
	// time has no user configuration yet, and needing one to create the other
	// half would be a circle.
	return cli.InitPrivileged(e.out, e.privilegedPath, *force)
}

// runImport adds a WireGuard configuration to the ones tun-manager manages.
func (e *env) runImport(args []string) error {
	fs := newFlagSet("import")
	yes := fs.Bool("yes", false, "import without being asked to agree to the file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: sudo tun-manager import [--yes] <name> <file.conf>")
	}

	cfg, _, u, err := e.acting()
	if err != nil {
		return err
	}

	// --yes answers without reading anything: whatever is on standard input
	// belongs to whoever comes next in the pipeline.
	ask := cli.Ask(e.in, e.out)
	if *yes {
		ask = cli.Assumed(true)
	}
	return cli.Import(e.out, ask, cfg, u, fs.Arg(0), fs.Arg(1))
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

	cfg, priv, _, err := e.acting()
	if err != nil {
		return err
	}
	_, err = cli.Backup(e.out, cfg, priv, e.now())
	return err
}

func (e *env) runDoctor() error {
	cfg, u, err := e.config()
	if err != nil {
		return err
	}

	// The one command that reports the privileged file rather than refusing to
	// start without it. Telling somebody why root cannot read what it needs is
	// the job; failing here would leave them with the failure and no report.
	priv, privErr := e.privileged()
	if privErr != nil {
		priv = profile.DefaultPrivileged()
	}

	var opts []cli.Option
	if e.flags.wgSocket != "" {
		opts = append(opts, cli.RootNotNeeded())
	}

	checks := cli.Doctor(cfg, priv, u, e.euid, version, opts...)
	if !e.flags.simulating() {
		// A simulated run reads no privileged file, so there is nothing to
		// report about one; the simulated line below says where its settings
		// came from instead.
		checks = append([]cli.Check{cli.PrivilegedFile(e.privilegedPath, priv, privErr, e.euid)}, checks...)
	}
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
	priv, err := e.privileged()
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

	f, served, problems := e.startFeed(ctx, a, priv, owner)
	if f != nil {
		// Cancelling before waiting is the whole point: closing the socket
		// first would break the accept loop out with clients still connected
		// and the process gone before goodbye was sent. stop is safe twice.
		defer func() {
			stop()
			<-served
		}()
	}
	return e.interactive(ctx, a, notifier, f, problems)
}

// startFeed opens the status socket, or returns nil and the reason it could not.
//
// Losing the menu bar must never cost you the ability to bring a tunnel up, so
// a feed that cannot start is reported and stepped over. The reason goes to the
// interface, which shows it in the log pane and opens that pane: printing it
// here would put it on a terminal the alternate screen covers a millisecond
// later. `doctor` says the same thing at any time.
//
// The returned channel closes once Serve has returned, so the caller can wait
// for the goodbye and the socket removal it is responsible for rather than
// racing them.
func (e *env) startFeed(ctx context.Context, a *app.App, priv *profile.Privileged, owner privdrop.User) (*feed.Server, <-chan struct{}, []string) {
	if !priv.Feed {
		return nil, nil, nil
	}

	f := &feed.Server{
		Path: priv.FeedSocket,
		// A simulated run binds where the flags said, under a directory
		// belonging to whoever started the demo. Under sudo those flags are
		// refused, so what is left is the real path and the strict rule.
		Simulated: e.flags.simulating(),
		Owner:     owner,
		Sampler:   a,
		Version:   version,
	}
	if err := f.Listen(); err != nil { //nolint:contextcheck // Listen takes no context; Serve below does
		// Handed back rather than printed: the interface is about to cover this
		// terminal, and a line written here is a line nobody sees.
		return nil, nil, []string{fmt.Sprintf("status feed unavailable: %v", err)}
	}

	served := make(chan struct{})
	go func() {
		defer close(served)
		// Serve says goodbye and removes the socket on its way out. Its error
		// is not actionable here: nothing is left to run once it returns.
		_ = f.Serve(ctx)
	}()
	return f, served, nil
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
