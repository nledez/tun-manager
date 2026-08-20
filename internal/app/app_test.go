package app

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/netctx"
	"ledez.net/tun-manager/internal/profile"
	"ledez.net/tun-manager/internal/wg"
	"ledez.net/tun-manager/internal/wgconf"
)

// Every key, address and name below is invented. Addresses come from the
// ranges reserved for documentation (RFC 5737).
const (
	alphaKey = "JTI/TFlmc4CNmqe0wc7b6PUCDxwpNkNQXWp3hJGeq7g="
	bravoKey = "SldkcX6LmKWyv8zZ5vMADRonNEFOW2h1go+cqbbD0N0="
	deltaKey = "lKGuu8jV4u/8CRYjMD1KV2RxfouYpbK/zNnm8wANGic="
)

var confs = map[string]string{
	"alpha": `# TO_CHECK=10.20.30.1
[Interface]
Address = 10.20.30.2/32
[Peer]
PublicKey = ` + alphaKey + `
Endpoint = 192.0.2.10:51820
AllowedIPs = 10.20.30.0/24
`,
	"bravo": `# TO_CHECK=10.20.31.1
[Interface]
Address = 10.20.31.2/32
[Peer]
PublicKey = ` + bravoKey + `
Endpoint = 198.51.100.20:51821
AllowedIPs = 10.20.31.1/32
`,
	"delta": `# TO_CHECK=198.51.100.1
[Interface]
Address = 10.20.33.2/32
[Peer]
PublicKey = ` + deltaKey + `
Endpoint = 192.0.2.40:51823
AllowedIPs = 198.51.100.0/24
`,
}

func configDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range confs {
		if err := os.WriteFile(filepath.Join(dir, name+".conf"), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

func testConfig(t *testing.T) *profile.Config {
	cfg := profile.Default()
	cfg.ConfigDir = configDir(t)
	cfg.Contexts = []netctx.Rule{{
		Name:       "office",
		Interfaces: []string{"en0"},
		CIDR:       "198.51.100.0/24",
	}}
	cfg.Groups = map[string][]string{
		profile.GroupNeeded: {"alpha", "bravo"},
		profile.GroupExtra:  {},
		profile.GroupAll:    {"delta", "alpha", "bravo"},
	}
	cfg.Overrides = []profile.Override{{
		Tunnel:    "delta",
		GroupWhen: map[string]string{"office": profile.GroupExtra, netctx.Default: profile.GroupNeeded},
	}}
	return cfg
}

type stateFunc func() (wg.State, error)

func (f stateFunc) Read() (wg.State, error) { return f() }

func upState(keys ...string) stateFunc {
	return func() (wg.State, error) {
		var s wg.State
		for i, k := range keys {
			s = append(s, wg.Peer{
				PublicKey:     k,
				Device:        "utun" + string(rune('0'+i)),
				LastHandshake: time.Now(),
				RxBytes:       int64(100 * (i + 1)),
			})
		}
		return s, nil
	}
}

type fakeRunner struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, strings.Join(append([]string{filepath.Base(name)}, args...), " "))
	return "", r.err
}

func (r *fakeRunner) actions() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		fields := strings.Fields(c)
		out[i] = fields[1] + " " + strings.TrimSuffix(filepath.Base(fields[2]), ".conf")
	}
	return out
}

func atOffice() netctx.Lister {
	return func() ([]netctx.Iface, error) {
		return []netctx.Iface{{Name: "en0", Addrs: []netip.Prefix{netip.MustParsePrefix("198.51.100.42/24")}}}, nil
	}
}

func away() netctx.Lister {
	return func() ([]netctx.Iface, error) {
		return []netctx.Iface{{Name: "en0", Addrs: []netip.Prefix{netip.MustParsePrefix("203.0.113.9/24")}}}, nil
	}
}

