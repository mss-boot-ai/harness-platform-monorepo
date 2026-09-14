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
use crate::config::{IsolationConfig, RuntimeProfile, WorkspaceProfile};

const MAX_RECORDS: usize = 4_096;
const MAX_STATE_BYTES: usize = 4 * 1024 * 1024;
const CLEANUP_TIMEOUT: Duration = Duration::from_secs(2);

#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Record {
    run: String,
    scope: String,
    boot: String,
    device: u64,
    inode: u64,
    workspace: PathBuf,
    workspace_device: u64,
    workspace_inode: u64,
    profile_digest: String,
    closed: bool,
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
}

fn unavailable<T>(_: T) -> ProcessError {
    ProcessError::CleanupUnconfirmed
}

impl Supervisor {
    pub fn open(config: &IsolationConfig) -> Result<Self, ProcessError> {
        let actual_root = current_cgroup()?;
        if canonical_safe_directory(&config.cgroup_root)? != actual_root
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
            .filter(|record| !record.closed)
            .cloned()
            .collect();
        for record in records {
            this.close_record(&record)?;
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
        file.write_all(b"{\"version\":1,\"records\":{}}")
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
        {
            return Err(ProcessError::UnsafeProfile);
        }
        let metadata = fs::metadata(&workspace_path).map_err(unavailable)?;
        let mut registry = self.registry.lock().map_err(unavailable)?;
        let id = hex(&run);
        if registry.state.records.contains_key(&id) {
            return Err(ProcessError::CleanupUnconfirmed); // Includes retired IDs: no respawn.
        }
        if registry.state.records.values().any(|record| {
            !record.closed
                && (record.workspace == workspace_path
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
        let group = self.config.cgroup_root.join(&scope_name);
        // An empty orphan from a crash here cannot have executed an Agent.
        fs::create_dir(&group).map_err(unavailable)?;
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
            device: group_metadata.dev(),
            inode: group_metadata.ino(),
            workspace: workspace_path,
            workspace_device: metadata.dev(),
            workspace_inode: metadata.ino(),
            profile_digest: hex(&Sha256::digest(&digest)),
            closed: false,
        };
        let mut next = registry.state.clone();
        next.records.insert(id.clone(), record.clone());
        // Durable scope claim precedes both spawning the trusted launcher and all runtime work.
        persist(&self.config.state_directory, &next)?;
        registry.state = next;
        registry.leases.insert(id, lease);
        Ok(Scope {
            supervisor: self.clone(),
            record,
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

    fn close_record(&self, record: &Record) -> Result<(), ProcessError> {
        if record.closed {
            return Ok(());
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
        let group = self.config.cgroup_root.join(&record.scope);
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
        let mut next = registry.state.clone();
        let current = next
            .records
            .get_mut(&record.run)
            .ok_or(ProcessError::CleanupUnconfirmed)?;
        if current.scope != record.scope || current.inode != record.inode {
            return Err(ProcessError::CleanupUnconfirmed);
        }
        current.closed = true;
        persist(&self.config.state_directory, &next)?;
        registry.state = next;
        registry.leases.remove(&record.run);
        // Empty scope may stay after a failure. Never remove a different/reused inode.
        if record.boot == self.boot
            && let Ok(metadata) = fs::symlink_metadata(&group)
            && metadata.dev() == record.device
            && metadata.ino() == record.inode
        {
            let _ = fs::remove_dir(group);
        }
        Ok(())
    }
}

impl Scope {
    pub(super) fn configure(
        &self,
        command: &mut Command,
        runtime: &RuntimeProfile,
    ) -> Result<(), ProcessError> {
        let home_root = self.supervisor.config.state_directory.join("runtime-homes");
        if !home_root.exists() {
            fs::create_dir(&home_root).map_err(unavailable)?;
        }
        fs::set_permissions(&home_root, fs::Permissions::from_mode(0o700)).map_err(unavailable)?;
        canonical_safe_directory(&home_root)?;
        let home = home_root.join(&self.record.run);
        fs::create_dir(&home).map_err(unavailable)?;
        fs::set_permissions(&home, fs::Permissions::from_mode(0o700)).map_err(unavailable)?;
        let config = &self.supervisor.config;
        command
            .arg("scope-exec")
            .arg("--cgroup")
            .arg(config.cgroup_root.join(&self.record.scope))
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
            .arg(std::process::id().to_string())
            .arg("--");
        command.args([
            "--unshare-user",
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
            .arg("--bind")
            .arg(home)
            .arg("/home/runtime")
            .arg("--bind")
            .arg(&self.record.workspace)
            .arg(&self.record.workspace)
            .arg("--chdir")
            .arg(&self.record.workspace)
            .arg("--")
            .arg(&runtime.command)
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
    if group.parent() != Some(parent.as_path()) || !valid_scope_name(name) || args.len() > 256 {
        return Err(ProcessError::UnsafeProfile);
    }
    let mut gate = open_gate(gate)?;
    lock_gate(&gate, rustix::fs::FlockOperation::NonBlockingLockShared)?;
    let mut gate_state = [0; 5];
    gate.read_exact(&mut gate_state).map_err(unavailable)?;
    if &gate_state != b"open\n" {
        return Err(ProcessError::CleanupUnconfirmed);
    }
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
    // The parent receives this before ACP initialize and never sends business work earlier.
    println!("{{\"scopeReady\":true}}");
    std::io::stdout().flush().map_err(unavailable)?;
    let mut child = Command::new("/usr/bin/bwrap")
        .args(args)
        .spawn()
        .map_err(unavailable)?;
    drop(gate); // All untrusted descendants now inherit the recorded scope.
    loop {
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
        let _ = self.close();
    }
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
    if state.version != 1
        || state.records.len() > MAX_RECORDS
        || state.records.iter().any(|(id, r)| {
            !valid_hex(id, 32)
                || id != &r.run
                || !valid_scope_name(&r.scope)
                || !r.scope.starts_with(&format!("harness-run-{id}-"))
                || !r.workspace.is_absolute()
                || r.workspace == Path::new("/")
                || !valid_hex(&r.profile_digest, 64)
                || r.boot.len() != 36
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

#[cfg(test)]
mod tests;
