use prost::Message;
use std::{env, fs, path::PathBuf};

fn main() -> Result<(), Box<dyn std::error::Error>> {
    let root = PathBuf::from(env::var("CARGO_MANIFEST_DIR")?).join("../../..");
    let proto = root.join("proto/candace/brainspine/v1/brainspine.proto");
    let refinements = root.join("pkg/liquidproto/v1/refinement.proto");
    println!("cargo:rerun-if-changed={}", proto.display());
    println!("cargo:rerun-if-changed={}", refinements.display());
    let descriptor = protox::compile([proto], [root.join("proto"), root.join("pkg")])?;
    let output = PathBuf::from(env::var("OUT_DIR")?);
    fs::write(output.join("descriptor.bin"), descriptor.encode_to_vec())?;
    prost_build::Config::new()
        .enable_type_names()
        .compile_fds(descriptor)?;
    Ok(())
}
