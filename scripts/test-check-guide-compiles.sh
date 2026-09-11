#!/usr/bin/env bash
# check-guide-compiles.sh builds the guide's programs in a generated module
# under a temp directory. If any ANCESTOR of that directory is a git
# repository, the Go toolchain tries to stamp VCS information and fails:
#
#   error obtaining VCS status: exit status 128
#
# That is a property of whatever contains $TMPDIR, not of the tutorial. A
# documentation compiler that reports it as "the tutorial does not compile"
# sends an author hunting through code that is fine. This reproduces the
# condition deliberately and pins that the checker is unaffected.
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)

outer=$(mktemp -d)
trap 'rm -rf "$outer"' EXIT

# An UNREADABLE .git above the temp directory the checker will create. An
# empty .git directory is what actually triggers this: the toolchain finds it,
# runs git, and git exits 128 because it is not a repository. A VALID
# repository above is harmless — git answers and the stamp succeeds — so
# pinning that case would pin nothing.
mkdir -p "$outer/scratch"
mkdir -p "$outer/.git"

if ! TMPDIR="$outer/scratch" "$script_dir/check-guide-compiles.sh" > "$outer/log" 2>&1; then
  echo "FAIL: the guide checker failed under a git-repository ancestor:" >&2
  cat "$outer/log" >&2
  exit 1
fi

grep -q 'compile' "$outer/log" || {
  echo "FAIL: no success line from the guide checker:" >&2
  cat "$outer/log" >&2
  exit 1
}

echo "check-guide-compiles: unaffected by an unreadable .git above TMPDIR"
