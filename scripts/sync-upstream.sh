#!/usr/bin/env bash
# sync-upstream.sh merges an upstream Gio ref into this fork and re-applies the fork's module
# path. UPSTREAM.md is the procedure; this script is its mechanical part.
#
#   scripts/sync-upstream.sh merge [REF]  fetch REF from upstream (default upstream/main; a tag
#                                         such as v0.10.4, or a commit), merge it without
#                                         committing, then rename and check
#   scripts/sync-upstream.sh rename       rewrite gioui.org imports to this module's path; run it
#                                         after resolving conflicts
#   scripts/sync-upstream.sh check        no gioui.org import left, go.mod's module, gofmt, the
#                                         builds and vet
#
# It never commits or pushes: the merge commit is yours to review and write.
set -euo pipefail

UPSTREAM_URL=https://git.sr.ht/~eliasnaur/gio
MODULE=github.com/yongjohnlee80/gio

die() {
	printf 'sync-upstream: %b\n' "$*" >&2
	exit 1
}

root=$(git rev-parse --show-toplevel)
cd "$root"

ensure_remote() {
	if ! git remote get-url upstream >/dev/null 2>&1; then
		git remote add upstream "$UPSTREAM_URL"
	fi
	# Upstream's tags never enter refs/tags. Pushed, a v0.10.x tag would become a version of this
	# module whose go.mod names gioui.org, and a higher one than ours.
	git config remote.upstream.tagOpt --no-tags
}

# fetch_ref REF prints the commit REF names upstream.
fetch_ref() {
	local ref=$1
	case $ref in
	upstream/*)
		git fetch --no-tags upstream "+refs/heads/${ref#upstream/}:refs/remotes/$ref" >&2
		;;
	v[0-9]*)
		git fetch --no-tags upstream "+refs/tags/$ref:refs/upstream-tags/$ref" >&2
		ref=refs/upstream-tags/$ref
		;;
	*)
		git fetch --no-tags upstream >&2
		;;
	esac
	git rev-parse --verify "$ref^{commit}"
}

cmd_rename() {
	if [ -n "$(git diff --name-only --diff-filter=U)" ]; then
		die "unresolved conflicts remain:\n$(git diff --name-only --diff-filter=U)"
	fi
	# Every quoted gioui.org path but gioui.org/shader, which stays upstream's module.
	git ls-files -z '*.go' | xargs -0 perl -pi -e 's#"gioui\.org/(?!shader(?:/|"))#"'"$MODULE"'/#g'
	perl -pi -e 's#^module gioui\.org$#module '"$MODULE"'#' go.mod
	# The rename moves imports between sort positions: gofmt re-sorts their blocks.
	git ls-files -z '*.go' | xargs -0 gofmt -w
	git add -u
}

cmd_check() {
	local left fmt
	# The fork is one module: a go.work above the checkout must not build it against other trees.
	export GOWORK=off
	left=$(git grep -n '"gioui\.org/' -- '*.go' | grep -v '"gioui\.org/shader' || true)
	[ -z "$left" ] || die "imports still name gioui.org:\n$left"
	grep -qx "module $MODULE" go.mod || die "go.mod does not name module $MODULE"
	fmt=$(git ls-files -z '*.go' | xargs -0 gofmt -l)
	[ -z "$fmt" ] || die "gofmt would rewrite:\n$fmt"
	go build ./...
	go vet ./app
	go build -tags nox11 ./app
	go build -tags nowayland ./app
	echo "sync-upstream: check ok. Next: the tests and the IME matrix (UPSTREAM.md, 'Gates')."
}

cmd_merge() {
	local ref=${1:-upstream/main} branch commit
	[ -z "$(git status --porcelain)" ] || die "the tree has changes: commit them or set them aside first"
	branch=$(git symbolic-ref --quiet --short HEAD || true)
	[ -n "$branch" ] || die "HEAD is detached: merge on a sync branch (git switch -c sync/<ref>)"
	[ "$branch" != main ] || die "merge on a sync branch, not main (git switch -c sync/<ref>)"
	ensure_remote
	commit=$(fetch_ref "$ref")
	echo "sync-upstream: merging $ref ($commit) into $branch"
	if ! git merge --no-ff --no-commit "$commit"; then
		die "conflicts, listed below. Resolve them (UPSTREAM.md, 'Conflicts'), then run:\n  $0 rename && $0 check\n$(git diff --name-only --diff-filter=U)"
	fi
	cmd_rename
	cmd_check
	echo "sync-upstream: review 'git diff --cached', then commit:"
	echo "  git commit -m 'sync: upstream $ref (${commit:0:12})'"
}

case ${1:-} in
merge) shift; cmd_merge "$@" ;;
rename) cmd_rename ;;
check) cmd_check ;;
*) die "usage: $0 merge [REF] | rename | check" ;;
esac
