#!/usr/bin/env bash
# Exercise the actual workflow guards without a token or network access.
set -euo pipefail
cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.."

output_file=$(mktemp)
trap 'rm -f "$output_file"' EXIT
export GITHUB_OUTPUT="$output_file"
export GITHUB_REPOSITORY=torana-edge/torana-plugin-sdk
export SDK_REVISION=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
export SDK_TAG=v0.5.0
export DEFAULT_REVISION=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb

gh() {
  test "$1" = api || return 2
  test "$3" = --jq || return 2
  case "$2" in
    "repos/$GITHUB_REPOSITORY/releases/tags/$SDK_TAG")
      [ "$SCENARIO" = draft ] || printf '%s\n' "$SDK_TAG"
      ;;
    "repos/$GITHUB_REPOSITORY/commits/refs/tags/$SDK_TAG")
      test "$4" = .sha || return 2
      printf '%s\n' "$SDK_REVISION"
      ;;
    "repos/$GITHUB_REPOSITORY")
      test "$4" = .default_branch || return 2
      [ "$SCENARIO" != default_lookup_error ] || return 1
      printf 'trunk\n'
      ;;
    "repos/$GITHUB_REPOSITORY/commits/refs/heads/trunk")
      test "$4" = .sha || return 2
      case "$SCENARIO" in
        identical) printf '%s\n' "$SDK_REVISION" ;;
        malformed_default) printf 'null\n' ;;
        *) printf '%s\n' "$DEFAULT_REVISION" ;;
      esac
      ;;
    "repos/$GITHUB_REPOSITORY/compare/$SDK_REVISION...$DEFAULT_REVISION" | \
    "repos/$GITHUB_REPOSITORY/compare/$SDK_REVISION...$SDK_REVISION")
      test "$4" = .merge_base_commit.sha || return 2
      case "$SCENARIO" in
        identical | ancestor) printf '%s\n' "$SDK_REVISION" ;;
        descendant) printf '%s\n' "$DEFAULT_REVISION" ;;
        diverged) printf 'cccccccccccccccccccccccccccccccccccccccc\n' ;;
        malformed_comparison) printf 'null\n' ;;
        comparison_error) return 1 ;;
        *) return 2 ;;
      esac
      ;;
    *) echo "unexpected API request: $2" >&2; return 2 ;;
  esac
}
export -f gh

for workflow in release publish-rust; do
  case "$workflow" in
    release) step='Require release source on the default branch' ;;
    publish-rust) step='Resolve an existing published release to immutable source' ;;
  esac
  # Extract one literal run block, stopping before the next step or job.
  guard=$(awk -v step="$step" '
    $0 == "      - name: " step { found = 1 }
    found && /^        run: \|$/ { run = 1; next }
    run && /^          / { sub(/^          /, ""); print; next }
    run && /^$/ { print; next }
    run { exit }
  ' ".github/workflows/$workflow.yml")
  test -n "$guard"

  for SCENARIO in identical ancestor descendant diverged malformed_default \
      default_lookup_error malformed_comparison comparison_error; do
    export SCENARIO
    : > "$output_file"
    expected=1
    case "$SCENARIO" in identical | ancestor) expected=0 ;; esac
    actual=0
    result=$(bash --noprofile --norc -e -o pipefail <<< "$guard" 2>&1) || actual=$?
    if { [ "$expected" = 0 ] && [ "$actual" != 0 ]; } || \
       { [ "$expected" != 0 ] && [ "$actual" = 0 ]; }; then
      printf 'FAIL: %s %s (exit %s)\n%s\n' "$workflow" "$SCENARIO" "$actual" "$result" >&2
      exit 1
    fi
    if [ "$workflow" = publish-rust ] && [ "$expected" = 0 ]; then
      test "$(< "$output_file")" = "revision=$SDK_REVISION"
    elif [ "$expected" != 0 ]; then
      test ! -s "$output_file"
    fi
    printf 'PASS: %s %s\n' "$workflow" "$SCENARIO"
  done
done
