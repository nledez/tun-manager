package main

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This generator produces the file that says which licences the binary is bound
// by, and `make notices-check` only ever proves it has not changed. What these
// tests are about is the thing that check cannot see: a licence file the
// collecting never picked up in the first place. So the package list is written
// here rather than taken from the module cache, and the modules are directories
// this test lays out.

// aModule writes a module with the files named in it, and returns its
// directory.
func aModule(t *testing.T, files map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	for name, body := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// listing makes the generator read the package list a test wrote.
func listing(t *testing.T, pkgs []listedPackage, err error) {
	t.Helper()

	previous := listPackages
	listPackages = func(context.Context, []string) ([]listedPackage, error) { return pkgs, err }
	t.Cleanup(func() { listPackages = previous })
}

// aPackage describes one imported package of one module.
func aPackage(importPath, dir, modulePath, version, moduleDir string) listedPackage {
	pkg := listedPackage{ImportPath: importPath, Dir: dir}
	pkg.Module = &struct {
		Path    string `json:"Path"`
		Version string `json:"Version"`
		Dir     string `json:"Dir"`
		Main    bool   `json:"Main"`
	}{Path: modulePath, Version: version, Dir: moduleDir}
	return pkg
}

func TestGenerateReproducesEveryLicenceVerbatim(t *testing.T) {
	// Verbatim is the requirement: BSD-3-Clause clause 2 and the equivalent
	// clauses of the dependencies are satisfied by the text, not by a summary
	// of it.
	dir := aModule(t, map[string]string{
		"LICENSE":     "the terms of example.com/thing\n",
		"NOTICE":      "a notice\n",
		"README":      "not a licence\n",
		"zstd/README": "nor this\n",
	})
	listing(t, []listedPackage{aPackage("example.com/thing", dir, "example.com/thing", "v1.2.3", dir)}, nil)

	rendered, err := generate(context.Background(), []string{"./..."})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	for _, want := range []string{
		"example.com/thing v1.2.3",
		"the terms of example.com/thing",
		"a notice",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the notices do not contain %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "not a licence") {
		t.Errorf("something that is not a licence was reproduced:\n%s", rendered)
	}
}

func TestGenerateFindsALicenceBesideAnImportedSubPackage(t *testing.T) {
	// klauspost/compress carries a separate licence beside its zstd/xxhash
	// tree. Collecting only the module root would leave it out, and the file
	// would claim terms that do not cover what is linked.
	dir := aModule(t, map[string]string{
		"LICENSE":                 "the module's own terms\n",
		"zstd/xxhash/LICENSE.txt": "the xxhash terms\n",
	})
	listing(t, []listedPackage{
		aPackage("example.com/thing/zstd/xxhash", filepath.Join(dir, "zstd", "xxhash"),
			"example.com/thing", "v1.0.0", dir),
	}, nil)

	rendered, err := generate(context.Background(), []string{"./..."})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	for _, want := range []string{"the module's own terms", "the xxhash terms"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the notices do not contain %q:\n%s", want, rendered)
		}
	}
}

func TestGenerateIgnoresASubPackageNobodyImports(t *testing.T) {
	// The point of walking up from the imported package rather than down from
	// the module root: a licence for code that is not linked is a claim about
	// something this program does not ship.
	dir := aModule(t, map[string]string{
		"LICENSE":              "the module's own terms\n",
		"examples/LICENSE.txt": "terms of an example nobody links\n",
	})
	listing(t, []listedPackage{aPackage("example.com/thing", dir, "example.com/thing", "v1.0.0", dir)}, nil)

	rendered, err := generate(context.Background(), []string{"./..."})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if strings.Contains(rendered, "nobody links") {
		t.Errorf("a licence for unimported code was reproduced:\n%s", rendered)
	}
}

