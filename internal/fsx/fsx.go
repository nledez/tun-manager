// Package fsx reads the identity behind a file.
//
// Eight lines, in a package of their own, because three others need them and
// none of them can test with the real thing: a fixture is owned by whoever runs
// the suite, and making it root-owned would mean running the suite as root — a
// suite that only proves itself under sudo proves nothing on anybody else's
// machine. So the call is a variable, and it is swapped in the tests of
// whichever package is asking.
//
// One copy rather than three. Three copies of a rule about who owns what is
// three chances for one of them to answer differently.
package fsx

import (
	"os"
	"syscall"
)

// Root is the uid every file this program trusts is meant to have.
const Root = 0

// Owner reports the uid and gid behind a FileInfo.
//
// A variable because a fixture is owned by
// whoever runs the suite, and making one root-owned would mean running the
// suite as root — a suite that only proves itself under sudo proves nothing on
// anybody else's machine.
var Owner func(path string, info os.FileInfo) (uid, gid int) = realOwner

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
