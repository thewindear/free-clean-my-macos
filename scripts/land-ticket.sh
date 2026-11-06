#!/usr/bin/env bash
# Land one ticket branch on main: merge, verify, push, close.
#
#   ./scripts/land-ticket.sh ticket/04-safety 4
#   ./scripts/land-ticket.sh ticket/04-safety 4 --expect 'internal/engine/*:internal/owner/*'
#   ./scripts/land-ticket.sh ticket/06-tui 6 --no-push      # rehearse locally
#
# The rhythm this encodes: one ticket lands at a time, main is green after every
# step, and a conflict stops the script instead of being auto-resolved —
# resolving a conflict is a judgement call, not a mechanical one.
set -euo pipefail

usage() {
	cat <<'EOF'
usage: land-ticket.sh <branch> [issue] [options]

  --expect <glob:glob>   warn when a changed file matches none of the globs
                         (the ticket's declared scope)
  --no-push              merge and verify, but do not push
  --no-close             merge and push, but do not close the issue
  -h, --help             this message
EOF
}

branch=""
issue=""
expect=""
do_push=1
do_close=1

while [ $# -gt 0 ]; do
	case "$1" in
	--expect)
		expect="$2"
		shift 2
		;;
	--no-push)
		do_push=0
		shift
		;;
	--no-close)
		do_close=0
		shift
		;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		if [ -z "$branch" ]; then
			branch="$1"
		elif [ -z "$issue" ]; then
			issue="$1"
		else
			echo "unexpected argument: $1" >&2
			usage >&2
			exit 2
		fi
		shift
		;;
	esac
done

if [ -z "$branch" ]; then
	usage >&2
	exit 2
fi

# This repo's gh setup suffers from a stale GITHUB_TOKEN in the shell
# environment overriding the keyring login. Drop it for the CLI calls.
unset GITHUB_TOKEN GH_TOKEN

root=$(git rev-parse --show-toplevel)
cd "$root"

fail() {
	echo "land-ticket: $*" >&2
	exit 1
}

[ "$(git rev-parse --abbrev-ref HEAD)" = "main" ] || fail "run this from main, not $(git rev-parse --abbrev-ref HEAD)"
[ -z "$(git status --porcelain)" ] || {
	git status --short
	fail "working tree is dirty; commit or stash first"
}
git rev-parse --verify --quiet "$branch" >/dev/null || fail "no such branch: $branch"

echo "== changed files (main...$branch)"
git diff --stat "main...$branch"

if [ -n "$expect" ]; then
	echo "== scope check (expect: $expect)"
	out_of_scope=""
	while IFS= read -r file; do
		matched=0
		IFS=':' read -ra globs <<<"$expect"
		for glob in "${globs[@]}"; do
			# shellcheck disable=SC2053  # intentional glob match
			if [[ $file == $glob ]]; then
				matched=1
				break
			fi
		done
		if [ "$matched" -eq 0 ]; then
			out_of_scope="$out_of_scope  $file"$'\n'
		fi
	done < <(git diff --name-only "main...$branch")
	if [ -n "$out_of_scope" ]; then
		echo "WARNING: these files fall outside the ticket's declared scope:"
		printf '%s' "$out_of_scope"
		echo "         (merge continues; check nothing leaked from another lane)"
	fi
fi

echo "== merge $branch"
if ! git merge --no-ff "$branch" -m "merge $branch${issue:+ (#$issue)}"; then
	echo >&2
	echo "land-ticket: conflict while merging $branch — that needs a human." >&2
	echo "land-ticket: the merge is aborted; resolve it by hand, then re-run." >&2
	git merge --abort || true
	exit 1
fi

echo "== tests (go test -count=1 ./...)"
if ! go test -count=1 ./...; then
	echo >&2
	echo "land-ticket: tests are RED after merging $branch." >&2
	echo "land-ticket: rolling main back to $(git rev-parse --short ORIG_HEAD); $branch is untouched." >&2
	git reset --hard ORIG_HEAD
	exit 1
fi

if [ "$do_push" -eq 1 ]; then
	echo "== push"
	git push origin main
else
	echo "== push skipped (--no-push)"
fi

# Closing an issue claims "this landed on main". Without a push, main is only
# local, so the claim would be premature.
if [ "$do_close" -eq 1 ] && [ -n "$issue" ] && [ "$do_push" -eq 0 ]; then
	echo "== close #$issue skipped (nothing was pushed)"
fi

if [ "$do_close" -eq 1 ] && [ -n "$issue" ] && [ "$do_push" -eq 1 ]; then
	echo "== close #$issue"
	gh issue close "$issue" --comment "Landed on main in $(git rev-parse --short HEAD)."
fi

echo "== done: $branch landed"
