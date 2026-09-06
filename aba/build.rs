fn main() -> Result<(), Box<dyn std::error::Error>> {
    let schema = "../protocol/proto/mss/awp/v1/wire.proto";
    println!("cargo:rerun-if-changed={schema}");
    let protoc = protoc_bin_vendored::protoc_bin_path()?;
    prost_build::Config::new()
        .protoc_executable(protoc)
        .compile_protos(&[schema], &["../protocol/proto"])?;
    Ok(())
}
