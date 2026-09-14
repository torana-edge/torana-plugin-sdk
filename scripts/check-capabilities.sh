#!/bin/sh
set -eu
exec python3 "$(dirname "$0")/generate-capabilities.py" --check
