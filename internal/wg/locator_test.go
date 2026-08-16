package wg

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunDirLocatorReadsTheInterfaceName(t *testing.T) {
	dir := t.TempDir()
	// wg-quick writes the real utun name here when it brings a tunnel up.
	if err := os.WriteFile(filepath.Join(dir, "delta.name"), []byte("utun4\n"), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	loc := RunDirLocator{Dir: dir}

	device, authoritative := loc.Device("delta")

	if !authoritative {
		t.Error("authoritative = false, want true when the run directory is readable")
	}
	if device != "utun4" {
		t.Errorf("device = %q, want %q", device, "utun4")
	}
}

func TestRunDirLocatorReportsAMissingNameFileAsDown(t *testing.T) {
	// delta6 shares its peer public key with delta: only the absence of its
	// name file tells them apart.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "delta.name"), []byte("utun4\n"), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}
	loc := RunDirLocator{Dir: dir}

	device, authoritative := loc.Device("delta6")

	if !authoritative {
		t.Error("authoritative = false, want true: the run directory answers for every tunnel")
	}
	if device != "" {
		t.Errorf("device = %q, want empty: no name file means the tunnel is down", device)
	}
}

func TestRunDirLocatorDeclinesWhenTheDirectoryIsAbsent(t *testing.T) {
	// Without the directory, callers must fall back to matching by public key.
	loc := RunDirLocator{Dir: filepath.Join(t.TempDir(), "absent")}

	if _, authoritative := loc.Device("delta"); authoritative {
		t.Error("authoritative = true, want false when the run directory does not exist")
	}
}

func TestRunDirLocatorTrimsTheInterfaceName(t *testing.T) {
	// wg-quick writes the name with a trailing newline.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "alpha.name"), []byte("  utun4  \n"), 0o400); err != nil {
		t.Fatalf("write: %v", err)
	}

	device, _ := RunDirLocator{Dir: dir}.Device("alpha")

	if device != "utun4" {
		t.Errorf("device = %q, want %q", device, "utun4")
	}
}

func TestRunDirLocatorDefaultsToTheSystemDirectory(t *testing.T) {
	// An empty Dir must not be read as the working directory.
	var loc RunDirLocator

	if _, authoritative := loc.Device("alpha"); authoritative != dirExists(DefaultRunDir) {
		t.Errorf("authoritative = %v, want it to follow %s", authoritative, DefaultRunDir)
	}
}

func dirExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
