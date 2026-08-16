package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ledez.net/tun-manager/internal/netctx"
)

// Every value below is invented. Addresses come from the ranges reserved for
// documentation (RFC 5737).
const sampleYAML = `
config_dir: /etc/wireguard
wg_quick: /usr/bin/wg-quick
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

	if cfg.ConfigDir != "/etc/wireguard" {
		t.Errorf("ConfigDir = %q", cfg.ConfigDir)
	}
	if cfg.WgQuick != "/usr/bin/wg-quick" {
		t.Errorf("WgQuick = %q", cfg.WgQuick)
	}
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

	if cfg.ConfigDir == "" || cfg.WgQuick == "" || cfg.RunDir == "" {
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

	if cfg.ConfigDir != DefaultConfigDir {
		t.Errorf("ConfigDir = %q, want the default %q", cfg.ConfigDir, DefaultConfigDir)
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
