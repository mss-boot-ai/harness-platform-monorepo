use serde::Serialize;

pub const PRODUCT_NAME: &str = "acp-brige-agent";
pub const AWP_VERSION: &str = "1.0";
pub const ACP_WIRE_VERSION: &str = "v1";
pub const ACP_RUST_SDK_VERSION: &str = "2.0.0";
pub const MINIMUM_RUST_VERSION: &str = "1.88.0";
pub const PLATFORM_BASE_TAG: &str = "v1.3.7";
pub const PLATFORM_BASE_COMMIT: &str = "77b53d41092741eac62fa6418c0bdbf87413c7cd";

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct BuildInfo {
    pub product: &'static str,
    pub version: &'static str,
    pub git_sha: &'static str,
    pub awp_version: &'static str,
    pub acp_wire_version: &'static str,
    pub acp_rust_sdk_version: &'static str,
    pub minimum_rust_version: &'static str,
    pub platform_base_tag: &'static str,
    pub platform_base_commit: &'static str,
}

#[must_use]
pub fn build_info() -> BuildInfo {
    BuildInfo {
        product: PRODUCT_NAME,
        version: env!("CARGO_PKG_VERSION"),
        git_sha: option_env!("ABA_GIT_SHA").unwrap_or("unknown"),
        awp_version: AWP_VERSION,
        acp_wire_version: ACP_WIRE_VERSION,
        acp_rust_sdk_version: ACP_RUST_SDK_VERSION,
        minimum_rust_version: MINIMUM_RUST_VERSION,
        platform_base_tag: PLATFORM_BASE_TAG,
        platform_base_commit: PLATFORM_BASE_COMMIT,
    }
}

#[cfg(test)]
mod tests {
    use super::{ACP_RUST_SDK_VERSION, ACP_WIRE_VERSION, PLATFORM_BASE_COMMIT, build_info};

    #[test]
    fn build_info_keeps_locked_protocol_and_platform_baselines() {
        let info = build_info();
        assert_eq!(info.acp_wire_version, ACP_WIRE_VERSION);
        assert_eq!(info.acp_rust_sdk_version, ACP_RUST_SDK_VERSION);
        assert_eq!(info.platform_base_commit, PLATFORM_BASE_COMMIT);
        assert_eq!(info.awp_version, "1.0");
    }
}