func TestGenerateSkipsTheStandardLibraryAndTheMainModule(t *testing.T) {
	dir := aModule(t, map[string]string{"LICENSE": "terms\n"})
	main := listedPackage{ImportPath: "ledez.net/tun-manager", Dir: dir}
	main.Module = &struct {
		Path    string `json:"Path"`
		Version string `json:"Version"`
		Dir     string `json:"Dir"`
		Main    bool   `json:"Main"`
	}{Path: "ledez.net/tun-manager", Dir: dir, Main: true}
	listing(t, []listedPackage{
		{ImportPath: "errors", Dir: "/usr/local/go/src/errors", Standard: true},
		{ImportPath: "example.com/nomodule", Dir: dir},
		main,
	}, nil)

	rendered, err := generate(context.Background(), []string{"./..."})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(rendered, "no third-party dependencies") {
		t.Errorf("something was reproduced for a program with no dependencies:\n%s", rendered)
	}
}

func TestGenerateReportsAPackageListItCannotGet(t *testing.T) {
	boom := errors.New("go list: exit status 1")
	listing(t, nil, boom)

	if _, err := generate(context.Background(), []string{"./..."}); !errors.Is(err, boom) {
		t.Errorf("err = %v, want the failure to list packages", err)
	}
}

func TestGenerateReportsALicenceItCannotRead(t *testing.T) {
	// Listed and then gone by the time it is read. Reproducing the rest and
	// staying silent would ship a file that claims to be complete.
	dir := aModule(t, map[string]string{"LICENSE": "terms\n"})
	listing(t, []listedPackage{aPackage("example.com/thing", dir, "example.com/thing", "v1.0.0", dir)}, nil)
	previous := readFile
	readFile = func(string) ([]byte, error) { return nil, errUnreadable }
	t.Cleanup(func() { readFile = previous })

	if _, err := generate(context.Background(), []string{"./..."}); !errors.Is(err, errUnreadable) {
		t.Errorf("err = %v, want the failure to read the licence", err)
	}
}

// errUnreadable is the licence that was there when the directory was listed and
// is not there when it is read.
var errUnreadable = errors.New("no such file or directory")

func TestALicenceWithNoNewlineAtTheEndStillEndsOne(t *testing.T) {
	// The separator between two reproduced files is a line of its own, and a
	// file that does not end in a newline would run into it.
	dir := aModule(t, map[string]string{"LICENSE": "terms with no newline"})
	listing(t, []listedPackage{aPackage("example.com/thing", dir, "example.com/thing", "v1.0.0", dir)}, nil)

	rendered, err := generate(context.Background(), []string{"./..."})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	if !strings.Contains(rendered, "terms with no newline\n") {
		t.Errorf("the licence was not ended:\n%q", rendered)
	}
}

func TestWhatCountsAsALicenceFile(t *testing.T) {
	for name, want := range map[string]bool{
		"LICENSE":      true,
		"licence":      true,
		"LICENSE.txt":  true,
		"COPYING":      true,
		"NOTICE.md":    true,
		"README":       false,
		"go.mod":       false,
		"licensing.go": false, // starts with LICENS, which is not one of the four names
		"CONTRIBUTORS": false,
	} {
		t.Run(name, func(t *testing.T) {
			if got := isLicenseFile(name); got != want {
				t.Errorf("isLicenseFile(%q) = %v, want %v", name, got, want)
			}
		})
	}
}

func TestCollectingIgnoresADirectoryItCannotRead(t *testing.T) {
	// A module directory that has gone. Saying nothing about it is right: the
	// licences of what is there are still worth reproducing.
	mod := &moduleLicenses{path: "example.com/thing", files: map[string]string{}}

	collectInto(mod, filepath.Join(t.TempDir(), "absent"), t.TempDir())

	if len(mod.files) != 0 {
		t.Errorf("files = %v, want none", mod.files)
	}
}

