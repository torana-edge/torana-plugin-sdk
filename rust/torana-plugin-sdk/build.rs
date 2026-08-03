fn main() {
    // The crate-local copies are required because crates.io packages cannot
    // read files outside their archive. CI diffs them against the
    // repository's canonical protos before every build.
    println!("cargo:rerun-if-changed=proto/torana/v1/torana.proto");
    prost_build::compile_protos(&["proto/torana/v1/torana.proto"], &["proto"])
        .expect("generate Torana ABI v1 bindings");
    // v2 is generated alongside v1 while the migration is in flight; v1 goes
    // when the host, both SDKs and the official plugins are on v2.
    println!("cargo:rerun-if-changed=proto/torana/v2/torana.proto");
    prost_build::compile_protos(&["proto/torana/v2/torana.proto"], &["proto"])
        .expect("generate Torana ABI v2 bindings");
}
