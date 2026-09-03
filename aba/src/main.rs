use std::path::PathBuf;
use std::process::ExitCode;

use aba::config::AgentConfig;
use aba::version::{PRODUCT_NAME, build_info};
use clap::{Args, Parser, Subcommand};

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
    }
    Ok(())
}
