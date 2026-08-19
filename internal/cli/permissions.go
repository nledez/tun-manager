package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"ledez.net/tun-manager/internal/privdrop"
	"ledez.net/tun-manager/internal/profile"
)

// The modes the layout is meant to have. A check passes anything at least this
// strict and fails what is looser: somebody who runs config_dir at 0500 has not
// made a mistake, and a doctor that tells them to loosen it is one people stop
// reading.
const (
	// WireGuardDirMode is what /private/wireguard and its config directory are
	// meant to be. 0700 and root-owned means the tunnel list cannot be read by
	// anybody else - not the keys, which are protected by the file mode below,
	// but the names, the endpoints and often who they reach.
	WireGuardDirMode os.FileMode = 0o700
	// TunnelFileMode is what each .conf is meant to be. It carries a private
	// key, and wg-quick reads it as root: nobody else needs it.
	TunnelFileMode os.FileMode = 0o600
	// UserConfigDirMode is what ~/.config/tun-manager is meant to be. It holds
	// no secret - the keys live in the .conf files, which this program's parser
	// never reads - so it is an ordinary dotfile directory, and the thing worth
	// checking is that a run under sudo has not left it owned by root.
	UserConfigDirMode os.FileMode = 0o755
)

// ownerRoot is the uid and gid the WireGuard side is meant to have.
const ownerRoot = 0

// Permissions reports whether the files this program reads are readable by
// people who should not read them.
//
// Three checks rather than one, because they fail for different reasons and are
// fixed by different commands. Every one of them names the chmod or chown that
// would put it right: a diagnostic that says something is wrong without saying
// what to type is half a diagnostic.
func Permissions(cfg *profile.Config, u privdrop.User) []Check {
	return []Check{
		checkWireGuardDirs(cfg.ConfigDir),
		checkTunnelFiles(cfg.ConfigDir),
		checkUserConfigDir(cfg, u),
	}
}

// checkWireGuardDirs checks the directory holding the .conf files, and the one
// holding it.
//
// The parent is checked for who can *write* it rather than who can read it. Its
// mode leaks nothing while the directory inside is 0700, but anybody who can
// write the parent can rename that directory away and put their own in its
// place, and wg-quick would then read whatever they wrote. A parent is often
// shared - config_dir may well be /etc/wireguard - so demanding 0700 of it
// would have this command telling people to chmod 700 /etc, which is worse than
// anything it is guarding against.
func checkWireGuardDirs(configDir string) Check {
	const name = "config dir mode"

	info, err := os.Stat(configDir)
	if err != nil {
		return Check{Name: name, Status: Fail, Detail: fmt.Sprintf("%s: %v", configDir, err)}
	}
	if problem := tooOpen(configDir, info, WireGuardDirMode); problem != "" {
		return Check{Name: name, Status: Fail, Detail: problem}
	}

	parent := filepath.Dir(configDir)
	parentInfo, err := os.Stat(parent)
	if err != nil {
		// NOT TESTED: the stat above walked through this directory, so it was
		// there a moment ago. Only something removing it in between reaches
		// here, which is not a window a test can open.
		// See docs/coverage-gaps.md, "filesystem races in the permission code".
		return Check{Name: name, Status: Fail, Detail: fmt.Sprintf("%s: %v", parent, err)}
	}
	if uid, _ := ownerOf(parent, parentInfo); uid != ownerRoot {
		return Check{
			Name:   name,
			Status: Fail,
			Detail: fmt.Sprintf("%s is owned by uid %d: `sudo chown 0:0 %s`", parent, uid, parent),
		}
	}
	if parentInfo.Mode().Perm()&0o022 != 0 {
		return Check{
			Name:   name,
			Status: Fail,
			Detail: fmt.Sprintf(
				"%s is %04o and writable by others, so %s could be replaced: `sudo chmod go-w %s`",
				parent, parentInfo.Mode().Perm(), filepath.Base(configDir), parent),
		}
	}

	return Check{
		Name:   name,
		Status: Pass,
		Detail: fmt.Sprintf("%s %04o root:root, under %s", configDir, info.Mode().Perm(), parent),
	}
}