func TestEveryTargetPlatformIsListedOnce(t *testing.T) {
	// go list answers for one platform at a time. Merging is what keeps a
	// licence that only darwin/amd64 is bound by from being left out, and the
	// deduplication is what keeps every shared dependency from appearing twice.
	var asked []string
	previous := listPackagesFor
	listPackagesFor = func(_ context.Context, goos, goarch string, _ []string) ([]listedPackage, error) {
		asked = append(asked, goos+"/"+goarch)
		if goarch == "arm64" {
			return []listedPackage{{ImportPath: "example.com/shared"}, {ImportPath: "example.com/arm-only"}}, nil
		}
		return []listedPackage{{ImportPath: "example.com/shared"}}, nil
	}
	t.Cleanup(func() { listPackagesFor = previous })

	pkgs, err := listEveryTarget(context.Background(), []string{"./..."})
	if err != nil {
		t.Fatalf("listEveryTarget: %v", err)
	}

	if len(asked) != len(targets) {
		t.Errorf("asked %v, want one call per shipped target", asked)
	}
	if len(pkgs) != 2 {
		t.Errorf("pkgs = %+v, want the shared one once and the arm one as well", pkgs)
	}
}

func TestAPlatformThatCannotBeListedIsNamed(t *testing.T) {
	boom := errors.New("build constraints exclude all Go files")
	previous := listPackagesFor
	listPackagesFor = func(context.Context, string, string, []string) ([]listedPackage, error) {
		return nil, boom
	}
	t.Cleanup(func() { listPackagesFor = previous })

	_, err := listEveryTarget(context.Background(), []string{"./..."})

	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the failure itself", err)
	}
	if !strings.Contains(err.Error(), targets[0].goos) {
		t.Errorf("error %q does not say which platform", err)
	}
}

func TestGoListAnswersForRealPackages(t *testing.T) {
	// The one call that shells out, exercised against the standard library so
	// it costs a fraction of a second and depends on nothing but Go itself.
	pkgs, err := runGoList(context.Background(), "darwin", "arm64", []string{"encoding/json"})
	if err != nil {
		t.Fatalf("runGoList: %v", err)
	}

	found := false
	for _, pkg := range pkgs {
		if pkg.ImportPath == "encoding/json" {
			found = true
			if !pkg.Standard {
				t.Error("encoding/json is not reported as standard")
			}
		}
	}
	if !found {
		t.Errorf("the listing has no encoding/json in it: %+v", pkgs)
	}
}

func TestGoListReportsAPatternItCannotResolve(t *testing.T) {
	// go list says why on its own standard error, which is this program's:
	// pointed at a builder here so the suite's output stays clean.
	previous := stderr
	stderr = &strings.Builder{}
	t.Cleanup(func() { stderr = previous })

	_, err := runGoList(context.Background(), "darwin", "arm64", []string{"example.invalid/nothing/here"})

	if err == nil {
		t.Fatal("runGoList reported success on a pattern that resolves to nothing")
	}
}

func TestMainWritesTheFileItWasAskedFor(t *testing.T) {
	dir := aModule(t, map[string]string{"LICENSE": "terms\n"})
	listing(t, []listedPackage{aPackage("example.com/thing", dir, "example.com/thing", "v1.0.0", dir)}, nil)
	out := filepath.Join(t.TempDir(), "NOTICES.txt")
	runMain(t, []string{"notices", "-o", out})

	body, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "terms") {
		t.Errorf("what was written has no licence in it:\n%s", body)
	}
}

func TestMainPrintsWhenAskedForStandardOutput(t *testing.T) {
	dir := aModule(t, map[string]string{"LICENSE": "printed terms\n"})
	listing(t, []listedPackage{aPackage("example.com/thing", dir, "example.com/thing", "v1.0.0", dir)}, nil)
	said := runMain(t, []string{"notices", "-o", "-"})

	if !strings.Contains(said.String(), "printed terms") {
		t.Errorf("nothing was printed:\n%s", said.String())
	}
}

func TestMainReportsWhatStoppedIt(t *testing.T) {
	listing(t, nil, errors.New("go list: exit status 1"))
	var code int
	said := runMainWithExit(t, []string{"notices", "-o", filepath.Join(t.TempDir(), "x")}, &code)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(said, "notices: ") {
		t.Errorf("nothing useful was said: %q", said)
	}
}

