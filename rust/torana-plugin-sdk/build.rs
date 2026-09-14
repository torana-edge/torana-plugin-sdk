fn main() {
    // The crate-local copies are required because crates.io packages cannot
    // read files outside their archive. CI diffs them against the
    // repository's canonical protos before every build.
    println!("cargo:rerun-if-changed=proto/torana/v1/torana.proto");
    let mut config = prost_build::Config::new();
    let out_dir = std::path::PathBuf::from(std::env::var_os("OUT_DIR").expect("OUT_DIR"));
    config.file_descriptor_set_path(out_dir.join("torana.descriptor.bin"));
    config
        .compile_protos(&["proto/torana/v1/torana.proto"], &["proto"])
        .expect("generate Torana ABI v1 bindings");
}
