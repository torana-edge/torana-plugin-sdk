#!/bin/sh
# generate.sh — deterministic re-vendoring of the provider schema snapshot.
#
# Update workflow (documented, deliberate):
#   1. Review the upstream member changes against the two pinned protos:
#        https://raw.githubusercontent.com/googleapis/googleapis/master/google/ai/generativelanguage/v1beta/content.proto
#        https://raw.githubusercontent.com/googleapis/googleapis/master/google/cloud/aiplatform/v1/content.proto
#   2. Edit snapshot.go to reflect the reviewed tables, record the new fetch
#      date in SnapshotRevision, and re-run:
#        ./generate.sh
#   3. The inventory test (snapshot_test.go) must pass offline; commit the
#      snapshot and the test change together.
#
# The snapshot is the source of truth for OFFLINE tests and CI — the test
# never needs the network. This script only prints the expected commands;
# the tables themselves are reviewed by a human before being committed.
set -eu
echo "provider schema snapshot regeneration (reviewed, offline):"
echo "  1. fetch and review:"
echo "     curl -sL https://raw.githubusercontent.com/googleapis/googleapis/master/google/ai/generativelanguage/v1beta/content.proto"
echo "     curl -sL https://raw.githubusercontent.com/googleapis/googleapis/master/google/cloud/aiplatform/v1/content.proto"
echo "  2. update snapshot.go tables + SnapshotRevision"
echo "  3. go test ./providerschema/ (offline)"
