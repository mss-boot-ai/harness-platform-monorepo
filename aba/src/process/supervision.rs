//! Linux cgroup-v2 ownership; not a business-operation journal.
//! No root RPC, PID-only kill, or runtime access to the delegated cgroup filesystem.

use std::collections::BTreeMap;
use std::fs::{self, File, OpenOptions};
use std::io::{Read as _, Seek as _, Write as _};
use std::os::unix::fs::{MetadataExt as _, OpenOptionsExt as _, PermissionsExt as _};
use std::path::{Path, PathBuf};
use std::process::Command;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant};

use rand_core::{OsRng, RngCore as _};
use serde::{Deserialize, Serialize};
use sha2::{Digest as _, Sha256};

use super::{ProcessError, canonical_safe_directory, canonical_safe_file};
use crate::config::{IsolationConfig, IsolationNetwork, RuntimeProfile, WorkspaceProfile};

const MAX_RECORDS: usize = 4_096;
const MAX_STATE_BYTES: usize = 4 * 1024 * 1024;
const CLEANUP_TIMEOUT: Duration = Duration::from_secs(2);

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Record {
    run: String,
    scope: String,
    boot: String,
    /// None can be parsed from historical v1 records, but is never invented on recovery.
    #[serde(default)]
    delegation_root: Option<PathBuf>,
    device: u64,
    inode: u64,
    workspace: PathBuf,
    workspace_device: u64,
    workspace_inode: u64,
    profile_digest: String,
    runtime_id: String,
    workspace_id: String,
    closed: bool,
    #[serde(default)]
    resource_faults: Option<ResourceFaults>,
}

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct ResourceFaults {
    pids_max: u64,
    memory_oom: u64,
    memory_oom_kill: u64,
}

#[derive(Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct State {
    version: u32,
    records: BTreeMap<String, Record>,
}

struct Registry {
    state: State,
    // A failed cleanup keeps the inode lease here, even when AgentProcess is dropped.
    leases: BTreeMap<String, File>,
    _lock: File,
    faulted: bool,
}

#[derive(Clone)]
pub struct Supervisor {
    config: IsolationConfig,
    boot: String,
    registry: Arc<Mutex<Registry>>,
}

pub(super) struct Scope {
    supervisor: Supervisor,
    record: Record,
    cleanup_on_drop: bool,
}

fn unavailable<T>(_: T) -> ProcessError {
    ProcessError::CleanupUnconfirmed
}

impl Supervisor {
    pub fn open(config: &IsolationConfig) -> Result<Self, ProcessError> {
        let actual_root = current_cgroup()?;
        if (canonical_safe_directory(&config.cgroup_root)? != actual_root
            && config.cgroup_root.join("host") != actual_root)
            || !config.cgroup_root.join("cgroup.kill").exists()
            || !fs::read_to_string("/proc/self/mountinfo")
                .map_err(unavailable)?
                .lines()
                .any(|line| line.contains(" /sys/fs/cgroup ") && line.contains(" - cgroup2 "))
        {
            return Err(ProcessError::UnsafeProfile);
        }
        let state_path = canonical_safe_directory(&config.state_directory)?;
        if fs::metadata(&state_path)
            .map_err(unavailable)?
            .permissions()
            .mode()
            & 0o077
            != 0
        {
            return Err(ProcessError::UnsafeProfile);
        }
        for path in &config.runtime_roots {
            let canonical = fs::canonicalize(path).map_err(unavailable)?;
            let metadata = fs::symlink_metadata(path).map_err(unavailable)?;
            if canonical != *path
                || !path.starts_with("/opt/harness")
                || path == Path::new("/opt/harness")
                || metadata.permissions().mode() & 0o022 != 0
                || metadata.uid() != 0
                || path.starts_with(&state_path)
                || state_path.starts_with(path)
            {
                return Err(ProcessError::UnsafeProfile);
            }
        }
        let lock = OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .mode(0o600)
            .custom_flags(rustix::fs::OFlags::NOFOLLOW.bits() as i32)
            .open(state_path.join("registry.lock"))
            .map_err(unavailable)?;
        rustix::fs::flock(&lock, rustix::fs::FlockOperation::NonBlockingLockExclusive)
            .map_err(unavailable)?;
        let path = state_path.join("registry.json");
        let state = if path.exists() {
            read_state(&path)?
        } else {
            // Provision explicitly. Missing registry after installation is never a clean reset.
            return Err(ProcessError::CleanupUnconfirmed);
        };
        validate_state(&state)?;
        let boot = boot_id()?;
        let this = Self {
            config: config.clone(),
            boot,
            registry: Arc::new(Mutex::new(Registry {
                state,
                leases: BTreeMap::new(),
                _lock: lock,
                faulted: false,
            })),
        };
        // Fresh Host never re-executes an old Run. This only retires its process scope.
        let records: Vec<_> = this
            .registry
            .lock()
            .map_err(unavailable)?
            .state
            .records
            .values()
            .cloned()
            .collect();
        for record in records {
            this.close_record(&record)?;
        }
        establish_topology(&config.cgroup_root)?;
        // Do not silently adopt unknown remnants, including a prepared directory whose
        // record failed to commit. An operator must reconcile it using the private evidence.
        for entry in fs::read_dir(config.cgroup_root.join("runs")).map_err(unavailable)? {
            let entry = entry.map_err(unavailable)?;
            if entry.file_type().map_err(unavailable)?.is_dir() {
                return Err(ProcessError::CleanupUnconfirmed);
            }
        }
        Ok(this)
    }

