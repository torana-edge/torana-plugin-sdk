#!/bin/sh
set -eu
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
python3 scripts/generate-capabilities.py --check
go run ./scripts/helper-signatures --check