func TestMainReportsAFileItCannotWrite(t *testing.T) {
	dir := aModule(t, map[string]string{"LICENSE": "terms\n"})
	listing(t, []listedPackage{aPackage("example.com/thing", dir, "example.com/thing", "v1.0.0", dir)}, nil)
	var code int
	said := runMainWithExit(t, []string{"notices", "-o", filepath.Join(t.TempDir(), "absent", "x")}, &code)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(said, "notices: ") {
		t.Errorf("nothing useful was said: %q", said)
	}
}

// runMain drives main with a command line, and returns what it printed.
func runMain(t *testing.T, args []string) *strings.Builder {
	t.Helper()

	var code int
	var printed strings.Builder
	swapMain(t, args, &printed, &code)
	main()
	if code != 0 {
		t.Fatalf("main exited with %d", code)
	}
	return &printed
}

// runMainWithExit is the same for a run that is expected to stop, and returns
// what reached standard error.
func runMainWithExit(t *testing.T, args []string, code *int) string {
	t.Helper()

	var printed, said strings.Builder
	swapMain(t, args, &printed, code)
	previousErr := stderr
	stderr = &said
	t.Cleanup(func() { stderr = previousErr })

	main()

	return said.String()
}

func swapMain(t *testing.T, args []string, printed *strings.Builder, code *int) {
	t.Helper()

	previousArgs, previousFlags, previousOut, previousExit := os.Args, flag.CommandLine, stdout, exit
	os.Args = args
	flag.CommandLine = flag.NewFlagSet(args[0], flag.ContinueOnError)
	stdout = printed
	exit = func(status int) { *code = status }
	t.Cleanup(func() {
		os.Args, flag.CommandLine, stdout, exit = previousArgs, previousFlags, previousOut, previousExit
	})
}

func TestCollectingSkipsWhatItCannotPlaceInTheModule(t *testing.T) {
	// A module root that is not a prefix of the directory being read. The
	// relative name is what the file is labelled with in the notices, and a
	// label that cannot be worked out is worse than a file left out.
	mod := &moduleLicenses{path: "example.com/thing", files: map[string]string{}}
	dir := aModule(t, map[string]string{"LICENSE": "terms\n"})

	collectInto(mod, dir, "a-relative-root")

	if len(mod.files) != 0 {
		t.Errorf("files = %v, want none", mod.files)
	}
}

func TestGoListReportsAToolchainItCannotStart(t *testing.T) {
	previousCmd, previousErr := goCommand, stderr
	goCommand = filepath.Join(t.TempDir(), "no-go-here")
	stderr = &strings.Builder{}
	t.Cleanup(func() { goCommand, stderr = previousCmd, previousErr })

	_, err := runGoList(context.Background(), "darwin", "arm64", []string{"errors"})

	if err == nil {
		t.Fatal("runGoList reported success with no toolchain to run")
	}
	if !strings.Contains(err.Error(), "go list") {
		t.Errorf("error %q does not say what it tried to run", err)
	}
}

func TestGoListReportsAnAnswerItCannotRead(t *testing.T) {
	// A toolchain that answers with something other than JSON. Taking the
	// packages it did manage to read would leave the notices short of whatever
	// came after the broken line.
	script := filepath.Join(t.TempDir(), "go")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho 'not json at all'\n"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	previousCmd, previousErr := goCommand, stderr
	goCommand = script
	stderr = &strings.Builder{}
	t.Cleanup(func() { goCommand, stderr = previousCmd, previousErr })

	_, err := runGoList(context.Background(), "darwin", "arm64", []string{"errors"})

	if err == nil {
		t.Fatal("runGoList read a listing that is not one")
	}
	if !strings.Contains(err.Error(), "what go list said") {
		t.Errorf("error %q does not say what could not be read", err)
	}
}
