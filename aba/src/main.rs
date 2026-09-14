use std::path::PathBuf;
use std::process::ExitCode;

use aba::config::AgentConfig;
use aba::gateway::GatewayClient;
use aba::identity::DevFileKeyStore;
use aba::identity::enrollment::EnrollmentClient;
use aba::journal::Journal;
use aba::process::AgentProcess;
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
    /// Internal, unprivileged scope launcher; never accepts input from the Platform.
    #[cfg(target_os = "linux")]
    #[command(hide = true)]
    ScopeExec {
        #[arg(long)]
        cgroup: PathBuf,
        #[arg(long)]
        device: u64,
        #[arg(long)]
        inode: u64,
        #[arg(long)]
        gate: PathBuf,
        #[arg(long)]
        parent: u32,
        #[arg(long)]
        provider_socket: Option<PathBuf>,
        #[arg(last = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
    /// Explicit first installation of an empty scope registry. Never overwrites existing state.
    #[cfg(target_os = "linux")]
    ScopeInit {
        #[arg(long)]
        directory: PathBuf,
    },
    #[cfg(target_os = "linux")]
    ScopeReconcile {
        #[arg(long)]
        config: PathBuf,
        #[arg(long)]
        insecure_loopback_development: bool,
    },
    #[cfg(target_os = "linux")]
    #[command(hide = true)]
    ScopeRuntime {
        #[arg(long)]
        provider_socket: Option<PathBuf>,
        #[arg(long)]
        runtime: PathBuf,
        #[arg(last = true, allow_hyphen_values = true)]
        args: Vec<String>,
    },
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
    /// Probe one locally allow-listed ACP runtime without connecting to Platform.
    Runtime(RuntimeArgs),
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
    /// Refresh credentials and complete one authenticated AWP connection handshake.
    Connect {
        #[arg(long, value_name = "PATH")]
        store: PathBuf,
        #[arg(long, value_name = "URL")]
        platform: Url,
        #[arg(long)]
        insecure_dev_keystore: bool,
    },
    /// Run the authenticated ABA control loop using strict local policy.
    Run {
        #[arg(long, value_name = "PATH")]
        config: PathBuf,
        #[arg(long, value_name = "PATH")]
        store: PathBuf,
        #[arg(long)]
        insecure_dev_keystore: bool,
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

#[derive(Debug, Args)]
struct RuntimeArgs {
    #[command(subcommand)]
    command: RuntimeCommand,
}

#[derive(Debug, Subcommand)]
enum RuntimeCommand {
    /// Start an ACP runtime and verify initialize plus session/new.
    Probe {
        #[arg(long, value_name = "PATH")]
        config: PathBuf,
        #[arg(long, value_name = "ID")]
        runtime: String,
        #[arg(long, value_name = "ID")]
        workspace: String,
        #[arg(long)]
        insecure_loopback_development: bool,
        /// Verify a real provider reply and a read-only workspace file tool using synthetic markers.
        #[arg(long)]
        exercise: bool,
        /// Keep the synthetic probe alive so the deployment verifier can inject Host death.
        #[arg(long, conflicts_with = "exercise")]
        hold_for_crash: bool,
    },
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
        #[cfg(target_os = "linux")]
        Command::ScopeInit { directory } => {
            aba::process::supervision::Supervisor::initialize_directory(&directory)?
        }
        #[cfg(target_os = "linux")]
        Command::ScopeReconcile {
            config,
            insecure_loopback_development,
        } => {
            let config =
                AgentConfig::load_with_loopback_development(config, insecure_loopback_development)?;
            let isolation = config
                .isolation
                .as_ref()
                .ok_or("scope reconciliation requires isolation")?;
            let _supervisor = aba::process::supervision::Supervisor::open(isolation)?;
            println!("Recorded process scopes reconciled; no runtime was started.");
        }
        #[cfg(target_os = "linux")]
        Command::ScopeExec {
            cgroup,
            device,
            inode,
            gate,
            parent,
            provider_socket,
            args,
        } => aba::process::supervision::enter_and_exec(
            &cgroup,
            device,
            inode,
            &gate,
            parent,
            provider_socket.as_deref(),
            &args,
        )?,
        #[cfg(target_os = "linux")]
        Command::ScopeRuntime {
            provider_socket,
            runtime,
            args,
        } => aba::process::provider::run_contained(provider_socket.as_deref(), &runtime, &args)?,
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
        Command::Runtime(RuntimeArgs {
            command:
                RuntimeCommand::Probe {
                    config,
                    runtime,
                    workspace,
                    insecure_loopback_development,
                    exercise,
                    hold_for_crash,
                },
        }) => {
            let config =
                AgentConfig::load_with_loopback_development(config, insecure_loopback_development)?;
            let runtime = config
                .runtimes
                .iter()
                .find(|candidate| candidate.id == runtime)
                .ok_or("runtime profile is not allow-listed")?;
            let workspace = config
                .workspaces
                .iter()
                .find(|candidate| candidate.id == workspace)
                .ok_or("workspace profile is not allow-listed")?;
            if !workspace.allowed_runtimes.contains(&runtime.id) {
                return Err("workspace does not allow the selected runtime".into());
            }
            if let Some(isolation) = &config.isolation {
                use rand_core::RngCore as _;
                let supervisor = aba::process::supervision::Supervisor::open(isolation)?;
                let mut id = [0; 16];
                rand_core::OsRng.fill_bytes(&mut id);
                let mut process =
                    AgentProcess::start_supervised(runtime, workspace, &supervisor, id)?;
                if hold_for_crash {
                    println!("Synthetic scope probe armed for Host-death injection.");
                    use std::io::Write as _;
                    std::io::stdout().flush()?;
                    loop {
                        std::thread::sleep(std::time::Duration::from_secs(1));
                    }
                }
                if exercise {
                    if isolation.network != aba::config::IsolationNetwork::CodexProvider {
                        return Err(
                            "real provider exercise requires codex_provider isolation".into()
                        );
                    }
                    let session: String = id.iter().map(|byte| format!("{byte:02x}")).collect();
                    for (number, text, expected) in [
                        (
                            1,
                            "Reply with exactly HARNESS_SCOPE_MODEL_OK.",
                            "HARNESS_SCOPE_MODEL_OK",
                        ),
                        (
                            2,
                            "Read scope-model-probe.txt in this workspace and reply with its exact contents. Do not write any files.",
                            "HARNESS_SCOPE_FILE_OK",
                        ),
                    ] {
                        let messages = process.prompt(&serde_json::to_vec(&serde_json::json!({
                            "jsonrpc":"2.0", "id":format!("scope-probe-{number}"), "method":"session/prompt",
                            "params":{"sessionId":session,"prompt":[{"type":"text","text":text}]}
                        }))?, &session)?;
                        let values: Vec<serde_json::Value> = messages
                            .iter()
                            .map(|message| serde_json::from_slice(message))
                            .collect::<Result<_, _>>()?;
                        let received: String = values
                            .iter()
                            .filter_map(|value| {
                                value
                                    .pointer("/params/update/content/text")
                                    .and_then(serde_json::Value::as_str)
                            })
                            .collect();
                        if !received.contains(expected)
                            || !values.iter().any(|value| {
                                value
                                    .pointer("/result/stopReason")
                                    .and_then(serde_json::Value::as_str)
                                    == Some("end_turn")
                            })
                        {
                            return Err(if number == 1 {
                                "isolated provider model reply failed"
                            } else {
                                "isolated provider read-only file tool failed"
                            }
                            .into());
                        }
                    }
                    println!("Isolated provider reply and read-only workspace tool confirmed.");
                    aba::process::probe::controls(&mut process, &supervisor, id, &workspace.path)?;
                }
                process.shutdown()?;
            } else {
                if hold_for_crash {
                    return Err("Host-death probe requires configured isolation".into());
                }
                if exercise {
                    return Err("real provider exercise requires configured isolation".into());
                }
                drop(AgentProcess::start(runtime, workspace)?);
            }
            println!("ACP runtime probe succeeded.");
        }
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
        Command::Connect {
            store,
            platform,
            insecure_dev_keystore,
        } => {
            let store = DevFileKeyStore::new(store, &platform, insecure_dev_keystore)?;
            let identity = store.load()?;
            let ready = GatewayClient::new(platform)?.connect(&identity, &store)?;
            println!(
                "gateway ready: endpoint {} generation {}",
                ready.endpoint_id, ready.connection_generation
            );
            println!("trust-root-jkt: {}", ready.root_jkt);
            ready.close()?;
        }
        Command::Run {
            config,
            store,
            insecure_dev_keystore,
        } => {
            let config =
                AgentConfig::load_with_loopback_development(config, insecure_dev_keystore)?;
            let journal_path = store
                .parent()
                .ok_or("ABA identity store must have a parent directory")?
                .join("relay-journal-v1.json");
            let journal = Journal::open(journal_path, config.limits.journal_max_bytes)?;
            let store = DevFileKeyStore::new(store, &config.platform.url, insecure_dev_keystore)?;
            let identity = store.load()?;
            GatewayClient::new(config.platform.url.clone())?
                .run(&identity, &store, &config, journal)?;
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
