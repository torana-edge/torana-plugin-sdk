fn main() {
    println!("cargo:rerun-if-changed=../../proto/torana/v1/torana.proto");
    prost_build::compile_protos(&["../../proto/torana/v1/torana.proto"], &["../../proto"])
        .expect("generate Torana ABI v1 bindings");
}