// checkTunnelFiles checks every .conf. These are the files holding the keys.
func checkTunnelFiles(configDir string) Check {
	const name = "tunnel files"

	entries, err := os.ReadDir(configDir)
	if err != nil {
		return Check{Name: name, Status: Fail, Detail: fmt.Sprintf("%s: %v", configDir, err)}
	}

	var problems []string
	checked := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".conf") {
			continue
		}
		path := filepath.Join(configDir, entry.Name())
		info, statErr := entry.Info()
		if statErr != nil {
			// NOT TESTED: ReadDir does not stat, so this lstat can fail for a
			// file deleted between listing the directory and reading it. Same
			// window as above, and just as closed to a test.
			// See docs/coverage-gaps.md, "filesystem races in the permission code".
			problems = append(problems, fmt.Sprintf("%s: %v", path, statErr))
			continue
		}
		checked++
		if problem := tooOpen(path, info, TunnelFileMode); problem != "" {
			problems = append(problems, problem)
		}
	}

	if len(problems) > 0 {
		sort.Strings(problems)
		return Check{Name: name, Status: Fail, Detail: strings.Join(problems, "; ")}
	}
	if checked == 0 {
		return Check{Name: name, Status: Warn, Detail: "no *.conf to check in " + configDir}
	}
	return Check{
		Name:   name,
		Status: Pass,
		Detail: fmt.Sprintf("%d file(s) %04o root:root", checked, TunnelFileMode.Perm()),
	}
}

// checkUserConfigDir checks the directory holding config.yaml.
//
// Nothing secret is in it, so the mode matters less than the owner: root is the
// failure worth catching. A file written by root under a home directory is one
// its owner can no longer edit, and that is what happens when something creates
// it during a sudo run without handing it back.
func checkUserConfigDir(cfg *profile.Config, u privdrop.User) Check {
	const name = "user config dir"

	dir := filepath.Dir(cfg.Path)
	info, err := os.Stat(dir)
	if err != nil {
		return Check{Name: name, Status: Warn, Detail: fmt.Sprintf("%s: %v", dir, err)}
	}
	if info.Mode().Perm()&0o022 != 0 {
		return Check{
			Name:   name,
			Status: Fail,
			Detail: fmt.Sprintf("%s is %04o and writable by others: `chmod go-w %s`",
				dir, info.Mode().Perm(), dir),
		}
	}

	uid, _ := ownerOf(dir, info)
	// Without SUDO_USER there is no pre-sudo user to compare against, and root
	// owning its own files is not news.
	if u.Demotable && uid != u.UID {
		return Check{
			Name:   name,
			Status: Fail,
			Detail: fmt.Sprintf("%s is owned by uid %d rather than %s, who cannot edit it: `sudo chown -R %d:%d %s`",
				dir, uid, u.Username, u.UID, u.GID, dir),
		}
	}
	return Check{
		Name:   name,
		Status: Pass,
		Detail: fmt.Sprintf("%s %04o, owned by uid %d", dir, info.Mode().Perm(), uid),
	}
}

// tooOpen reports what is wrong with a path meant to be root-owned and no more
// permissive than want, or "" when nothing is.
func tooOpen(path string, info os.FileInfo, want os.FileMode) string {
	if uid, _ := ownerOf(path, info); uid != ownerRoot {
		return fmt.Sprintf("%s is owned by uid %d rather than root: `sudo chown 0:0 %s`", path, uid, path)
	}
	// Only the bits that grant somebody else access. A mode stricter than want
	// - 0500 where 0700 was expected - is somebody being careful, not somebody
	// making a mistake.
	if extra := info.Mode().Perm() &^ want.Perm(); extra != 0 {
		return fmt.Sprintf("%s is %04o, want %04o or stricter: `sudo chmod %04o %s`",
			path, info.Mode().Perm(), want.Perm(), want.Perm(), path)
	}
	return ""
}

// ownerOf reads the uid and gid behind a FileInfo.
//
// A variable, and the only one in this package, because it is the one thing
// these checks depend on that a test cannot arrange. A fixture is owned by
// whoever runs the suite; making it root-owned would mean running the suite as
// root, and a suite that only proves itself under sudo proves nothing on
// anybody else's machine. Swapped by ownedBy in the tests, and never assigned
// anywhere else.
var ownerOf func(path string, info os.FileInfo) (uid, gid int) = realOwner

// realOwner reads the uid and gid out of the stat behind a FileInfo. darwin
// only, like the rest of this program.
func realOwner(_ string, info os.FileInfo) (uid, gid int) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// NOT TESTED: os.Stat on darwin always yields a *syscall.Stat_t. This
		// guards a platform where it does not, on which the whole program does
		// not build.
		// See docs/coverage-gaps.md, "cli.ownerOf".
		return -1, -1
	}
	return int(stat.Uid), int(stat.Gid)
}
