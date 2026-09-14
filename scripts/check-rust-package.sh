#!/bin/sh
set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
manifest="$root/rust/torana-plugin-sdk/Cargo.toml"
version=$(sed -n 's/^version = "\([^"]*\)"$/\1/p' "$manifest" | head -1)
if [ -z "$version" ]; then
	echo "cannot read crate version from $manifest" >&2
	exit 1
fi

# Cargo's own verification builds from an unpacked crate. --allow-dirty lets
# contributors run this check while reviewing a manifest change; generated
# freshness is enforced independently before this script in CI and release.
cargo package --manifest-path "$manifest" --locked --allow-dirty --offline

archive="$root/rust/torana-plugin-sdk/target/package/torana-plugin-sdk-$version.crate"
if [ ! -f "$archive" ]; then
	echo "cargo package did not create $archive" >&2
	exit 1
fi

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT HUP INT TERM
tar -xzf "$archive" -C "$scratch"
package="$scratch/torana-plugin-sdk-$version"

# Compile a separate consumer against only the archive contents. This catches
# build.rs inputs, generated protobuf sources, and generated capability modules
# that exist in the repository but were omitted from Cargo.toml's include list.
mkdir -p "$scratch/consumer/src"
cat > "$scratch/consumer/Cargo.toml" <<EOF
[package]
name = "torana-packaged-consumer"
version = "0.0.0"
edition = "2021"

[dependencies]
torana-plugin-sdk = { path = "$package" }
EOF
cat > "$scratch/consumer/src/main.rs" <<'EOF'
use torana_plugin_sdk::{extension_call, host_call, pbv1, ABI_VERSION};

fn typecheck_packaged_api() {
    let args = pbv1::MetaGetArgs { key: "key".into() };
    let _typed = host_call("env.meta_get", &args);
    let _extension = extension_call("torana_plugin_counter", br#"{}"#);
    assert_eq!(ABI_VERSION, (1_u64 << 32) | 1);
}

fn main() {
    let _ = typecheck_packaged_api as fn();
}
EOF

cargo check --manifest-path "$scratch/consumer/Cargo.toml" --offline
