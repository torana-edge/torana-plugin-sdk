fn main() {
    // The crate-local copy is required because crates.io packages cannot read
    // files outside their archive. CI diffs it against the repository's
    // canonical proto before every build.
    println!("cargo:rerun-if-changed=proto/torana/v1/torana.proto");
    prost_build::compile_protos(&["proto/torana/v1/torana.proto"], &["proto"])
        .expect("generate Torana ABI v1 bindings");
}