    /// Explicit installation action. Never called by normal run/recovery startup.
    pub fn initialize_directory(path: &Path) -> Result<(), ProcessError> {
        let path = canonical_safe_directory(path)?;
        if fs::metadata(&path)
            .map_err(unavailable)?
            .permissions()
            .mode()
            & 0o077
            != 0
        {
            return Err(ProcessError::UnsafeProfile);
        }
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(path.join("registry.json"))
            .map_err(unavailable)?;
        file.write_all(b"{\"version\":2,\"records\":{}}")
            .and_then(|()| file.sync_all())
            .map_err(unavailable)?;
        File::open(path)
            .and_then(|file| file.sync_all())
            .map_err(unavailable)
    }

    pub(super) fn prepare(
        &self,
        runtime: &RuntimeProfile,
        workspace: &WorkspaceProfile,
        run: [u8; 16],
    ) -> Result<Scope, ProcessError> {
        let workspace_path = canonical_safe_directory(&workspace.path)?;
        let command = canonical_safe_file(&runtime.command)?;
        if workspace_path.starts_with(&self.config.state_directory)
            || self.config.state_directory.starts_with(&workspace_path)
            || self
                .config
                .runtime_roots
                .iter()
                .any(|root| root.starts_with(&workspace_path) || workspace_path.starts_with(root))
            || !(command.starts_with("/usr/")
                || self
                    .config
                    .runtime_roots
                    .iter()
                    .any(|root| command.starts_with(root)))
            || run == [0; 16]
            || !workspace_path.starts_with("/srv/harness-workspaces")
            || workspace_path == Path::new("/srv/harness-workspaces")
            || runtime.env_allow.iter().any(|name| !safe_runtime_env(name))
        {
            return Err(ProcessError::UnsafeProfile);
        }
        let metadata = fs::metadata(&workspace_path).map_err(unavailable)?;
        let mut registry = self.registry.lock().map_err(unavailable)?;
        if registry.faulted {
            return Err(ProcessError::CleanupUnconfirmed);
        }
        let id = hex(&run);
        if registry.state.records.contains_key(&id) {
            return Err(ProcessError::CleanupUnconfirmed); // Includes retired IDs: no respawn.
        }
        if registry.state.records.values().any(|record| {
            !record.closed
                && (workspaces_overlap(&record.workspace, &workspace_path)
                    || (record.workspace_device == metadata.dev()
                        && record.workspace_inode == metadata.ino()))
        }) {
            return Err(ProcessError::WorkspaceBusy);
        }
        if registry.state.records.len() >= MAX_RECORDS {
            return Err(ProcessError::Limit);
        }
        let lease = File::open(&workspace_path).map_err(unavailable)?;
        rustix::fs::flock(&lease, rustix::fs::FlockOperation::NonBlockingLockExclusive)
            .map_err(|_| ProcessError::WorkspaceBusy)?;
        let mut nonce = [0u8; 16];
        OsRng.fill_bytes(&mut nonce);
        let scope_name = format!("harness-run-{}-{}", id, hex(&nonce));
        let group = self.config.cgroup_root.join("runs").join(&scope_name);
        // An empty orphan from a crash here cannot have executed an Agent.
        fs::create_dir(&group).map_err(unavailable)?;
        fs::write(
            group.join("memory.max"),
            self.config.run_memory_bytes.to_string(),
        )
        .map_err(unavailable)?;
        fs::write(group.join("memory.oom.group"), b"1").map_err(unavailable)?;
        fs::write(group.join("pids.max"), self.config.run_tasks.to_string())
            .map_err(unavailable)?;
        fs::write(group.join("cpu.max"), b"200000 100000").map_err(unavailable)?;
        let group_metadata = fs::metadata(&group).map_err(unavailable)?;
        let mut gate = OpenOptions::new()
            .read(true)
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(self.config.state_directory.join(format!("{id}.gate")))
            .map_err(unavailable)?;
        gate.write_all(b"open\n")
            .and_then(|()| gate.sync_all())
            .map_err(unavailable)?;
        let digest = serde_json::to_vec(&(
            runtime.id.as_str(),
            &command,
            &runtime.args,
            &runtime.env_allow,
            workspace.id.as_str(),
            &workspace_path,
        ))
        .map_err(unavailable)?;
        let record = Record {
            run: id.clone(),
            scope: scope_name,
            boot: self.boot.clone(),
            delegation_root: Some(self.config.cgroup_root.clone()),
            device: group_metadata.dev(),
            inode: group_metadata.ino(),
            workspace: workspace_path,
            workspace_device: metadata.dev(),
            workspace_inode: metadata.ino(),
            profile_digest: hex(&Sha256::digest(&digest)),
            runtime_id: runtime.id.clone(),
            workspace_id: workspace.id.clone(),
            closed: false,
            resource_faults: None,
        };
        let mut next = registry.state.clone();
        next.records.insert(id.clone(), record.clone());
        // Durable scope claim precedes both spawning the trusted launcher and all runtime work.
        if let Err(error) = persist(&self.config.state_directory, &next) {
            registry.faulted = true;
            registry.leases.insert(id, lease);
            return Err(error);
        }
        registry.state = next;
        registry.leases.insert(id, lease);
        Ok(Scope {
            supervisor: self.clone(),
            record,
            cleanup_on_drop: true,
        })
    }

