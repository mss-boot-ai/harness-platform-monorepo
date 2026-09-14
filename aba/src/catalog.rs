//! Safe discovery metadata, projected only from owner-approved local profiles.
use serde::Serialize;

use crate::config::{AgentConfig, ValidationError};

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct ExecutionCatalog {
    pub version: u32,
    pub runtimes: Vec<CatalogRuntime>,
    pub workspaces: Vec<CatalogWorkspace>,
}

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct CatalogRuntime {
    pub id: String,
    pub display_name: String,
}

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
#[serde(rename_all = "camelCase")]
pub struct CatalogWorkspace {
    pub id: String,
    pub display_name: String,
    pub runtime_ids: Vec<String>,
}

impl ExecutionCatalog {
    pub fn from_config(config: &AgentConfig) -> Result<Self, ValidationError> {
        if !config.publish_catalog {
            return Ok(Self {
                version: 1,
                runtimes: Vec::new(),
                workspaces: Vec::new(),
            });
        }
        let catalog = Self {
            version: 1,
            runtimes: config
                .runtimes
                .iter()
                .map(|runtime| CatalogRuntime {
                    id: runtime.id.clone(),
                    display_name: runtime.display_name.trim().to_owned(),
                })
                .collect(),
            workspaces: config
                .workspaces
                .iter()
                .filter(|workspace| !workspace.allowed_runtimes.is_empty())
                .map(|workspace| CatalogWorkspace {
                    id: workspace.id.clone(),
                    display_name: workspace.display_name.trim().to_owned(),
                    runtime_ids: workspace.allowed_runtimes.clone(),
                })
                .collect(),
        };
        let encoded = serde_json::to_vec(&catalog).map_err(|_| ValidationError::CatalogLimit)?;
        if encoded.len() > 12 * 1024 {
            return Err(ValidationError::CatalogLimit);
        }
        Ok(catalog)
    }
}

#[cfg(test)]
mod tests {
    use super::ExecutionCatalog;
    use crate::config::AgentConfig;

    #[test]
    fn catalog_requires_opt_in_and_does_not_publish_execution_secrets()
    -> Result<(), Box<dyn std::error::Error>> {
        let text = r#"
schema_version = 1
publish_catalog = true
[platform]
url = "https://platform.example"
[[runtime]]
id = "agent"
display_name = "My agent"
command = "/private/executable"
args = ["private-argument"]
env_allow = ["PRIVATE_API_KEY"]
[[workspace]]
id = "project"
display_name = "My project"
path = "/private/workspace"
allowed_runtimes = ["agent"]
"#;
        let mut config = AgentConfig::parse(text)?;
        let catalog = ExecutionCatalog::from_config(&config)?;
        let encoded = serde_json::to_string(&catalog)?;
        assert!(encoded.contains("My project"));
        assert!(encoded.contains("runtimeIds"));
        for forbidden in [
            "/private",
            "private-argument",
            "PRIVATE_API_KEY",
            "command",
            "args",
            "path",
            "env",
        ] {
            assert!(!encoded.contains(forbidden));
        }
        config.publish_catalog = false;
        assert!(
            ExecutionCatalog::from_config(&config)?
                .workspaces
                .is_empty()
        );
        Ok(())
    }
}
