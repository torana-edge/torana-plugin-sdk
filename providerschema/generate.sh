#!/bin/sh
# generate.sh — deterministic re-vendoring of the provider schema snapshot.
#
# The checked-in authority is snapshot.gen.go, GENERATED from the vendored
# artifacts in source/ (never a hand-written mirror). This script
#
#   1. verifies the vendored artifacts against source/manifest.json digests,
#   2. regenerates snapshot.gen.go,
#   3. proves determinism (two renders, byte-identical),
#   4. runs the offline suite.
#
# Update workflow (documented, deliberate):
#
#   1. Fetch the new upstream protos at their EXACT commit SHAs and replace
#      the vendored files + manifest entries (upstream_commit_sha, path,
#      url, sha256, fetched_at). Never pin a moving branch: the SHA is the
#      immutable input.
#   2. Run ./generate.sh. The parser is strict: materially different
#      upstream syntax surfaces as a parse error — a reviewed act, never a
#      silent parse.
#   3. Review the snapshot.gen.go diff (the schema facts), then commit the
#      artifacts, manifest, generated file, and any decision changes in
#      snapshot.go together.
#
# Adversarial pins enforced by the offline tests (no network):
#   - changing a vendored artifact without re-vendoring the manifest digest
#     fails TestSnapshotArtifactDigestsPinned;
#   - changing the manifest SHA/digest without regenerating fails
#     TestSnapshotGeneratedExact (provenance header + bytes);
#   - the update command is byte-identical across runs (determinism proof
#     below + TestSnapshotGeneratedExact re-renders in memory).
set -eu
cd "$(dirname "$0")"

tmp1=$(mktemp)
tmp2=$(mktemp)
trap 'rm -f "$tmp1" "$tmp2"' EXIT

echo "generate: pass 1"
GOWORK=off go run generate.go
cp snapshot.gen.go "$tmp1"

echo "generate: pass 2 (determinism)"
GOWORK=off go run generate.go
cp snapshot.gen.go "$tmp2"
if ! cmp -s "$tmp1" "$tmp2"; then
  echo "generate: FAIL — two renders are not byte-identical" >&2
  exit 1
fi

echo "generate: offline suite"
GOWORK=off go test ./...
echo "generate: ok — snapshot.gen.go regenerated deterministically"