func newApp(t *testing.T, reader wg.Reader, runner wg.Runner, lister netctx.Lister) *App {
	t.Helper()
	cfg := testConfig(t)
	return &App{
		Config: cfg,
		Reader: reader,
		Lister: lister,
		// These tests identify tunnels by public key; the run-directory locator
		// is exercised on its own.
		Locator: blindLocator{},
		Control: &wg.Controller{
			Check:   installedWgQuick,
			WgQuick: "/usr/bin/wg-quick",
			Runner:  runner,
		},
	}
}

func TestViewMarksLiveTunnelsUpAndOthersDown(t *testing.T) {
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	byName := map[string]Row{}
	for _, r := range view.Rows {
		byName[r.Tunnel.Name] = r
	}
	if got := byName["alpha"].Health; got != wg.Up {
		t.Errorf("alpha health = %v, want %v", got, wg.Up)
	}
	if got := byName["bravo"].Health; got != wg.Down {
		t.Errorf("bravo health = %v, want %v", got, wg.Down)
	}
}

func TestViewCarriesTheLivePeerCounters(t *testing.T) {
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	for _, r := range view.Rows {
		if r.Tunnel.Name != "alpha" {
			continue
		}
		if r.Peer.RxBytes != 100 {
			t.Errorf("RxBytes = %d, want 100", r.Peer.RxBytes)
		}
		if r.Peer.Device == "" {
			t.Error("Device is empty, want the live utun name")
		}
		return
	}
	t.Fatal("alpha missing from the view")
}

func TestViewGroupsFollowTheNetworkContext(t *testing.T) {
	home := newApp(t, upState(), &fakeRunner{}, atOffice())
	homeView, err := home.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if got := groupOf(homeView, "delta"); got != profile.GroupExtra {
		t.Errorf("in the office, delta group = %q, want %q", got, profile.GroupExtra)
	}

	roaming := newApp(t, upState(), &fakeRunner{}, away())
	awayView, err := roaming.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if got := groupOf(awayView, "delta"); got != profile.GroupNeeded {
		t.Errorf("elsewhere, delta group = %q, want %q", got, profile.GroupNeeded)
	}
}

func TestViewReportsTheDetectedContext(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, atOffice())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if view.Context.Name != "office" || view.Context.Interface != "en0" {
		t.Errorf("Context = %+v, want office on en0", view.Context)
	}
}

func TestViewOrdersNeededBeforeExtra(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, atOffice())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	var seenExtra bool
	for _, r := range view.Rows {
		if r.Group == profile.GroupExtra {
			seenExtra = true
		}
		if seenExtra && r.Group == profile.GroupNeeded {
			t.Fatalf("row order = %v, want every needed tunnel before the extras", names(view))
		}
	}
}

func TestViewPropagatesReaderErrors(t *testing.T) {
	boom := errors.New("no wireguard socket")
	a := newApp(t, stateFunc(func() (wg.State, error) { return nil, boom }), &fakeRunner{}, away())

	if _, err := a.View(); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap %v", err, boom)
	}
}

func TestUpGroupStartsOnlyTheTunnelsThatAreDown(t *testing.T) {
	runner := &fakeRunner{}
	a := newApp(t, upState(alphaKey), runner, away())

	results, err := a.UpGroup(context.Background(), profile.GroupNeeded)
	if err != nil {
		t.Fatalf("UpGroup: %v", err)
	}

	// away: needed = alpha, bravo, delta. alpha is already up.
	got := runner.actions()
	want := []string{"up bravo", "up delta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("commands = %v, want %v", got, want)
	}
	if len(results) != len(want) {
		t.Errorf("len(results) = %d, want %d", len(results), len(want))
	}
}

func TestDownAllStopsOnlyLiveTunnels(t *testing.T) {
	runner := &fakeRunner{}
	a := newApp(t, upState(alphaKey, deltaKey), runner, away())

	if _, err := a.DownAll(context.Background()); err != nil {
		t.Fatalf("DownAll: %v", err)
	}

	got := runner.actions()
	want := []string{"down delta", "down alpha"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("commands = %v, want %v (the `all` group order)", got, want)
	}
}

