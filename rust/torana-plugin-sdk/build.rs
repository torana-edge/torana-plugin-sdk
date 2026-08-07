fn main() {
    // The crate-local copies are required because crates.io packages cannot
    // read files outside their archive. CI diffs them against the
    // repository's canonical protos before every build.
    println!("cargo:rerun-if-changed=proto/torana/v2/torana.proto");
    prost_build::compile_protos(&["proto/torana/v2/torana.proto"], &["proto"])
        .expect("generate Torana ABI v2 bindings");
}
