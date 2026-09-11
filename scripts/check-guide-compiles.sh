#!/usr/bin/env bash
# The plugin-authoring guide's main tutorial is the first code a new plugin
# author writes. It stopped compiling when Message.Content was removed in
# favour of the ordered block body, and nothing noticed, because documentation
# is not built.
#
# This builds every complete program in the guide against the SDK in this
# working tree, so a breaking SDK change fails here before it reaches an
# author. Plugin programs are built for wasip1/wasm, the real plugin target;
# programs that declare tests are built for the HOST, because sdktest is
# //go:build !wasip1 on purpose — it is the harness you run with `go test`.
#
# Partial snippets (signatures, fragments) are deliberately not built: they do
# not claim to compile.
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/.." && pwd)
guide="$repo_root/docs/WRITING_A_PLUGIN.md"

tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

count=$(python3 - "$guide" "$tmp_dir" <<'PY'
import os, re, sys
guide, out_dir = sys.argv[1], sys.argv[2]
source = open(guide, encoding="utf-8").read()
blocks = re.findall(r"```go\n(.*?)```", source, re.S)
programs = [b for b in blocks if b.lstrip().startswith("package main")]
if not programs:
    print("no complete Go programs found in the guide; this check guards nothing", file=sys.stderr)
    sys.exit(1)
for i, program in enumerate(programs):
    kind = "test" if re.search(r"^func Test[A-Z_]", program, re.M) else "plugin"
    d = os.path.join(out_dir, "%s%d" % (kind, i))
    os.makedirs(d, exist_ok=True)
    name = "guide_test.go" if kind == "test" else "main.go"
    open(os.path.join(d, name), "w", encoding="utf-8").write(program)
    if kind == "test":
        # A test file alone is not a package main program.
        open(os.path.join(d, "main.go"), "w", encoding="utf-8").write("package main\n\nfunc main() {}\n")
print(len(programs))
PY
)

failures=0
for dir in "$tmp_dir"/*/; do
  cat > "$dir/go.mod" <<MOD
module guidecheck

go 1.25
MOD
  (cd "$dir" \
    && go mod edit -require=github.com/torana-edge/torana-plugin-sdk@v0.0.0 \
         -replace="github.com/torana-edge/torana-plugin-sdk=$repo_root" \
    && go mod tidy >/dev/null 2>&1) || {
    echo "FAIL: could not resolve modules for $dir" >&2
    failures=$((failures + 1))
    continue
  }
  if [[ "$(basename "$dir")" == test* ]]; then
    # Compile the test binary on the host; run nothing.
    (cd "$dir" && go test -run '^$' -count=1 ./... >/dev/null) || {
      echo "FAIL: a docs/WRITING_A_PLUGIN.md test program does not compile ($dir)" >&2
      failures=$((failures + 1))
    }
  else
    (cd "$dir" && GOOS=wasip1 GOARCH=wasm go build -o /dev/null ./...) || {
      echo "FAIL: a docs/WRITING_A_PLUGIN.md plugin program does not compile for wasip1/wasm ($dir)" >&2
      failures=$((failures + 1))
    }
  fi
done

if [[ "$failures" -gt 0 ]]; then
  echo "$failures of $count guide program(s) do not compile" >&2
  exit 1
fi
echo "docs/WRITING_A_PLUGIN.md: all $count complete Go program(s) compile"
