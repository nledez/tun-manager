#!/usr/bin/env bash
#
# Cuts a release: checks the tree is releasable, then tags and pushes.
#
# Pushing the tag is what publishes. CI re-runs the whole suite on the tagged
# commit and only then builds the archives and creates the GitHub Release, so a
# tag that fails its build publishes nothing and can be superseded.
#
# Every check below refuses rather than repairs: a release cut from a tree
# nobody else can see is a release nobody can reproduce.
#
# Usage: make release VERSION=0.1.0   (DRY_RUN=1 to stop before tagging)

set -euo pipefail

version="${1:-}"
[ -n "$version" ] || {
	echo "release: give a version, e.g. make release VERSION=0.1.0" >&2
	exit 1
}

version="${version#v}"
tag="v${version}"

[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
	echo "release: '$version' is not MAJOR.MINOR.PATCH" >&2
	exit 1
}

branch=$(git rev-parse --abbrev-ref HEAD)
[ "$branch" = main ] || {
	echo "release: on '$branch', not main" >&2
	exit 1
}

[ -z "$(git status --porcelain)" ] || {
	echo "release: the tree is dirty" >&2
	exit 1
}

# Ask origin before fetching, or the fetch below pulls the tag down and the
# local check reports it as local. A tag that exists only on the remote would
# otherwise surface at the push, with the tag already made here.
if git ls-remote --exit-code --tags origin "refs/tags/$tag" >/dev/null 2>&1; then
	echo "release: $tag already exists on origin" >&2
	exit 1
fi

git fetch --quiet --tags origin main
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || {
	echo "release: main and origin/main have diverged; push first" >&2
	exit 1
}

if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
	echo "release: $tag already exists locally" >&2
	exit 1
fi

# go.mod tidiness and the notices file are both checked by CI, but only after
# the tag is pushed, and by then the only way out is another tag. Running the
# whole suite here costs half a minute and saves a wasted version number.
#
# make all regenerates the notices file, so a failure leaves the fix sitting in
# the tree, ready to commit.
echo "release: verifying the tree"
make all >/dev/null

go mod tidy
git diff --exit-code --quiet -- go.mod go.sum || {
	echo "release: go.mod is not tidy - commit the change, then run again" >&2
	exit 1
}

previous=$(git describe --tags --abbrev=0 2>/dev/null || true)
if [ -n "$previous" ]; then
	echo "release: $tag, $(git rev-list --count "$previous"..HEAD) commit(s) since $previous"
else
	echo "release: $tag, the first tag"
fi

if [ -n "${DRY_RUN:-}" ]; then
	echo "release: DRY_RUN set, stopping before the tag"
	exit 0
fi

# Annotated, so the tag carries an author and a date of its own. GoReleaser
# builds the release notes from the commits since the previous tag, which is
# why nothing here writes a changelog: two accounts of the same change would
# only diverge.
git tag -a "$tag" -m "tun-manager ${version}"
git push origin "$tag"

remote=$(git remote get-url origin)
slug=${remote#*github.com[:/]}
slug=${slug%.git}
echo "release: pushed $tag"
echo "release: https://github.com/${slug}/actions - the release appears once CI is green"