    /// Unknown ID is not an ALREADY_CLOSED proof. Call off the Gateway I/O thread.
    pub fn close_run(&self, run: [u8; 16]) -> Result<(), ProcessError> {
        let record = self
            .registry
            .lock()
            .map_err(unavailable)?
            .state
            .records
            .get(&hex(&run))
            .cloned()
            .ok_or(ProcessError::CleanupUnconfirmed)?;
        self.close_record(&record)
    }

    /// Read-only diagnostic for the explicit synthetic CLI probe. Never a PID kill authority.
    pub fn has_process_command(
        &self,
        run: [u8; 16],
        expected: &[&str],
    ) -> Result<bool, ProcessError> {
        let record = self
            .registry
            .lock()
            .map_err(unavailable)?
            .state
            .records
            .get(&hex(&run))
            .cloned()
            .ok_or(ProcessError::CleanupUnconfirmed)?;
        if record.closed || record.boot != self.boot || expected.is_empty() {
            return Ok(false);
        }
        let group = self.config.cgroup_root.join("runs").join(&record.scope);
        let metadata = fs::symlink_metadata(&group).map_err(unavailable)?;
        if metadata.dev() != record.device || metadata.ino() != record.inode {
            return Err(ProcessError::CleanupUnconfirmed);
        }
        for pid in fs::read_to_string(group.join("cgroup.procs"))
            .map_err(unavailable)?
            .lines()
            .take(1_024)
        {
            let Ok(pid) = pid.parse::<u32>() else {
                return Err(ProcessError::CleanupUnconfirmed);
            };
            let Ok(bytes) = fs::read(format!("/proc/{pid}/cmdline")) else {
                continue;
            };
            let values: Vec<_> = bytes
                .split(|byte| *byte == 0)
                .filter(|value| !value.is_empty())
                .collect();
            if values.len() == expected.len()
                && std::str::from_utf8(values[0])
                    .ok()
                    .and_then(|value| Path::new(value).file_name())
                    .is_some_and(|value| value == expected[0])
                && values
                    .iter()
                    .skip(1)
                    .zip(expected.iter().skip(1))
                    .all(|(left, right)| *left == right.as_bytes())
            {
                return Ok(true);
            }
        }
        Ok(false)
    }

    fn close_record(&self, record: &Record) -> Result<(), ProcessError> {
        self.close_record_inner(record, true)
    }

    /// Explicit local diagnostic: persist the real close transition but leave its empty
    /// directory, so a fresh process can test the post-commit/pre-removal recovery cut.
    /// This creates a new non-executing scope and never changes an existing runtime.
    pub fn probe_interrupted_retirement(
        &self,
        runtime: &RuntimeProfile,
        workspace: &WorkspaceProfile,
    ) -> Result<(), ProcessError> {
        let mut id = [0; 16];
        OsRng.fill_bytes(&mut id);
        let mut scope = self.prepare(runtime, workspace, id)?;
        self.close_record_inner(&scope.record, false)?;
        scope.cleanup_on_drop = false;
        Ok(())
    }