func TestDownAllIsANoOpWhenEverythingIsAlreadyDown(t *testing.T) {
	runner := &fakeRunner{}
	a := newApp(t, upState(), runner, away())

	results, err := a.DownAll(context.Background())
	if err != nil {
		t.Fatalf("DownAll: %v", err)
	}

	if len(runner.calls) != 0 {
		t.Errorf("commands = %v, want none", runner.actions())
	}
	if len(results) != 0 {
		t.Errorf("results = %v, want none", results)
	}
}

func TestToggleStopsALiveTunnel(t *testing.T) {
	runner := &fakeRunner{}
	a := newApp(t, upState(alphaKey), runner, away())

	if _, err := a.Toggle(context.Background(), "alpha"); err != nil {
		t.Fatalf("Toggle: %v", err)
	}

	if got := runner.actions(); strings.Join(got, ",") != "down alpha" {
		t.Errorf("commands = %v, want [down alpha]", got)
	}
}

func TestToggleStartsADeadTunnel(t *testing.T) {
	runner := &fakeRunner{}
	a := newApp(t, upState(), runner, away())

	if _, err := a.Toggle(context.Background(), "bravo"); err != nil {
		t.Fatalf("Toggle: %v", err)
	}

	if got := runner.actions(); strings.Join(got, ",") != "up bravo" {
		t.Errorf("commands = %v, want [up bravo]", got)
	}
}

func TestToggleRejectsAnUnknownTunnel(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, away())

	if _, err := a.Toggle(context.Background(), "ghost"); err == nil {
		t.Fatal("Toggle succeeded on an unknown tunnel, want error")
	}
}

func TestAnyUpReflectsTheLiveState(t *testing.T) {
	live := newApp(t, upState(alphaKey), &fakeRunner{}, away())
	view, err := live.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if !view.AnyUp() {
		t.Error("AnyUp = false, want true when a tunnel is live")
	}

	dead := newApp(t, upState(), &fakeRunner{}, away())
	view, err = dead.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}
	if view.AnyUp() {
		t.Error("AnyUp = true, want false when everything is down")
	}
}

func groupOf(v View, name string) string {
	for _, r := range v.Rows {
		if r.Tunnel.Name == name {
			return r.Group
		}
	}
	return "<missing>"
}

func names(v View) []string {
	out := make([]string, len(v.Rows))
	for i, r := range v.Rows {
		out[i] = r.Tunnel.Name + ":" + r.Group
	}
	return out
}

// fakeLocator answers like /var/run/wireguard does: a name per live tunnel.
type fakeLocator map[string]string

func (l fakeLocator) Device(tunnel string) (string, bool) { return l[tunnel], true }

type blindLocator struct{}

func (blindLocator) Device(string) (string, bool) { return "", false }

func TestViewDistinguishesTwoTunnelsSharingAPeerKey(t *testing.T) {
	// delta and delta6 point at the same server through different endpoints,
	// so they carry the same peer public key. Only one of them is up.
	dir := configDir(t)
	if err := os.WriteFile(filepath.Join(dir, "delta6.conf"), []byte(confs["delta"]), 0o600); err != nil {
		t.Fatalf("write delta6: %v", err)
	}

	cfg := profile.Default()
	cfg.ConfigDir = dir
	cfg.Groups = map[string][]string{profile.GroupAll: {"delta"}}
	cfg.Overrides = nil

	a := &App{
		Config:  cfg,
		Reader:  upState(deltaKey),
		Lister:  away(),
		Locator: fakeLocator{"delta": "utun0"},
		Control: &wg.Controller{WgQuick: "wg-quick", Runner: &fakeRunner{}, Check: installedWgQuick},
	}

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	delta, _ := view.Row("delta")
	if delta.Health != wg.Up {
		t.Errorf("delta health = %v, want %v", delta.Health, wg.Up)
	}
	delta6, ok := view.Row("delta6")
	if !ok {
		t.Fatal("delta6 missing from the view")
	}
	if delta6.Health != wg.Down {
		t.Errorf("delta6 health = %v, want %v: it has no interface of its own", delta6.Health, wg.Down)
	}
	if delta6.Peer.Device != "" {
		t.Errorf("delta6 device = %q, want empty", delta6.Peer.Device)
	}
}

