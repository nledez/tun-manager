package main

// NOT TESTED: this package has no unit tests, and is excluded from the coverage
// profile by COVER_PKGS in the Makefile.
//
// It is a build-time generator: it never ships in the binary, and its only
// output is THIRD-PARTY-NOTICES.txt. `make notices-check` runs it and fails when
// that output differs from the file in the tree, on every `make all` and in CI,
// which catches a regression. It does not catch a licence the generator never
// collected in the first place.
//
// See docs/coverage-gaps.md, "The notices generator".