    fn close_record_inner(
        &self,
        record: &Record,
        remove_directory: bool,
    ) -> Result<(), ProcessError> {
        self.verify_delegation(record)?;
        {
            let registry = self.registry.lock().map_err(unavailable)?;
            if registry.faulted {
                return Err(ProcessError::CleanupUnconfirmed);
            }
            let current = registry
                .state
                .records
                .get(&record.run)
                .ok_or(ProcessError::CleanupUnconfirmed)?;
            if current.scope != record.scope || current.inode != record.inode {
                return Err(ProcessError::CleanupUnconfirmed);
            }
            if current.closed {
                drop(registry);
                if let Some(group) = self.closed_scope_remnant(record)?
                    && remove_directory
                {
                    fs::remove_dir(group).map_err(unavailable)?;
                }
                return Ok(());
            }
        }
        // Serialize with the trusted launcher's join+spawn. A late starter cannot enter
        // after the emptiness proof, including when the previous Host died before join.
        let mut gate = open_gate(
            &self
                .config
                .state_directory
                .join(format!("{}.gate", record.run)),
        )?;
        lock_gate(&gate, rustix::fs::FlockOperation::NonBlockingLockExclusive)?;
        gate.rewind()
            .and_then(|()| gate.write_all(b"shut\n"))
            .and_then(|()| gate.sync_all())
            .map_err(unavailable)?;
        let group = self.config.cgroup_root.join("runs").join(&record.scope);
        if record.boot == self.boot {
            match fs::symlink_metadata(&group) {
                Ok(metadata) => {
                    if !metadata.is_dir()
                        || metadata.file_type().is_symlink()
                        || metadata.dev() != record.device
                        || metadata.ino() != record.inode
                    {
                        return Err(ProcessError::CleanupUnconfirmed);
                    }
                    // cgroup.kill includes descendants that changed PID/session/process group.
                    // Do not infer success from the leader, a signal, or an empty in-memory map.
                    fs::write(group.join("cgroup.kill"), b"1").map_err(unavailable)?;
                    let deadline = Instant::now() + CLEANUP_TIMEOUT;
                    loop {
                        if group_empty(&group)? {
                            break;
                        }
                        if Instant::now() >= deadline {
                            return Err(ProcessError::CleanupUnconfirmed);
                        }
                        std::thread::sleep(Duration::from_millis(10));
                    }
                }
                // A populated cgroup cannot be removed. The recorded, random name is never reused;
                // the launcher validates its inode before joining and before running any Agent.
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
                Err(error) => return Err(unavailable(error)),
            }
        }
        // Changed boot ID proves previous-boot processes are gone, not that their operations succeeded.
        let mut registry = self.registry.lock().map_err(unavailable)?;
        if registry.faulted {
            return Err(ProcessError::CleanupUnconfirmed);
        }
        let mut next = registry.state.clone();
        let current = next
            .records
            .get_mut(&record.run)
            .ok_or(ProcessError::CleanupUnconfirmed)?;
        if current.scope != record.scope || current.inode != record.inode {
            return Err(ProcessError::CleanupUnconfirmed);
        }
        current.closed = true;
        if record.boot == self.boot && group.exists() {
            current.resource_faults = resource_faults(&group).ok();
        }
        if let Err(error) = persist(&self.config.state_directory, &next) {
            registry.faulted = true;
            return Err(error);
        }
        registry.state = next;
        registry.leases.remove(&record.run);
        // Empty scope may stay after a failure. Never remove a different/reused inode.
        if remove_directory
            && record.boot == self.boot
            && let Ok(metadata) = fs::symlink_metadata(&group)
            && metadata.dev() == record.device
            && metadata.ino() == record.inode
        {
            let _ = fs::remove_dir(group);
        }
        Ok(())
    }

    // Resume retirement after a crash between the durable close and rmdir. A known
    // closed directory is removable only with its sealed gate, exact inode, and no live tasks.
    fn closed_scope_remnant(&self, record: &Record) -> Result<Option<PathBuf>, ProcessError> {
        self.verify_delegation(record)?;
        if record.boot != self.boot {
            return Ok(None);
        } // Never inspect a current-boot namesake.
        let group = self.config.cgroup_root.join("runs").join(&record.scope);
        let metadata = match fs::symlink_metadata(&group) {
            Ok(metadata) => metadata,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(error) => return Err(unavailable(error)),
        };
        if record.boot != self.boot
            || !metadata.is_dir()
            || metadata.file_type().is_symlink()
            || metadata.dev() != record.device
            || metadata.ino() != record.inode
        {
            return Err(ProcessError::CleanupUnconfirmed);
        }
        let mut gate = open_gate(
            &self
                .config
                .state_directory
                .join(format!("{}.gate", record.run)),
        )?;
        lock_gate(&gate, rustix::fs::FlockOperation::NonBlockingLockExclusive)?;
        let mut value = [0; 5];
        gate.read_exact(&mut value).map_err(unavailable)?;
        if &value != b"shut\n" || !group_empty(&group)? {
            return Err(ProcessError::CleanupUnconfirmed);
        }
        Ok(Some(group))
    }