func TestViewFallsBackToThePeerKeyWithoutALocator(t *testing.T) {
	// When /var/run/wireguard cannot be read, matching by public key is the
	// only thing left.
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())
	a.Locator = blindLocator{}

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	alpha, _ := view.Row("alpha")
	if alpha.Health != wg.Up {
		t.Errorf("alpha health = %v, want %v", alpha.Health, wg.Up)
	}
}

func TestCheckIPsCoverOnlyLiveTunnels(t *testing.T) {
	// Two configs may share a check address. Probing a down tunnel would show
	// the latency of whichever sibling is up, which reads as if it were up too.
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	got := view.CheckIPs()

	if len(got) != 1 {
		t.Fatalf("CheckIPs = %v, want only the live tunnel's address", got)
	}
	if got[0] != "10.20.30.1" {
		t.Errorf("CheckIPs = %v, want alpha's check address", got)
	}
}

func TestCheckIPsAreEmptyWhenNothingIsUp(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, away())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if got := view.CheckIPs(); len(got) != 0 {
		t.Errorf("CheckIPs = %v, want none", got)
	}
}

func TestHealthMapsEveryTunnelByName(t *testing.T) {
	// This map is what notification diffing compares between two refreshes.
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	got := view.Health()

	if len(got) != len(view.Rows) {
		t.Fatalf("len = %d, want one entry per row (%d)", len(got), len(view.Rows))
	}
	if got["alpha"] != wg.Up {
		t.Errorf("alpha = %v, want %v", got["alpha"], wg.Up)
	}
	if got["bravo"] != wg.Down {
		t.Errorf("bravo = %v, want %v", got["bravo"], wg.Down)
	}
}

func TestRowMissesAnUnknownTunnel(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, away())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if _, ok := view.Row("ghost"); ok {
		t.Error("Row found an unknown tunnel")
	}
}

func TestDownRejectsAnUnknownTunnel(t *testing.T) {
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if _, err := a.Down(context.Background(), view, []string{"ghost"}); err == nil {
		t.Fatal("Down succeeded on an unknown tunnel, want an error")
	}
}

func TestDownSkipsATunnelThatIsAlreadyStopped(t *testing.T) {
	runner := &fakeRunner{}
	a := newApp(t, upState(alphaKey), runner, away())

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	results, err := a.Down(context.Background(), view, []string{"alpha", "bravo"})
	if err != nil {
		t.Fatalf("Down: %v", err)
	}

	if got := runner.actions(); strings.Join(got, ",") != "down alpha" {
		t.Errorf("commands = %v, want only the live tunnel stopped", got)
	}
	if len(results) != 1 {
		t.Errorf("len(results) = %d, want 1", len(results))
	}
}

func TestUpGroupRejectsAnUnknownGroup(t *testing.T) {
	runner := &fakeRunner{}
	a := newApp(t, upState(), runner, away())

	results, err := a.UpGroup(context.Background(), "nosuchgroup")
	if err != nil {
		t.Fatalf("UpGroup: %v", err)
	}

	if len(results) != 0 || len(runner.actions()) != 0 {
		t.Errorf("an unknown group acted: results=%v commands=%v", results, runner.actions())
	}
}

func TestViewUsesTheInjectedClock(t *testing.T) {
	frozen := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	a := newApp(t, upState(), &fakeRunner{}, away())
	a.Now = func() time.Time { return frozen }

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if !view.Taken.Equal(frozen) {
		t.Errorf("Taken = %v, want %v", view.Taken, frozen)
	}
}

