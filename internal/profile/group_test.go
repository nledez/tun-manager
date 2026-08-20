package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig puts a configuration file in a temporary directory and returns
// its path.
func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func readConfig(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(data)
}

func TestAddToGroupAppendsTheTunnel(t *testing.T) {
	path := writeConfig(t, "groups:\n  all:\n    - alpha\n")

	if err := AddToGroup(path, GroupAll, "bravo"); err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Groups[GroupAll]; len(got) != 2 || got[0] != "alpha" || got[1] != "bravo" {
		t.Errorf("all = %v, want alpha then bravo", got)
	}
}

func TestAddToGroupKeepsEveryComment(t *testing.T) {
	// This is a file the user maintains by hand. Re-marshalling the Config
	// struct would produce a correct configuration that had lost all of this.
	body := `# tun-manager configuration
notify: true

groups:
  # everything, for the stop-all key
  all:
    - alpha # the important one
  needed: [alpha]
`
	path := writeConfig(t, body)

	if err := AddToGroup(path, GroupAll, "bravo"); err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}

	got := readConfig(t, path)
	for _, want := range []string{
		"# tun-manager configuration",
		"# everything, for the stop-all key",
		"# the important one",
		"notify: true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q was lost:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "bravo") {
		t.Errorf("bravo was not added:\n%s", got)
	}
}

func TestAddToGroupLeavesTheOtherGroupsAlone(t *testing.T) {
	path := writeConfig(t, "groups:\n  all:\n    - alpha\n  needed:\n    - alpha\n  extra:\n    - charlie\n")

	if err := AddToGroup(path, GroupAll, "bravo"); err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Groups[GroupNeeded]; len(got) != 1 || got[0] != "alpha" {
		t.Errorf("needed = %v, want it untouched", got)
	}
	if got := cfg.Groups[GroupExtra]; len(got) != 1 || got[0] != "charlie" {
		t.Errorf("extra = %v, want it untouched", got)
	}
}

func TestAddToGroupIsIdempotent(t *testing.T) {
	// Importing the same tunnel twice must not list it twice: the file is read
	// by a person as well as by the program.
	path := writeConfig(t, "groups:\n  all:\n    - alpha\n")

	for range 3 {
		if err := AddToGroup(path, GroupAll, "alpha"); err != nil {
			t.Fatalf("AddToGroup: %v", err)
		}
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Groups[GroupAll]; len(got) != 1 {
		t.Errorf("all = %v, want alpha once", got)
	}
}

func TestAddToGroupCreatesTheGroupWhenTheFileHasNone(t *testing.T) {
	path := writeConfig(t, "notify: true\n")

	if err := AddToGroup(path, GroupAll, "alpha"); err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Groups[GroupAll]; len(got) != 1 || got[0] != "alpha" {
		t.Errorf("all = %v, want alpha", got)
	}
	if !cfg.Notify {
		t.Error("notify = false, want the untouched key kept")
	}
}

func TestAddToGroupCreatesTheMemberListWhenTheKeyIsEmpty(t *testing.T) {
	// `all:` with nothing after it parses as null, not as an empty list.
	path := writeConfig(t, "groups:\n  all:\n")

	if err := AddToGroup(path, GroupAll, "alpha"); err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Groups[GroupAll]; len(got) != 1 || got[0] != "alpha" {
		t.Errorf("all = %v, want alpha", got)
	}
}

func TestAddToGroupWritesAConfigurationWhenThereIsNone(t *testing.T) {
	// A first import on a machine that never had a configuration file. Only
	// the group is written: spelling out today's built-in defaults would
	// freeze them into the user's file.
	path := filepath.Join(t.TempDir(), "sub", "config.yaml")

	if err := AddToGroup(path, GroupAll, "alpha"); err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Groups[GroupAll]; len(got) != 1 || got[0] != "alpha" {
		t.Errorf("all = %v, want alpha", got)
	}
	if strings.Contains(readConfig(t, path), "refresh_interval") {
		t.Errorf("the built-in defaults were written out:\n%s", readConfig(t, path))
	}
}

func TestAddToGroupWritesAConfigurationWhenTheFileIsEmpty(t *testing.T) {
	path := writeConfig(t, "")

	if err := AddToGroup(path, GroupAll, "alpha"); err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Groups[GroupAll]; len(got) != 1 || got[0] != "alpha" {
		t.Errorf("all = %v, want alpha", got)
	}
}

func TestAddToGroupKeepsTheFilesPermissions(t *testing.T) {
	path := writeConfig(t, "groups:\n  all:\n    - alpha\n")
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if err := AddToGroup(path, GroupAll, "bravo"); err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %o, want 600: the rewrite must not widen the file", got)
	}
}

func TestAddToGroupReportsAFileThatIsNotYAML(t *testing.T) {
	path := writeConfig(t, "groups: [unclosed\n")

	if err := AddToGroup(path, GroupAll, "alpha"); err == nil {
		t.Error("AddToGroup accepted a file that is not YAML")
	}
}

func TestAddToGroupReportsAConfigurationThatIsNotAMapping(t *testing.T) {
	path := writeConfig(t, "- alpha\n- bravo\n")

	if err := AddToGroup(path, GroupAll, "charlie"); err == nil {
		t.Error("AddToGroup accepted a list where a mapping belongs")
	}
}

func TestAddToGroupReportsGroupsThatIsNotAMapping(t *testing.T) {
	path := writeConfig(t, "groups:\n  - alpha\n")

	if err := AddToGroup(path, GroupAll, "bravo"); err == nil {
		t.Error("AddToGroup accepted a groups key that is not a mapping")
	}
}

func TestAddToGroupReportsAnUnreadableFile(t *testing.T) {
	// A directory where the file should be: readable by nothing.
	dir := t.TempDir()

	if err := AddToGroup(dir, GroupAll, "alpha"); err == nil {
		t.Error("AddToGroup accepted a directory as a configuration file")
	}
}

func TestAddToGroupReportsADirectoryItCannotCreate(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := AddToGroup(filepath.Join(blocker, "config.yaml"), GroupAll, "alpha"); err == nil {
		t.Error("AddToGroup created a configuration under a regular file")
	}
}

func TestAddToGroupReportsADanglingDirectoryOnItsPath(t *testing.T) {
	// A symlink pointing nowhere: reading through it says the file is not
	// there, which is the path that writes a new configuration, and creating
	// the directory then fails on the symlink already holding the name.
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := AddToGroup(filepath.Join(link, "config.yaml"), GroupAll, "alpha"); err == nil {
		t.Error("AddToGroup created a configuration under a dangling symlink")
	}
}

func TestAddToGroupReportsATemporaryFileItCannotWrite(t *testing.T) {
	// The rewrite goes through a file beside the original. A directory sitting
	// on that name is the one way to stop it without touching permissions,
	// which would prove nothing when the suite runs under sudo.
	path := writeConfig(t, "groups:\n  all:\n    - alpha\n")
	if err := os.Mkdir(path+".tmp", 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := AddToGroup(path, GroupAll, "bravo"); err == nil {
		t.Error("AddToGroup reported success without writing anything")
	}
	if got := readConfig(t, path); strings.Contains(got, "bravo") {
		t.Errorf("the configuration was changed anyway:\n%s", got)
	}
}