    fn verify_delegation(&self, record: &Record) -> Result<(), ProcessError> {
        let original = record
            .delegation_root
            .as_ref()
            .ok_or(ProcessError::ScopeMigrationRequired)?;
        if record.boot == self.boot && original != &self.config.cgroup_root {
            return Err(ProcessError::ScopeMigrationRequired);
        }
        Ok(())
    }
}

fn workspaces_overlap(left: &Path, right: &Path) -> bool {
    left.starts_with(right) || right.starts_with(left)
}

impl Scope {
    pub(super) fn configure(
        &self,
        command: &mut Command,
        runtime: &RuntimeProfile,
    ) -> Result<(), ProcessError> {
        let metadata =
            fs::metadata(canonical_safe_directory(&self.record.workspace)?).map_err(unavailable)?;
        let digest = serde_json::to_vec(&(
            runtime.id.as_str(),
            canonical_safe_file(&runtime.command)?,
            &runtime.args,
            &runtime.env_allow,
            self.record.workspace_id.as_str(),
            &self.record.workspace,
        ))
        .map_err(unavailable)?;
        if metadata.dev() != self.record.workspace_device
            || metadata.ino() != self.record.workspace_inode
            || runtime.id != self.record.runtime_id
            || hex(&Sha256::digest(&digest)) != self.record.profile_digest
        {
            return Err(ProcessError::UnsafeProfile);
        }
        let home_root = self.supervisor.config.state_directory.join("runtime-homes");
        match fs::create_dir(&home_root) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {}
            Err(error) => return Err(unavailable(error)),
        }
        canonical_safe_directory(&home_root)?;
        fs::set_permissions(&home_root, fs::Permissions::from_mode(0o700)).map_err(unavailable)?;
        let home = home_root.join(&self.record.run);
        fs::create_dir(&home).map_err(unavailable)?;
        fs::set_permissions(&home, fs::Permissions::from_mode(0o700)).map_err(unavailable)?;
        let config = &self.supervisor.config;
        let helper = canonical_safe_file(&std::env::current_exe().map_err(unavailable)?)?;
        let helper_metadata = fs::metadata(&helper).map_err(unavailable)?;
        if helper_metadata.uid() != 0 || helper_metadata.permissions().mode() & 0o022 != 0 {
            return Err(ProcessError::UnsafeProfile);
        }
        let provider_socket = config
            .state_directory
            .join(format!("{}.provider.sock", self.record.run));
        command
            .arg("scope-exec")
            .arg("--cgroup")
            .arg(config.cgroup_root.join("runs").join(&self.record.scope))
            .arg("--device")
            .arg(self.record.device.to_string())
            .arg("--inode")
            .arg(self.record.inode.to_string())
            .arg("--gate")
            .arg(
                config
                    .state_directory
                    .join(format!("{}.gate", self.record.run)),
            )
            .arg("--parent")
            .arg(std::process::id().to_string());
        if config.network == IsolationNetwork::CodexProvider {
            command.arg("--provider-socket").arg(&provider_socket);
        }
        command.arg("--");
        command.args([
            "--unshare-user",
            "--unshare-net",
            "--unshare-pid",
            "--unshare-ipc",
            "--unshare-uts",
            "--unshare-cgroup",
            "--die-with-parent",
            "--new-session",
            "--cap-drop",
            "ALL",
            "--ro-bind",
            "/usr",
            "/usr",
            "--symlink",
            "usr/bin",
            "/bin",
            "--symlink",
            "usr/sbin",
            "/sbin",
            "--symlink",
            "usr/lib",
            "/lib",
            "--symlink",
            "usr/lib64",
            "/lib64",
            "--proc",
            "/proc",
            "--dev",
            "/dev",
            "--tmpfs",
            "/tmp",
            "--tmpfs",
            "/run",
            "--dir",
            "/etc",
            "--dir",
            "/home",
        ]);
        for path in [
            "/etc/hosts",
            "/etc/nsswitch.conf",
            "/etc/ssl/certs",
            "/etc/resolv.conf",
        ] {
            // Resolver is often a symlink into /run. Copy only this reviewed mount, never /run.
            command
                .arg("--ro-bind")
                .arg(fs::canonicalize(path).map_err(unavailable)?)
                .arg(path);
        }
        for root in &config.runtime_roots {
            command.arg("--ro-bind").arg(root).arg(root);
        }
        command
            .arg("--ro-bind")
            .arg(helper)
            .arg("/opt/harness/aba-internal");
        if config.network == IsolationNetwork::CodexProvider {
            command
                .arg("--ro-bind")
                .arg(provider_socket)
                .arg("/run/harness-provider.sock");
        }
        command
            .arg("--bind")
            .arg(home)
            .arg("/home/runtime")
            .arg("--bind")
            .arg(&self.record.workspace)
            .arg(&self.record.workspace)
            .arg("--chdir")
            .arg(&self.record.workspace)
            .arg("--")
            .arg("/opt/harness/aba-internal")
            .arg("scope-runtime");
        if config.network == IsolationNetwork::CodexProvider {
            command
                .arg("--provider-socket")
                .arg("/run/harness-provider.sock");
        }
        command
            .arg("--runtime")
            .arg(&runtime.command)
            .arg("--")
            .args(&runtime.args)
            .env("HOME", "/home/runtime");
        Ok(())
    }
    pub(super) fn close(&self) -> Result<(), ProcessError> {
        self.supervisor.close_record(&self.record)
    }
}

