package profile

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"ledez.net/tun-manager/internal/netctx"
)

// examplePath is the configuration shipped with the program, read as part of
// the repository rather than as something the machine happens to have.
const examplePath = "../../configs/config.example.yaml"

// Every value below is invented. Addresses come from the ranges reserved for
// documentation (RFC 5737).
const sampleYAML = `
refresh_interval: 5m
notify: true

contexts:
  - name: office
    interfaces: [en0, en10]
    cidr: 198.51.100.0/24

groups:
  needed: [alpha, bravo]
  extra: [charlie, echo]
  all: [delta, alpha, bravo, charlie, echo]

overrides:
  - tunnel: delta
    group_when:
      office: extra
      default: needed
`

func loadString(t *testing.T, yaml string) *Config {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return cfg
}

func TestLoadReadsScalarSettings(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	if cfg.RefreshInterval != 5*time.Minute {
		t.Errorf("RefreshInterval = %v, want 5m", cfg.RefreshInterval)
	}
	if !cfg.Notify {
		t.Error("Notify = false, want true")
	}
	if len(cfg.Contexts) != 1 || cfg.Contexts[0].Name != "office" {
		t.Errorf("Contexts = %+v", cfg.Contexts)
	}
}

func TestGroupOfUsesOverrideOutsideTheNamedContext(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	if got := cfg.GroupOf("delta", netctx.Default); got != "needed" {
		t.Errorf("GroupOf(delta, default) = %q, want %q", got, "needed")
	}
}

func TestGroupOfUsesOverrideInsideTheNamedContext(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	if got := cfg.GroupOf("delta", "office"); got != "extra" {
		t.Errorf("GroupOf(delta, office) = %q, want %q", got, "extra")
	}
}

func TestGroupOfReturnsStaticMembership(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	if got := cfg.GroupOf("alpha", "office"); got != "needed" {
		t.Errorf("GroupOf(alpha, office) = %q, want %q", got, "needed")
	}
	if got := cfg.GroupOf("charlie", netctx.Default); got != "extra" {
		t.Errorf("GroupOf(charlie, default) = %q, want %q", got, "extra")
	}
}

func TestGroupOfIsEmptyForUnlistedTunnel(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	if got := cfg.GroupOf("foxtrot", "office"); got != "" {
		t.Errorf("GroupOf(foxtrot, office) = %q, want empty", got)
	}
}

func TestGroupOfIgnoresTheAllGroup(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	// Every tunnel is in "all"; that must never be reported as its group.
	if got := cfg.GroupOf("echo", "office"); got != "extra" {
		t.Errorf("GroupOf(echo, office) = %q, want %q", got, "extra")
	}
}

func TestMembersAddsOverriddenTunnelOutsideTheContext(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	got := cfg.Members("needed", netctx.Default)

	want := []string{"alpha", "bravo", "delta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Members(needed, default) = %v, want %v", got, want)
	}
}

func TestMembersDropsOverriddenTunnelInsideTheContext(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	for _, name := range cfg.Members("needed", "office") {
		if name == "delta" {
			t.Fatalf("Members(needed, office) = %v, must not contain delta", cfg.Members("needed", "office"))
		}
	}
	var found bool
	for _, name := range cfg.Members("extra", "office") {
		if name == "delta" {
			found = true
		}
	}
	if !found {
		t.Errorf("Members(extra, office) = %v, want it to contain delta", cfg.Members("extra", "office"))
	}
}

func TestMembersOfAllIsVerbatimAndContextIndependent(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	office := cfg.Members(GroupAll, "office")
	elsewhere := cfg.Members(GroupAll, netctx.Default)

	if strings.Join(office, ",") != strings.Join(elsewhere, ",") {
		t.Errorf("Members(all) differs by context: %v vs %v", office, elsewhere)
	}
	if office[0] != "delta" {
		t.Errorf("Members(all)[0] = %q, want the YAML order preserved", office[0])
	}
}

func TestLoadMissingFileYieldsUsableDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "absent.yaml"))
	if err != nil {
		t.Fatalf("Load of a missing file must not fail: %v", err)
	}

	if cfg.ConfigDir == "" {
		t.Errorf("defaults incomplete: %+v", cfg)
	}
	if cfg.RefreshInterval != DefaultRefresh {
		t.Errorf("RefreshInterval = %v, want %v", cfg.RefreshInterval, DefaultRefresh)
	}
	if !cfg.IsDefault {
		t.Error("IsDefault = false, want true when no file was read")
	}
}

