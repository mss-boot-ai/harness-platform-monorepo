use std::path::PathBuf;
use std::process::ExitCode;

use aba::config::AgentConfig;
use aba::identity::DevFileKeyStore;
use aba::identity::enrollment::EnrollmentClient;
use aba::version::{PRODUCT_NAME, build_info};
use clap::{Args, Parser, Subcommand};
use url::Url;

#[derive(Debug, Parser)]
#[command(name = "aba", version, about = "Lightweight local ACP bridge agent")]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Debug, Subcommand)]
enum Command {
    /// Print build, protocol, SDK, and Platform baseline information.
    Version {
        /// Emit machine-readable JSON.
        #[arg(long)]
        json: bool,
    },
    /// Validate ABA local configuration without starting a connection or agent.
    Config(ConfigArgs),
    /// Manage the ABA endpoint identity.
    Identity(IdentityArgs),
    /// Enroll this ABA with a loopback development Platform.
    Enroll {
        #[arg(long, value_name = "PATH")]
        store: PathBuf,
        #[arg(long, value_name = "URL")]
        platform: Url,
        #[arg(long, default_value = "Local ABA")]
        name: String,
        #[arg(long)]
        insecure_dev_keystore: bool,
        #[arg(long, default_value_t = 600)]
        timeout_seconds: u64,
    },
}

#[derive(Debug, Args)]
struct IdentityArgs {
    #[command(subcommand)]
    command: IdentityCommand,
}

#[derive(Debug, Subcommand)]
enum IdentityCommand {
    /// Generate an explicit loopback-only development identity.
    Init {
        #[arg(long, value_name = "PATH")]
        store: PathBuf,
        #[arg(long, value_name = "URL")]
        platform: Url,
        #[arg(long)]
        insecure_dev_keystore: bool,
        #[arg(long)]
        json: bool,
    },
    /// Inspect the public identity summary without printing secrets.
    Inspect {
        #[arg(long, value_name = "PATH")]
        store: PathBuf,
        #[arg(long, value_name = "URL")]
        platform: Url,
        #[arg(long)]
        insecure_dev_keystore: bool,
        #[arg(long)]
        json: bool,
    },
}

#[derive(Debug, Args)]
struct ConfigArgs {
    #[command(subcommand)]
    command: ConfigCommand,
}

#[derive(Debug, Subcommand)]
enum ConfigCommand {
    /// Strictly parse and validate a local ABA TOML configuration.
    Validate {
        /// Path to the local ABA configuration file.
        #[arg(long, value_name = "PATH")]
        config: PathBuf,
    },
}

fn main() -> ExitCode {
    match execute(Cli::parse()) {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("{PRODUCT_NAME}: {error}");
            ExitCode::FAILURE
        }
    }
}

fn execute(cli: Cli) -> Result<(), Box<dyn std::error::Error>> {
    match cli.command {
        Command::Version { json } => {
            let info = build_info();
            if json {
                println!("{}", serde_json::to_string_pretty(&info)?);
            } else {
                println!("{} {}", info.product, info.version);
                println!("git-sha: {}", info.git_sha);
                println!("awp: {}", info.awp_version);
                println!("acp-wire: {}", info.acp_wire_version);
                println!("acp-rust-sdk: {}", info.acp_rust_sdk_version);
                println!("minimum-rust: {}", info.minimum_rust_version);
                println!(
                    "platform-base: {} ({})",
                    info.platform_base_tag, info.platform_base_commit
                );
            }
        }
        Command::Config(ConfigArgs {
            command: ConfigCommand::Validate { config },
        }) => {
            AgentConfig::load(config)?;
            println!("ABA configuration is valid.");
        }
        Command::Identity(IdentityArgs { command }) => match command {
            IdentityCommand::Init {
                store,
                platform,
                insecure_dev_keystore,
                json,
            } => {
                let summary =
                    DevFileKeyStore::new(store, &platform, insecure_dev_keystore)?.initialize()?;
                print_identity_summary(&summary, json)?;
            }
            IdentityCommand::Inspect {
                store,
                platform,
                insecure_dev_keystore,
                json,
            } => {
                let summary = DevFileKeyStore::new(store, &platform, insecure_dev_keystore)?
                    .load()?
                    .summary()?;
                print_identity_summary(&summary, json)?;
            }
        },
        Command::Enroll {
            store,
            platform,
            name,
            insecure_dev_keystore,
            timeout_seconds,
        } => {
            let store = DevFileKeyStore::new(store, &platform, insecure_dev_keystore)?;
            let identity = store.load()?;
            let endpoint_id = EnrollmentClient::new(platform)?.enroll(
                &identity,
                &store,
                &name,
                std::time::Duration::from_secs(timeout_seconds.clamp(10, 900)),
                |display| {
                    println!("verification-uri: {}", display.verification_uri);
                    println!("user-code: {}", display.user_code);
                    println!("waiting for approval...");
                },
            )?;
            println!("enrollment complete: endpoint {endpoint_id}");
        }
    }
    Ok(())
}

fn print_identity_summary(
    summary: &aba::identity::IdentitySummary,
    json: bool,
) -> Result<(), serde_json::Error> {
    if json {
        println!("{}", serde_json::to_string_pretty(summary)?);
    } else {
        println!("assurance: {}", summary.assurance);
        println!("signing-jkt: {}", summary.signing_jkt);
        println!("kem-jkt: {}", summary.kem_jkt);
    }
    Ok(())
}