/// Trusted pre-exec helper: executes as the existing service UID, never setuid/root.
/// There is no listener and no privileged generic-command endpoint.
pub fn enter_and_exec(
    group: &Path,
    device: u64,
    inode: u64,
    gate: &Path,
    parent_id: u32,
    provider_socket: Option<&Path>,
    args: &[String],
) -> Result<(), ProcessError> {
    use rustix::event::{PollFd, PollFlags, Timespec, poll};
    use rustix::process::{Pid, PidfdFlags, getppid, pidfd_open};
    let parent_pid = Pid::from_raw(i32::try_from(parent_id).map_err(unavailable)?)
        .ok_or(ProcessError::UnsafeProfile)?;
    if getppid() != Some(parent_pid) {
        return Err(ProcessError::UnsafeProfile);
    }
    let owner = pidfd_open(parent_pid, PidfdFlags::empty()).map_err(unavailable)?;
    if getppid() != Some(parent_pid) {
        return Err(ProcessError::UnsafeProfile);
    }
    let parent = current_cgroup()?;
    let name = group
        .file_name()
        .and_then(|name| name.to_str())
        .ok_or(ProcessError::UnsafeProfile)?;
    let delegated = parent.parent().ok_or(ProcessError::UnsafeProfile)?;
    if parent.file_name().is_none_or(|name| name != "host")
        || group.parent() != Some(delegated.join("runs").as_path())
        || !valid_scope_name(name)
        || args.len() > 256
    {
        return Err(ProcessError::UnsafeProfile);
    }
    let gate_path = gate;
    let mut gate = open_gate(gate_path)?;
    claim_launch(&mut gate)?;
    let metadata = fs::symlink_metadata(group).map_err(unavailable)?;
    if !metadata.is_dir()
        || metadata.file_type().is_symlink()
        || metadata.dev() != device
        || metadata.ino() != inode
    {
        return Err(ProcessError::UnsafeProfile);
    }
    fs::write(group.join("cgroup.procs"), std::process::id().to_string()).map_err(unavailable)?;
    if current_cgroup()? != group {
        return Err(ProcessError::UnsafeProfile);
    }
    let provider_health = if let Some(socket) = provider_socket {
        let run = name
            .strip_prefix("harness-run-")
            .and_then(|name| name.split_once('-'))
            .map(|(run, _)| run)
            .ok_or(ProcessError::UnsafeProfile)?;
        let directory = gate_path.parent().ok_or(ProcessError::UnsafeProfile)?;
        if gate_path != directory.join(format!("{run}.gate"))
            || socket != directory.join(format!("{run}.provider.sock"))
        {
            return Err(ProcessError::UnsafeProfile);
        }
        Some(super::provider::start_host_proxy(socket)?)
    } else {
        None
    };
    // The parent receives this before ACP initialize and never sends business work earlier.
    println!("{{\"scopeReady\":true}}");
    std::io::stdout().flush().map_err(unavailable)?;
    let mut command = Command::new("/usr/bin/bwrap");
    command.args(args).env_clear();
    for name in [
        "HOME",
        "PATH",
        "LANG",
        "LC_ALL",
        "TZ",
        "HARNESS_CODEX_MODEL",
        "HARNESS_CODEX_MODELS",
    ] {
        if let Some(value) = std::env::var_os(name) {
            command.env(name, value);
        }
    }
    if provider_socket.is_some() {
        command
            .env("HARNESS_CODEX_API_BASE_URL", "http://127.0.0.1:39121/v1")
            .env("HARNESS_CODEX_API_KEY", "local-isolated-provider")
            .env("HARNESS_CODEX_PROVIDER_TRANSPORT", "local-isolated-v1");
    }
    let mut child = command.spawn().map_err(unavailable)?;
    drop(gate); // All untrusted descendants now inherit the recorded scope.
    loop {
        if provider_health
            .as_ref()
            .is_some_and(|flag| !flag.load(std::sync::atomic::Ordering::Acquire))
        {
            let _ = child.kill();
            return Err(ProcessError::Transport);
        }
        if let Some(status) = child.try_wait().map_err(unavailable)? {
            return if status.success() {
                Ok(())
            } else {
                Err(ProcessError::Transport)
            };
        }
        // pidfd tracks the parent process, not the short-lived Rust startup thread.
        // No PID reuse signal and no PR_SET_PDEATHSIG thread-exit trap.
        let mut owners = [PollFd::new(&owner, PollFlags::IN)];
        if poll(
            &mut owners,
            Some(&Timespec {
                tv_sec: 0,
                tv_nsec: 100_000_000,
            }),
        )
        .map_err(unavailable)?
            > 0
        {
            fs::write(group.join("cgroup.kill"), b"1").map_err(unavailable)?;
            return Err(ProcessError::CleanupUnconfirmed);
        }
    }
}