func TestDefaultsDescribeNoTunnel(t *testing.T) {
	// Groups and contexts describe one person's setup; they belong in the
	// user's file, never in the binary.
	cfg := Default()

	if cfg.HasGroups() {
		t.Errorf("Groups = %v, Overrides = %v, want both empty", cfg.Groups, cfg.Overrides)
	}
	if len(cfg.Contexts) != 0 {
		t.Errorf("Contexts = %+v, want none", cfg.Contexts)
	}
}

func TestHasGroupsSeesAConfiguredGroup(t *testing.T) {
	cfg := loadString(t, sampleYAML)

	if !cfg.HasGroups() {
		t.Error("HasGroups = false, want true for a file that defines groups")
	}
}

func TestHasGroupsSeesAnOverrideOnItsOwn(t *testing.T) {
	cfg := loadString(t, "overrides:\n  - tunnel: delta\n    group_when:\n      default: needed\n")

	if !cfg.HasGroups() {
		t.Error("HasGroups = false, want true when an override assigns a group")
	}
}

func TestLoadFillsMissingFieldsWithDefaults(t *testing.T) {
	cfg := loadString(t, "groups:\n  needed: [alpha]\n")

	if cfg.ConfigDir != ConfigDir {
		t.Errorf("ConfigDir = %q, want the default %q", cfg.ConfigDir, ConfigDir)
	}
	if cfg.RefreshInterval != DefaultRefresh {
		t.Errorf("RefreshInterval = %v, want %v", cfg.RefreshInterval, DefaultRefresh)
	}
	if cfg.IsDefault {
		t.Error("IsDefault = true, want false when a file was read")
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("groups: [oops\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Load(path); err == nil {
		t.Fatal("Load succeeded on malformed YAML, want error")
	}
}

func TestGroupOfFallsBackToTheDefaultKey(t *testing.T) {
	// An override lists the contexts it cares about. On any other network the
	// default key decides, which is what makes a two-line override enough.
	cfg := loadString(t, sampleYAML)

	if got := cfg.GroupOf("delta", "airport"); got != "needed" {
		t.Errorf("GroupOf(delta, airport) = %q, want the default key %q", got, "needed")
	}
}

func TestGroupOfIsEmptyWhenTheOverrideHasNoDefault(t *testing.T) {
	cfg := loadString(t, `
groups:
  needed: [alpha]
overrides:
  - tunnel: delta
    group_when:
      office: extra
`)

	if got := cfg.GroupOf("delta", "airport"); got != "" {
		t.Errorf("GroupOf(delta, airport) = %q, want empty", got)
	}
}

func TestLoadReportsAReadFailureThatIsNotAMissingFile(t *testing.T) {
	// A missing file is the normal case and yields the defaults. Anything else
	// is a real problem and must not be mistaken for one.
	dir := t.TempDir()

	if _, err := Load(dir); err == nil {
		t.Fatal("Load succeeded on a directory, want an error")
	}
}

func TestAConfigurationFileWithoutTheFeedKeyStillGetsTheFeed(t *testing.T) {
	// The keys are optional and what is left out falls back to the built-in
	// default — which for a bool means the zero value unless Load is careful,
	// and false is the opposite of what Default says. This is not theoretical:
	// it turned the feed off for every configuration written before the feed
	// existed, silently, with the menu bar reporting "not running" while
	// tun-manager was running.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("notify: true\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Notify {
		t.Error("Notify = false, want the default for a key the file does not mention")
	}
}

func TestAConfigurationFileWithoutTheNotifyKeyStillNotifies(t *testing.T) {
	// The same trap, and it has been there longer.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("notify: true\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Notify {
		t.Error("Notify = false, want the default for a key the file does not mention")
	}
}

func TestANotificationTurnedOffInTheFileStaysOff(t *testing.T) {
	// The other half: a default that cannot be overridden is not a default.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("notify: false\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Notify {
		t.Error("Notify = true, want it off")
	}
}

func TestAFieldWrittenOutAsBlankFallsBackToItsDefault(t *testing.T) {
	// Not the same as leaving a key out, which Load handles by starting from the
	// defaults. This is a document that mentions a key and gives it nothing —
	// a mistake rather than an intent, and answered with the default rather
	// than with a confusing failure much later.
	body := "refresh_interval: 0s\ngroups:\n"
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	d := Default()
	if cfg.ConfigDir != d.ConfigDir {
		t.Errorf("ConfigDir = %q, want the default %q", cfg.ConfigDir, d.ConfigDir)
	}
	if cfg.RefreshInterval != d.RefreshInterval {
		t.Errorf("RefreshInterval = %v, want %v", cfg.RefreshInterval, d.RefreshInterval)
	}
	if cfg.Groups == nil {
		t.Error("Groups is nil, want an empty map so the group commands have something to read")
	}
}

