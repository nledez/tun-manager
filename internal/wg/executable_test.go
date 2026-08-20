package wg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// anExecutable writes something that looks like wg-quick, in a directory laid
// out the way a sound installation has it.
func anExecutable(t *testing.T) string {
	t.Helper()

	// Resolved, because on darwin t.TempDir hands back a path under /var, which
	// is itself a link to /private/var. Building the fixture under the resolved
	// name is what makes "the path and what it resolves to are the same thing"
	// a case these tests actually cover.
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve the temporary directory: %v", err)
	}
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "wg-quick")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	return path
}

func refusedExecutable(t *testing.T, path string) string {
	t.Helper()

	err := CheckExecutable(path)
	if err == nil {
		t.Fatalf("CheckExecutable(%s) accepted it, want a refusal", path)
	}
	return err.Error()
}

func TestCheckExecutableAcceptsASoundInstallation(t *testing.T) {
	if err := CheckExecutable(anExecutable(t)); err != nil {
		t.Errorf("CheckExecutable on a sound binary: %v", err)
	}
}

func TestCheckExecutableFollowsTheSymbolicLinkHomebrewLeaves(t *testing.T) {
	// /opt/homebrew/bin/wg-quick is a link into ../Cellar. Refusing links would
	// refuse the installation this program documents, so the link is followed
	// and what it points at is what gets checked.
	target := anExecutable(t)
	link := filepath.Join(filepath.Dir(target), "wg-quick-link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	if err := CheckExecutable(link); err != nil {
		t.Errorf("CheckExecutable on a linked binary: %v", err)
	}
}

func TestCheckExecutableRefusesALinkToNowhere(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "wg-quick")
	if err := os.Symlink(filepath.Join(dir, "gone"), link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	refusedExecutable(t, link)
}

func TestCheckExecutableRefusesWhatIsNotThere(t *testing.T) {
	message := refusedExecutable(t, filepath.Join(t.TempDir(), "wg-quick"))

	if !strings.Contains(message, "wg-quick") {
		t.Errorf("refusal %q does not name what is missing", message)
	}
}

func TestCheckExecutableRefusesADirectory(t *testing.T) {
	refusedExecutable(t, t.TempDir())
}

func TestCheckExecutableRefusesSomethingWithNoExecuteBit(t *testing.T) {
	path := anExecutable(t)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	message := refusedExecutable(t, path)

	if !strings.Contains(message, "executable") {
		t.Errorf("refusal %q does not say what is wrong", message)
	}
}

func TestCheckExecutableRefusesABinaryAnybodyCanWrite(t *testing.T) {
	// This is the whole point: root runs it. Anybody who can write it chooses
	// what root does at the next `sudo tun-manager up`.
	path := anExecutable(t)
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	message := refusedExecutable(t, path)

	for _, want := range []string{path, "0777", "root"} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal %q does not contain %q", message, want)
		}
	}
}

func TestCheckExecutableRefusesADirectoryAnybodyCanWriteOnTheWay(t *testing.T) {
	// The mode of the binary is not the whole story: anybody who can write the
	// directory holding it can replace it with one of their own.
	path := anExecutable(t)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	message := refusedExecutable(t, path)

	if !strings.Contains(message, dir) {
		t.Errorf("refusal %q does not name the directory", message)
	}
}

func TestCheckExecutableRefusesADirectoryAnybodyCanWriteBehindTheLink(t *testing.T) {
	// The link is in a sound directory; what it points at is not. Checking only
	// the name the configuration gives would miss it entirely.
	target := anExecutable(t)
	behind := filepath.Dir(target)
	if err := os.Chmod(behind, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(behind, 0o755) })

	sound := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(sound, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(sound, "wg-quick")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	message := refusedExecutable(t, link)

	if !strings.Contains(message, behind) {
		t.Errorf("refusal %q does not name the directory behind the link", message)
	}
}

func TestCheckExecutableAcceptsAStickyDirectoryAnybodyCanWrite(t *testing.T) {
	// /tmp is 1777. The sticky bit is what stops one user renaming another's
	// file away, so a binary sitting under it cannot be swapped by somebody who
	// does not own it - and a demo that keeps its stub there is not a finding.
	path := anExecutable(t)
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, os.ModeSticky|0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := CheckExecutable(path); err != nil {
		t.Errorf("CheckExecutable under a sticky directory: %v", err)
	}
}

func TestARefusalNamesBothTheLinkAndWhatItPointsAt(t *testing.T) {
	// "wg_quick /opt/homebrew/bin/wg-quick is 0644 and not executable" sends
	// somebody to look at the link. What is wrong is behind it.
	target := anExecutable(t)
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	link := filepath.Join(t.TempDir(), "wg-quick")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	message := refusedExecutable(t, link)

	for _, want := range []string{link, target} {
		if !strings.Contains(message, want) {
			t.Errorf("refusal %q does not contain %q", message, want)
		}
	}
}