impl Drop for Scope {
    fn drop(&mut self) {
        if self.cleanup_on_drop {
            let _ = self.close();
        }
    }
}

fn establish_topology(root: &Path) -> Result<(), ProcessError> {
    for leaf in ["host", "runs"] {
        let path = root.join(leaf);
        match fs::create_dir(&path) {
            Ok(()) => {}
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {}
            Err(error) => return Err(unavailable(error)),
        }
        canonical_safe_directory(&path)?;
    }
    fs::write(
        root.join("host/cgroup.procs"),
        std::process::id().to_string(),
    )
    .map_err(unavailable)?;
    if !fs::read_to_string(root.join("cgroup.procs"))
        .map_err(unavailable)?
        .trim()
        .is_empty()
    {
        return Err(ProcessError::CleanupUnconfirmed);
    }
    for path in [root.to_path_buf(), root.join("runs")] {
        let available = fs::read_to_string(path.join("cgroup.controllers")).map_err(unavailable)?;
        if ["cpu", "memory", "pids"]
            .iter()
            .any(|wanted| !available.split_whitespace().any(|got| got == *wanted))
        {
            return Err(ProcessError::UnsafeProfile);
        }
        fs::write(path.join("cgroup.subtree_control"), b"+cpu +memory +pids")
            .map_err(unavailable)?;
    }
    let memory: u64 = fs::read_to_string(root.join("memory.max"))
        .map_err(unavailable)?
        .trim()
        .parse()
        .map_err(unavailable)?;
    let tasks: u32 = fs::read_to_string(root.join("pids.max"))
        .map_err(unavailable)?
        .trim()
        .parse()
        .map_err(unavailable)?;
    if memory < 1_073_741_824 || tasks < 128 {
        return Err(ProcessError::UnsafeProfile);
    }
    // Aggregate runtime budgets reserve 25% memory and 64 tasks for the Host and cleanup.
    fs::write(root.join("runs/memory.max"), (memory / 4 * 3).to_string()).map_err(unavailable)?;
    fs::write(root.join("runs/pids.max"), (tasks - 64).to_string()).map_err(unavailable)?;
    Ok(())
}

pub(super) fn safe_runtime_env(name: &str) -> bool {
    matches!(name, "HOME" | "PATH" | "LANG" | "LC_ALL" | "TZ")
        || name.starts_with("HARNESS_CODEX_")
        || name.starts_with("MSS_HARNESS_")
}

fn claim_launch(gate: &mut File) -> Result<(), ProcessError> {
    lock_gate(gate, rustix::fs::FlockOperation::NonBlockingLockExclusive)?;
    let mut gate_state = [0; 5];
    gate.rewind()
        .and_then(|()| gate.read_exact(&mut gate_state))
        .map_err(unavailable)?;
    if &gate_state != b"open\n" {
        return Err(ProcessError::CleanupUnconfirmed);
    }
    gate.rewind()
        .and_then(|()| gate.write_all(b"used\n"))
        .and_then(|()| gate.sync_all())
        .map_err(unavailable)
}

fn open_gate(path: &Path) -> Result<File, ProcessError> {
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .custom_flags(rustix::fs::OFlags::NOFOLLOW.bits() as i32)
        .open(path)
        .map_err(unavailable)?;
    let metadata = file.metadata().map_err(unavailable)?;
    if !metadata.is_file() || metadata.len() != 5 || metadata.permissions().mode() & 0o077 != 0 {
        return Err(ProcessError::UnsafeProfile);
    }
    Ok(file)
}
fn lock_gate(file: &File, operation: rustix::fs::FlockOperation) -> Result<(), ProcessError> {
    let deadline = Instant::now() + CLEANUP_TIMEOUT;
    loop {
        match rustix::fs::flock(file, operation) {
            Ok(()) => return Ok(()),
            Err(rustix::io::Errno::WOULDBLOCK) if Instant::now() < deadline => {
                std::thread::sleep(Duration::from_millis(10))
            }
            Err(_) => return Err(ProcessError::CleanupUnconfirmed),
        }
    }
}