func TestViewFallsBackToTheSystemListerAndLocator(t *testing.T) {
	// Without injection the application must still read the real host rather
	// than crash on a nil field.
	cfg := testConfig(t)
	a := &App{
		Config:  cfg,
		Reader:  upState(),
		Control: &wg.Controller{WgQuick: "wg-quick", Runner: &fakeRunner{}, Check: installedWgQuick},
	}

	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if len(view.Rows) == 0 {
		t.Error("no row, want the configs listed")
	}
}

// brokenApp cannot build a view: its config directory holds no tunnel.
func brokenApp(t *testing.T) *App {
	t.Helper()
	cfg := profile.Default()
	cfg.ConfigDir = t.TempDir()
	return &App{
		Config:  cfg,
		Reader:  upState(),
		Lister:  away(),
		Locator: blindLocator{},
		Control: &wg.Controller{WgQuick: "wg-quick", Runner: &fakeRunner{}, Check: installedWgQuick},
	}
}

func TestViewFailsWhenTheConfigsCannotBeRead(t *testing.T) {
	if _, err := brokenApp(t).View(); err == nil {
		t.Fatal("View succeeded with no tunnel to read, want an error")
	}
}

func TestViewFailsOnAnUnusableNetworkRule(t *testing.T) {
	// The contexts come from the user's YAML, so a malformed CIDR reaches the
	// detection rather than being caught at load time.
	a := newApp(t, upState(), &fakeRunner{}, away())
	a.Config.Contexts = []netctx.Rule{{Name: "office", CIDR: "not-a-cidr"}}

	if _, err := a.View(); err == nil {
		t.Fatal("View succeeded with a malformed CIDR, want an error")
	}
}

func TestUpGroupFailsWhenTheViewDoes(t *testing.T) {
	runner := &fakeRunner{}
	a := brokenApp(t)
	a.Control = &wg.Controller{WgQuick: "wg-quick", Runner: runner, Check: installedWgQuick}

	if _, err := a.UpGroup(context.Background(), profile.GroupNeeded); err == nil {
		t.Fatal("UpGroup succeeded on an unreadable configuration, want an error")
	}
	if len(runner.calls) != 0 {
		t.Errorf("commands = %v, want none: nothing may run on a state nobody could read", runner.actions())
	}
}

func TestDownAllFailsWhenTheViewDoes(t *testing.T) {
	if _, err := brokenApp(t).DownAll(context.Background()); err == nil {
		t.Fatal("DownAll succeeded on an unreadable configuration, want an error")
	}
}

func TestToggleFailsWhenTheViewDoes(t *testing.T) {
	if _, err := brokenApp(t).Toggle(context.Background(), "alpha"); err == nil {
		t.Fatal("Toggle succeeded on an unreadable configuration, want an error")
	}
}

func TestUpRejectsAnUnknownTunnel(t *testing.T) {
	// Covered from main_test.go too, but go test only credits the package
	// under test, and this branch belongs to this one.
	a := newApp(t, upState(), &fakeRunner{}, away())
	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	if _, err := a.Up(context.Background(), view, []string{"ghost"}); err == nil {
		t.Fatal("Up succeeded on an unknown tunnel, want an error")
	}
}

func TestPlanUpSkipsWhatIsAlreadyUp(t *testing.T) {
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())
	view, err := a.View()
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	steps := view.PlanUp([]string{"alpha", "bravo"})

	if len(steps) != 1 || steps[0].Tunnel.Name != "bravo" || steps[0].Action != ActionUp {
		t.Errorf("steps = %+v, want bravo alone coming up", steps)
	}
}

func TestPlanDownSkipsWhatIsAlreadyDown(t *testing.T) {
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())
	view, _ := a.View()

	steps := view.PlanDown([]string{"alpha", "bravo"})

	if len(steps) != 1 || steps[0].Tunnel.Name != "alpha" || steps[0].Action != ActionDown {
		t.Errorf("steps = %+v, want alpha alone going down", steps)
	}
}

