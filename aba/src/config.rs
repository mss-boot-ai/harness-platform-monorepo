use std::collections::BTreeSet;
use std::fs;
use std::path::{Path, PathBuf};

use serde::Deserialize;
use thiserror::Error;
use url::Url;

use crate::wire::MAX_WIRE_PACKET_BYTES;

pub const CONFIG_SCHEMA_VERSION: u32 = 1;
const MAX_IDENTIFIER_BYTES: usize = 64;
const MAX_DISPLAY_NAME_BYTES: usize = 128;
const MAX_AGENT_SESSIONS: u16 = 1_024;
const MIN_JOURNAL_BYTES: u64 = 1_048_576;
const MAX_JOURNAL_BYTES: u64 = 1_073_741_824;

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AgentConfig {
    pub schema_version: u32,
    pub platform: PlatformConfig,
    #[serde(default)]
    pub limits: Limits,
    #[serde(default, rename = "runtime")]
    pub runtimes: Vec<RuntimeProfile>,
    #[serde(default, rename = "workspace")]
    pub workspaces: Vec<WorkspaceProfile>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PlatformConfig {
    pub url: Url,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Limits {
    pub max_sessions: u16,
    pub max_packet_bytes: u32,
    pub max_inflight_per_channel: u32,
    pub journal_max_bytes: u64,
}

impl Default for Limits {
    fn default() -> Self {
        Self {
            max_sessions: 8,
            max_packet_bytes: MAX_WIRE_PACKET_BYTES,
            max_inflight_per_channel: 1_024,
            journal_max_bytes: 67_108_864,
        }
    }
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RuntimeProfile {
    pub id: String,
    pub display_name: String,
    pub command: PathBuf,
    #[serde(default)]
    pub args: Vec<String>,
    #[serde(default)]
    pub env_allow: Vec<String>,
    pub max_sessions: Option<u16>,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct WorkspaceProfile {
    pub id: String,
    pub display_name: String,
    pub path: PathBuf,
    #[serde(default)]
    pub allowed_runtimes: Vec<String>,
    #[serde(default)]
    pub follow_symlinks: bool,
}

#[derive(Debug, Error)]
pub enum ConfigError {
    #[error("failed to read ABA configuration file {path}")]
    Read {
        path: PathBuf,
        #[source]
        source: std::io::Error,
    },
    #[error("ABA configuration contains invalid TOML")]
    Parse(#[source] toml::de::Error),
    #[error(transparent)]
    Validation(#[from] ValidationError),
}

#[derive(Debug, Error, Clone, PartialEq, Eq)]
pub enum ValidationError {
    #[error("unsupported configuration schema version")]
    UnsupportedSchemaVersion,
    #[error("Platform URL must use HTTPS and contain no credentials, query, or fragment")]
    InvalidPlatformUrl,
    #[error("limits.max_sessions is outside the supported range")]
    InvalidMaxSessions,
    #[error("limits.max_packet_bytes is outside the AWP v1 supported range")]
    InvalidMaxPacketBytes,
    #[error("limits.max_inflight_per_channel is outside the supported range")]
    InvalidMaxInflight,
    #[error("limits.journal_max_bytes is outside the supported range")]
    InvalidJournalLimit,
    #[error("a runtime identifier is invalid or duplicated")]
    InvalidRuntimeId,
    #[error("a runtime display name is invalid")]
    InvalidRuntimeDisplayName,
    #[error("a runtime command must be an absolute local path")]
    InvalidRuntimeCommand,
    #[error("a runtime environment allow-list contains an invalid or duplicate name")]
    InvalidRuntimeEnvironment,
    #[error("a runtime max_sessions value is outside the allowed range")]
    InvalidRuntimeSessionLimit,
    #[error("a workspace identifier is invalid or duplicated")]
    InvalidWorkspaceId,
    #[error("a workspace display name is invalid")]
    InvalidWorkspaceDisplayName,
    #[error("a workspace path must be an absolute local path")]
    InvalidWorkspacePath,
    #[error("workspace.follow_symlinks is not supported by this ABA version")]
    UnsupportedWorkspaceSymlinks,
    #[error("a workspace references an unknown or duplicate runtime identifier")]
    InvalidWorkspaceRuntime,
}

impl AgentConfig {
    pub fn load(path: impl AsRef<Path>) -> Result<Self, ConfigError> {
        let path = path.as_ref();
        let contents = fs::read_to_string(path).map_err(|source| ConfigError::Read {
            path: path.to_path_buf(),
            source,
        })?;
        Self::parse(&contents)
    }

    pub fn parse(contents: &str) -> Result<Self, ConfigError> {
        let config: Self = toml::from_str(contents).map_err(ConfigError::Parse)?;
        config.validate()?;
        Ok(config)
    }

    pub fn validate(&self) -> Result<(), ValidationError> {
        if self.schema_version != CONFIG_SCHEMA_VERSION {
            return Err(ValidationError::UnsupportedSchemaVersion);
        }
        validate_platform_url(&self.platform.url)?;
        validate_limits(&self.limits)?;

        let mut runtime_ids = BTreeSet::new();
        for runtime in &self.runtimes {
            if !valid_identifier(&runtime.id) || !runtime_ids.insert(runtime.id.as_str()) {
                return Err(ValidationError::InvalidRuntimeId);
            }
            if !valid_display_name(&runtime.display_name) {
                return Err(ValidationError::InvalidRuntimeDisplayName);
            }
            if !runtime.command.is_absolute() {
                return Err(ValidationError::InvalidRuntimeCommand);
            }
            validate_runtime_environment(&runtime.env_allow)?;
            if let Some(max_sessions) = runtime.max_sessions
                && (max_sessions == 0 || max_sessions > self.limits.max_sessions)
            {
                return Err(ValidationError::InvalidRuntimeSessionLimit);
            }
        }

        let mut workspace_ids = BTreeSet::new();
        for workspace in &self.workspaces {
            if !valid_identifier(&workspace.id) || !workspace_ids.insert(workspace.id.as_str()) {
                return Err(ValidationError::InvalidWorkspaceId);
            }
            if !valid_display_name(&workspace.display_name) {
                return Err(ValidationError::InvalidWorkspaceDisplayName);
            }
            if !workspace.path.is_absolute() {
                return Err(ValidationError::InvalidWorkspacePath);
            }
            if workspace.follow_symlinks {
                return Err(ValidationError::UnsupportedWorkspaceSymlinks);
            }

            let mut allowed = BTreeSet::new();
            for runtime_id in &workspace.allowed_runtimes {
                if !runtime_ids.contains(runtime_id.as_str())
                    || !allowed.insert(runtime_id.as_str())
                {
                    return Err(ValidationError::InvalidWorkspaceRuntime);
                }
            }
        }

        Ok(())
    }
}

fn validate_platform_url(url: &Url) -> Result<(), ValidationError> {
    if url.scheme() != "https"
        || url.host_str().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(ValidationError::InvalidPlatformUrl);
    }
    Ok(())
}

fn validate_limits(limits: &Limits) -> Result<(), ValidationError> {
    if limits.max_sessions == 0 || limits.max_sessions > MAX_AGENT_SESSIONS {
        return Err(ValidationError::InvalidMaxSessions);
    }
    if !(4_096..=MAX_WIRE_PACKET_BYTES).contains(&limits.max_packet_bytes) {
        return Err(ValidationError::InvalidMaxPacketBytes);
    }
    if limits.max_inflight_per_channel == 0 || limits.max_inflight_per_channel > 1_024 {
        return Err(ValidationError::InvalidMaxInflight);
    }
    if !(MIN_JOURNAL_BYTES..=MAX_JOURNAL_BYTES).contains(&limits.journal_max_bytes) {
        return Err(ValidationError::InvalidJournalLimit);
    }
    Ok(())
}

fn validate_runtime_environment(names: &[String]) -> Result<(), ValidationError> {
    let mut unique = BTreeSet::new();
    for name in names {
        if !valid_environment_name(name) || !unique.insert(name.as_str()) {
            return Err(ValidationError::InvalidRuntimeEnvironment);
        }
    }
    Ok(())
}

fn valid_identifier(value: &str) -> bool {
    let bytes = value.as_bytes();
    if bytes.is_empty() || bytes.len() > MAX_IDENTIFIER_BYTES {
        return false;
    }
    if !bytes[0].is_ascii_lowercase() && !bytes[0].is_ascii_digit() {
        return false;
    }
    bytes.iter().all(|byte| {
        byte.is_ascii_lowercase()
            || byte.is_ascii_digit()
            || matches!(*byte, b'.' | b'_' | b'-')
    })
}

fn valid_display_name(value: &str) -> bool {
    !value.trim().is_empty()
        && value.len() <= MAX_DISPLAY_NAME_BYTES
        && !value.chars().any(char::is_control)
}

fn valid_environment_name(value: &str) -> bool {
    let mut bytes = value.bytes();
    let Some(first) = bytes.next() else {
        return false;
    };
    if first != b'_' && !first.is_ascii_uppercase() {
        return false;
    }
    bytes.all(|byte| byte == b'_' || byte.is_ascii_uppercase() || byte.is_ascii_digit())
}

#[cfg(test)]
mod tests {
    use super::{AgentConfig, ValidationError};

    const VALID_CONFIG: &str = r#"
schema_version = 1

[platform]
url = "https://platform.example.com"

[limits]
max_sessions = 8
max_packet_bytes = 1048576
max_inflight_per_channel = 1024
journal_max_bytes = 67108864

[[runtime]]
id = "codex-acp"
display_name = "Codex ACP"
command = "/usr/local/bin/codex-acp"
args = ["--acp"]
env_allow = ["HOME", "PATH"]
max_sessions = 2

[[workspace]]
id = "mss-boot-admin"
display_name = "mss-boot-admin"
path = "/workspace/mss-boot-admin"
allowed_runtimes = ["codex-acp"]
follow_symlinks = false
"#;

    #[test]
    fn accepts_strict_local_profiles() {
        let result = AgentConfig::parse(VALID_CONFIG);
        assert!(result.is_ok());
    }

    #[test]
    fn rejects_unknown_secret_like_fields() {
        let config = format!("{VALID_CONFIG}\nendpoint_private_key = \"do-not-store-here\"\n");
        let result = AgentConfig::parse(&config);
        assert!(result.is_err());
    }

    #[test]
    fn rejects_remote_command_paths() {
        let config = VALID_CONFIG.replace(
            "command = \"/usr/local/bin/codex-acp\"",
            "command = \"codex-acp\"",
        );
        let result = AgentConfig::parse(&config);
        assert!(matches!(
            result,
            Err(super::ConfigError::Validation(
                ValidationError::InvalidRuntimeCommand
            ))
        ));
    }

    #[test]
    fn rejects_unknown_workspace_runtime() {
        let config = VALID_CONFIG.replace(
            "allowed_runtimes = [\"codex-acp\"]",
            "allowed_runtimes = [\"unknown-runtime\"]",
        );
        let result = AgentConfig::parse(&config);
        assert!(matches!(
            result,
            Err(super::ConfigError::Validation(
                ValidationError::InvalidWorkspaceRuntime
            ))
        ));
    }

    #[test]
    fn rejects_following_workspace_symlinks_until_safely_implemented() {
        let config = VALID_CONFIG.replace("follow_symlinks = false", "follow_symlinks = true");
        let result = AgentConfig::parse(&config);
        assert!(matches!(
            result,
            Err(super::ConfigError::Validation(
                ValidationError::UnsupportedWorkspaceSymlinks
            ))
        ));
    }
}