// The shipped example is what a new user copies, so it is part of the program
// rather than documentation about it. Nothing checked it until `feed:` was
// missing from it for as long as the feed had existed — and every configuration
// derived from it therefore had the feed off.
func TestTheShippedExampleParses(t *testing.T) {
	cfg, err := Load(examplePath)
	if err != nil {
		t.Fatalf("the example does not load: %v", err)
	}

	if cfg.ConfigDir == "" {
		t.Errorf("paths are empty: %+v", cfg)
	}
	if len(cfg.Groups[GroupAll]) == 0 {
		t.Error("the example documents an `all` group and does not define one")
	}
	if len(cfg.Contexts) == 0 {
		t.Error("the example documents contexts and defines none")
	}
	if len(cfg.Overrides) == 0 {
		t.Error("the example documents overrides and defines none")
	}
}

func TestTheShippedExampleHasNoKeyThisProgramIgnores(t *testing.T) {
	// A misspelled key is accepted in silence by default, which is how somebody
	// sets `feeed: false` and spends an evening wondering why it did nothing.
	data, err := os.ReadFile(examplePath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		t.Errorf("the example carries a key the program does not know: %v", err)
	}
}

func TestAKeyThisProgramDoesNotKnowIsRefused(t *testing.T) {
	// yaml ignores an unrecognised key by default, which is how somebody writes
	// `feeed: false`, watches nothing happen, and has no way to find out why.
	// Refusing costs a clear failure at startup instead of a silent one later.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("notify: true\nfeeed: false\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, err := Load(path)

	if err == nil {
		t.Fatal("Load accepted a key it does not know")
	}
	if !strings.Contains(err.Error(), "feeed") {
		t.Errorf("error = %v, want it to name the key", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error = %v, want it to name the file", err)
	}
}

func TestAnEmptyConfigurationFileIsAllDefaults(t *testing.T) {
	// A file somebody created and has not filled in yet. Not an error, and in
	// particular not an unknown-key error.
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Notify || cfg.ConfigDir != ConfigDir {
		t.Errorf("cfg = %+v, want the built-in defaults", cfg)
	}
}

func TestAFileThatIsOnlyCommentsIsAllDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("# nothing set yet\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Notify {
		t.Errorf("cfg = %+v, want the built-in defaults", cfg)
	}
}

func TestLoadRefusesConfigDirAndSaysWhereItWent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("config_dir: /Users/someone/wireguard\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err == nil {
		t.Fatalf("Load = %v, want a refusal: config_dir decides what root reads", cfg)
	}

	// Somebody upgrading reads this message and nothing else. It has to name
	// the key, say where the directory is now, and say why it moved.
	message := err.Error()
	for _, want := range []string{"config_dir", ConfigDir, path} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not contain %q", message, want)
		}
	}
}

func TestTheConfigDirectoryIsTheFixedOneByDefault(t *testing.T) {
	cfg := loadString(t, "notify: true\n")

	if cfg.ConfigDir != ConfigDir {
		t.Errorf("ConfigDir = %q, want the fixed %q", cfg.ConfigDir, ConfigDir)
	}
}

func TestLoadRefusesEveryKeyThatMovedToTheRootOnlyFile(t *testing.T) {
	// Each of these decided something root would then do. A file a plain user
	// can write must not be able to say any of it, so the key is refused by
	// name rather than quietly ignored — somebody upgrading has a file that
	// used to work, and needs to be told what to do with it.
	for key, line := range map[string]string{
		"wg_quick":    "wg_quick: /tmp/anywhere/wg-quick\n",
		"run_dir":     "run_dir: /tmp/anywhere/wireguard\n",
		"feed":        "feed: false\n",
		"feed_socket": "feed_socket: /tmp/anywhere/feed.sock\n",
	} {
		t.Run(key, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")
			if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			cfg, err := Load(path)
			if err == nil {
				t.Fatalf("Load = %v, want %s refused", cfg, key)
			}
			for _, want := range []string{key, PrivilegedPath} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("message %q does not contain %q", err, want)
				}
			}
		})
	}
}