func TestPlanToggleReadsEachTunnelState(t *testing.T) {
	// What a tunnel will do is known from the table, before anything runs.
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())
	view, _ := a.View()

	steps := view.PlanToggle([]string{"alpha", "bravo"})

	if len(steps) != 2 {
		t.Fatalf("steps = %+v, want one per tunnel", steps)
	}
	if steps[0].Action != ActionDown || steps[1].Action != ActionUp {
		t.Errorf("actions = %q, %q, want down then up", steps[0].Action, steps[1].Action)
	}
}

func TestPlanSkipsATunnelItHasNeverHeardOf(t *testing.T) {
	// A group naming a tunnel with no configuration must not stop the tunnels
	// that do have one.
	a := newApp(t, upState(), &fakeRunner{}, away())
	view, _ := a.View()

	if steps := view.PlanUp([]string{"ghost", "alpha"}); len(steps) != 1 {
		t.Errorf("steps = %+v, want the ghost skipped and alpha kept", steps)
	}
	if steps := view.PlanDown([]string{"ghost"}); len(steps) != 0 {
		t.Errorf("steps = %+v, want none", steps)
	}
	if steps := view.PlanToggle([]string{"ghost"}); len(steps) != 0 {
		t.Errorf("steps = %+v, want none", steps)
	}
}

func TestUnknownNamesTheTunnelsTheTableHasNeverHeardOf(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, away())
	view, _ := a.View()

	got := view.Unknown([]string{"alpha", "ghost", "bravo"})

	if len(got) != 1 || got[0] != "ghost" {
		t.Errorf("Unknown = %v, want only the ghost", got)
	}
}

func TestRunBatchReportsEachTunnelStartingAndFinishing(t *testing.T) {
	runner := &fakeRunner{}
	a := newApp(t, upState(), runner, away())
	a.Stagger = time.Millisecond
	view, _ := a.View()
	steps := view.PlanUp([]string{"alpha", "bravo"})

	var started, finished []string
	for e := range a.RunBatch(context.Background(), steps) {
		switch e.Phase {
		case Started:
			started = append(started, e.Tunnel)
		case Finished:
			finished = append(finished, e.Tunnel)
		}
	}

	if len(started) != 2 || len(finished) != 2 {
		t.Errorf("started %v, finished %v, want both tunnels in each", started, finished)
	}
}

func TestRunBatchRunsTheTunnelsAtTheSameTime(t *testing.T) {
	// Eight tunnels one after another is eight times as long as one. The whole
	// point of the batch is that they do not wait for each other.
	var live, peak atomic.Int32
	runner := runnerFunc(func() {
		n := live.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		live.Add(-1)
	})
	a := newApp(t, upState(), runner, away())
	a.Stagger = time.Millisecond
	view, _ := a.View()
	steps := view.PlanUp([]string{"alpha", "bravo", "delta"})

	for range a.RunBatch(context.Background(), steps) {
	}

	if peak.Load() < 2 {
		t.Errorf("peak concurrency = %d, want the tunnels to overlap", peak.Load())
	}
}

func TestRunBatchStaggersTheLaunches(t *testing.T) {
	// A few milliseconds apart, so that two wg-quick runs do not reach the
	// routing table in the same instant.
	var starts []time.Time
	var mu sync.Mutex
	runner := runnerFunc(func() {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
	})
	a := newApp(t, upState(), runner, away())
	a.Stagger = 40 * time.Millisecond
	view, _ := a.View()
	steps := view.PlanUp([]string{"alpha", "bravo"})

	for range a.RunBatch(context.Background(), steps) {
	}

	if len(starts) != 2 {
		t.Fatalf("got %d launches, want 2", len(starts))
	}
	if gap := starts[1].Sub(starts[0]); gap < 20*time.Millisecond {
		t.Errorf("launches are %v apart, want them staggered", gap)
	}
}