fn current_cgroup() -> Result<PathBuf, ProcessError> {
    let content = fs::read_to_string("/proc/self/cgroup").map_err(unavailable)?;
    let value = content
        .strip_prefix("0::/")
        .ok_or(ProcessError::UnsafeProfile)?
        .trim_end();
    if value.is_empty()
        || value.contains('\n')
        || value.split('/').any(|part| part == ".." || part == ".")
    {
        return Err(ProcessError::UnsafeProfile);
    }
    canonical_safe_directory(&Path::new("/sys/fs/cgroup").join(value))
}
fn boot_id() -> Result<String, ProcessError> {
    let value = fs::read_to_string("/proc/sys/kernel/random/boot_id").map_err(unavailable)?;
    let value = value.trim().to_owned();
    if value.len() != 36
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() || byte == b'-')
    {
        return Err(ProcessError::CleanupUnconfirmed);
    }
    Ok(value)
}
fn hex(value: &[u8]) -> String {
    value.iter().map(|byte| format!("{byte:02x}")).collect()
}
fn valid_hex(value: &str, len: usize) -> bool {
    value.len() == len
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
}
fn valid_scope_name(name: &str) -> bool {
    name.strip_prefix("harness-run-")
        .and_then(|rest| rest.split_once('-'))
        .is_some_and(|(run, nonce)| valid_hex(run, 32) && valid_hex(nonce, 32))
}
fn validate_state(state: &State) -> Result<(), ProcessError> {
    if state.version != 2
        || state
            .records
            .values()
            .any(|record| record.delegation_root.is_none())
    {
        return Err(ProcessError::ScopeMigrationRequired);
    }
    if state.records.len() > MAX_RECORDS
        || state.records.iter().any(|(id, r)| {
            !valid_hex(id, 32)
                || id != &r.run
                || !valid_scope_name(&r.scope)
                || !r.scope.starts_with(&format!("harness-run-{id}-"))
                || !r.workspace.is_absolute()
                || r.workspace == Path::new("/")
                || !valid_hex(&r.profile_digest, 64)
                || r.boot.len() != 36
                || r.delegation_root.as_ref().is_none_or(|root| {
                    !root.is_absolute()
                        || root == Path::new("/")
                        || root.components().any(|part| {
                            matches!(
                                part,
                                std::path::Component::ParentDir | std::path::Component::CurDir
                            )
                        })
                })
        })
    {
        return Err(ProcessError::CleanupUnconfirmed);
    }
    Ok(())
}
fn read_state(path: &Path) -> Result<State, ProcessError> {
    let metadata = fs::symlink_metadata(path).map_err(unavailable)?;
    if !metadata.is_file()
        || metadata.file_type().is_symlink()
        || metadata.len() > MAX_STATE_BYTES as u64
        || metadata.permissions().mode() & 0o077 != 0
    {
        return Err(ProcessError::CleanupUnconfirmed);
    }
    serde_json::from_slice(&fs::read(path).map_err(unavailable)?).map_err(unavailable)
}
fn persist(directory: &Path, state: &State) -> Result<(), ProcessError> {
    validate_state(state)?;
    let encoded = serde_json::to_vec(state).map_err(unavailable)?;
    if encoded.len() > MAX_STATE_BYTES {
        return Err(ProcessError::Limit);
    }
    let mut temporary = tempfile::NamedTempFile::new_in(directory).map_err(unavailable)?;
    temporary
        .as_file_mut()
        .write_all(&encoded)
        .and_then(|()| temporary.as_file().sync_all())
        .map_err(unavailable)?;
    temporary
        .persist(directory.join("registry.json"))
        .map_err(unavailable)?;
    File::open(directory)
        .and_then(|file| file.sync_all())
        .map_err(unavailable)
}
fn group_empty(group: &Path) -> Result<bool, ProcessError> {
    let content = fs::read_to_string(group.join("cgroup.events")).map_err(unavailable)?;
    match content
        .lines()
        .find_map(|line| line.strip_prefix("populated "))
    {
        Some("0") => Ok(true),
        Some("1") => Ok(false),
        _ => Err(ProcessError::CleanupUnconfirmed),
    }
}

fn resource_faults(group: &Path) -> Result<ResourceFaults, ProcessError> {
    let pids = fs::read_to_string(group.join("pids.events")).map_err(unavailable)?;
    let memory = fs::read_to_string(group.join("memory.events")).map_err(unavailable)?;
    fn value(text: &str, key: &str) -> Result<u64, ProcessError> {
        text.lines()
            .find_map(|line| {
                line.split_once(' ')
                    .filter(|(name, _)| *name == key)
                    .map(|(_, value)| value)
            })
            .ok_or(ProcessError::CleanupUnconfirmed)?
            .parse()
            .map_err(unavailable)
    }
    Ok(ResourceFaults {
        pids_max: value(&pids, "max")?,
        memory_oom: value(&memory, "oom")?,
        memory_oom_kill: value(&memory, "oom_kill")?,
    })
}

#[cfg(test)]
mod tests;