func TestRunBatchOfNothingClosesAtOnce(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, away())

	for range a.RunBatch(context.Background(), nil) {
		t.Error("an empty batch reported something")
	}
}

func TestRunBatchStopsWithItsContext(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, away())
	a.Stagger = time.Hour // the batch can only end by being cancelled
	view, _ := a.View()
	steps := view.PlanUp([]string{"alpha", "bravo", "delta"})

	ctx, cancel := context.WithCancel(context.Background())
	events := a.RunBatch(ctx, steps)
	cancel()

	done := make(chan struct{})
	go func() {
		for range events {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the batch did not stop when its context was cancelled")
	}
}

// runnerFunc runs a side effect and reports success, for tests that care about
// when a command ran rather than what it was.
func runnerFunc(fn func()) wg.Runner {
	return runnerFn(func(context.Context, string, ...string) (string, error) {
		fn()
		return "", nil
	})
}

type runnerFn func(context.Context, string, ...string) (string, error)

func (f runnerFn) Run(ctx context.Context, name string, args ...string) (string, error) {
	return f(ctx, name, args...)
}

func TestABatchWithoutAStaggerUsesTheDefault(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, away())

	if got := a.stagger(); got != DefaultStagger {
		t.Errorf("stagger = %v, want %v", got, DefaultStagger)
	}
}

func TestDownAllSkipsAGroupMemberWithNoConfiguration(t *testing.T) {
	// The `all` group is a list of things to tear down; one that has no
	// configuration is already down, and refusing the whole batch over it would
	// leave the rest running.
	runner := &fakeRunner{}
	a := newApp(t, upState(alphaKey), runner, away())
	a.Config.Groups[profile.GroupAll] = []string{"ghost", "alpha"}

	if _, err := a.DownAll(context.Background()); err != nil {
		t.Fatalf("DownAll: %v", err)
	}

	if got := runner.actions(); strings.Join(got, ",") != "down alpha" {
		t.Errorf("commands = %v, want the known tunnel stopped", got)
	}
}

func TestSampleReadsTheCountersOfOneTunnel(t *testing.T) {
	// A graph asks for this every second, so it reads the control socket alone
	// and never the configuration on disk.
	a := newApp(t, upState(alphaKey), &fakeRunner{}, away())
	at := time.Date(2026, 8, 17, 10, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { return at }
	view, _ := a.View()
	row, _ := view.Row("alpha")

	got, ok := a.Sample(row.Tunnel)

	if !ok {
		t.Fatal("Sample found nothing for a live tunnel")
	}
	if got.Rx != 100 {
		t.Errorf("Rx = %d, want the live counter", got.Rx)
	}
	if !got.At.Equal(at) {
		t.Errorf("At = %v, want the injected clock", got.At)
	}
}

func TestSampleOfADownTunnelFindsNothing(t *testing.T) {
	a := newApp(t, upState(), &fakeRunner{}, away())
	view, _ := a.View()
	row, _ := view.Row("alpha")

	if _, ok := a.Sample(row.Tunnel); ok {
		t.Error("Sample found counters for a tunnel that is down")
	}
}

func TestSampleSurvivesAnUnreadableState(t *testing.T) {
	// The socket can go away under the program; a graph missing a point is not
	// worth an error path of its own.
	a := newApp(t, stateFunc(func() (wg.State, error) { return nil, errors.New("boom") }), &fakeRunner{}, away())

	if _, ok := a.Sample(wgconf.Tunnel{Name: "alpha"}); ok {
		t.Error("Sample reported a reading it could not take")
	}
}

// installedWgQuick stands in for the check wg.Controller makes on the binary
// before running it. These tests name a wg-quick that is not installed, because
// what they are about is everything around the call; the check has its own
// tests in internal/wg.
func installedWgQuick(string) error { return nil }
