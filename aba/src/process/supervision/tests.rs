use super::*;

fn fixture() -> Result<(tempfile::TempDir, Supervisor, Record), Box<dyn std::error::Error>> {
    let directory = tempfile::tempdir()?;
    let state_dir = directory.path().join("state");
    let root = directory.path().join("cgroups");
    let workspace = directory.path().join("project");
    fs::create_dir(&state_dir)?;
    fs::set_permissions(&state_dir, fs::Permissions::from_mode(0o700))?;
    fs::create_dir(&root)?;
    fs::create_dir(root.join("runs"))?;
    fs::create_dir(&workspace)?;
    Supervisor::initialize_directory(&state_dir)?;
    let run = "01".repeat(16);
    let name = format!("harness-run-{run}-{}", "02".repeat(16));
    let group = root.join("runs").join(&name);
    fs::create_dir(&group)?;
    fs::write(group.join("cgroup.kill"), b"")?;
    fs::write(group.join("cgroup.events"), b"populated 0\nfrozen 0\n")?;
    let gate = state_dir.join(format!("{run}.gate"));
    fs::write(&gate, b"open\n")?;
    fs::set_permissions(gate, fs::Permissions::from_mode(0o600))?;
    let metadata = fs::metadata(&group)?;
    let wm = fs::metadata(&workspace)?;
    let record = Record {
        run: run.clone(),
        scope: name,
        boot: "0".repeat(36),
        device: metadata.dev(),
        inode: metadata.ino(),
        workspace: workspace.clone(),
        workspace_device: wm.dev(),
        workspace_inode: wm.ino(),
        profile_digest: "0".repeat(64),
        runtime_id: "fixture".into(),
        workspace_id: "project".into(),
        closed: false,
    };
    let mut state = State {
        version: 1,
        records: BTreeMap::new(),
    };
    state.records.insert(run.clone(), record.clone());
    persist(&state_dir, &state)?;
    let lease = File::open(&workspace)?;
    rustix::fs::flock(&lease, rustix::fs::FlockOperation::NonBlockingLockExclusive)?;
    let supervisor = Supervisor {
        boot: record.boot.clone(),
        config: IsolationConfig {
            state_directory: state_dir.clone(),
            cgroup_root: root,
            runtime_roots: vec![],
            network: crate::config::IsolationNetwork::None,
            run_memory_bytes: 1_073_741_824,
            run_tasks: 96,
        },
        registry: Arc::new(Mutex::new(Registry {
            state,
            leases: BTreeMap::from([(run, lease)]),
            _lock: File::open(state_dir.join("registry.json"))?,
            faulted: false,
        })),
    };
    Ok((directory, supervisor, record))
}

#[test]
fn explicit_install_never_overwrites_existing_state() -> Result<(), Box<dyn std::error::Error>> {
    let directory = tempfile::tempdir()?;
    fs::set_permissions(directory.path(), fs::Permissions::from_mode(0o700))?;
    Supervisor::initialize_directory(directory.path())?;
    assert!(Supervisor::initialize_directory(directory.path()).is_err());
    assert_eq!(
        read_state(&directory.path().join("registry.json"))?.version,
        1
    );
    Ok(())
}

#[test]
fn whole_scope_kill_and_persisted_receipt_precede_lease_release()
-> Result<(), Box<dyn std::error::Error>> {
    let (_directory, supervisor, record) = fixture()?;
    supervisor.close_run([1; 16])?;
    assert_eq!(
        fs::read(
            supervisor
                .config
                .cgroup_root
                .join("runs")
                .join(&record.scope)
                .join("cgroup.kill")
        )?,
        b"1"
    );
    let state = read_state(&supervisor.config.state_directory.join("registry.json"))?;
    assert!(state.records[&record.run].closed);
    let lease = File::open(&record.workspace)?;
    rustix::fs::flock(&lease, rustix::fs::FlockOperation::NonBlockingLockExclusive)?;
    // Lost reply: retry exact original ID, never allocate or start another scope.
    supervisor.close_run([1; 16])?;
    assert!(supervisor.close_run([3; 16]).is_err());
    Ok(())
}

#[test]
fn still_populated_scope_keeps_claim_and_lease() -> Result<(), Box<dyn std::error::Error>> {
    let (_directory, supervisor, record) = fixture()?;
    fs::write(
        supervisor
            .config
            .cgroup_root
            .join("runs")
            .join(&record.scope)
            .join("cgroup.events"),
        b"populated 1\n",
    )?;
    assert!(supervisor.close_run([1; 16]).is_err());
    assert!(
        !read_state(&supervisor.config.state_directory.join("registry.json"))?.records[&record.run]
            .closed
    );
    let lease = File::open(&record.workspace)?;
    assert!(
        rustix::fs::flock(&lease, rustix::fs::FlockOperation::NonBlockingLockExclusive).is_err()
    );
    Ok(())
}

#[test]
fn replacement_inode_is_never_killed_or_released() -> Result<(), Box<dyn std::error::Error>> {
    let (_directory, supervisor, mut record) = fixture()?;
    record.inode += 1;
    assert!(supervisor.close_record(&record).is_err());
    assert!(
        fs::read(
            supervisor
                .config
                .cgroup_root
                .join("runs")
                .join(&record.scope)
                .join("cgroup.kill")
        )?
        .is_empty()
    );
    assert!(
        !supervisor
            .registry
            .lock()
            .map_err(unavailable)?
            .state
            .records[&record.run]
            .closed
    );
    Ok(())
}

#[test]
fn failed_commit_does_not_release_workspace() -> Result<(), Box<dyn std::error::Error>> {
    let (_directory, supervisor, record) = fixture()?;
    let path = supervisor.config.state_directory.join("registry.json");
    fs::rename(
        &path,
        supervisor.config.state_directory.join("retained.json"),
    )?;
    fs::create_dir(&path)?;
    assert!(supervisor.close_run([1; 16]).is_err());
    let registry = supervisor.registry.lock().map_err(unavailable)?;
    assert!(!registry.state.records[&record.run].closed);
    assert!(registry.leases.contains_key(&record.run));
    Ok(())
}

#[test]
fn close_fences_late_launch_before_emptiness_check() -> Result<(), Box<dyn std::error::Error>> {
    let (_directory, supervisor, record) = fixture()?;
    supervisor.close_run([1; 16])?;
    assert_eq!(
        fs::read(
            supervisor
                .config
                .state_directory
                .join(format!("{}.gate", record.run))
        )?,
        b"shut\n"
    );
    Ok(())
}

#[test]
fn launch_claim_is_durable_and_cannot_be_consumed_twice() -> Result<(), Box<dyn std::error::Error>>
{
    let (_directory, supervisor, record) = fixture()?;
    let path = supervisor
        .config
        .state_directory
        .join(format!("{}.gate", record.run));
    let mut first = open_gate(&path)?;
    claim_launch(&mut first)?;
    drop(first);
    assert_eq!(fs::read(&path)?, b"used\n");
    let mut second = open_gate(&path)?;
    assert!(claim_launch(&mut second).is_err());
    drop(second);
    supervisor.close_run([1; 16])?;
    let mut retired = open_gate(&path)?;
    assert!(claim_launch(&mut retired).is_err());
    Ok(())
}

#[test]
fn loader_environment_cannot_influence_the_trusted_helper() {
    for name in [
        "LD_PRELOAD",
        "LD_LIBRARY_PATH",
        "PYTHONPATH",
        "PYTHONSTARTUP",
        "BASH_ENV",
        "ENV",
        "OPENSSL_CONF",
        "GIT_CONFIG_GLOBAL",
    ] {
        assert!(!safe_runtime_env(name));
    }
    assert!(safe_runtime_env("HARNESS_CODEX_API_KEY"));
    assert!(safe_runtime_env("PATH"));
}

#[test]
fn busy_launch_gate_is_not_a_close_receipt() -> Result<(), Box<dyn std::error::Error>> {
    let (_directory, supervisor, record) = fixture()?;
    let gate = open_gate(
        &supervisor
            .config
            .state_directory
            .join(format!("{}.gate", record.run)),
    )?;
    rustix::fs::flock(&gate, rustix::fs::FlockOperation::NonBlockingLockShared)?;
    assert!(supervisor.close_run([1; 16]).is_err());
    assert!(
        fs::read(
            supervisor
                .config
                .cgroup_root
                .join("runs")
                .join(&record.scope)
                .join("cgroup.kill")
        )?
        .is_empty()
    );
    drop(gate);
    supervisor.close_run([1; 16])?;
    Ok(())
}

#[test]
fn reboot_does_not_signal_a_same_named_new_group() -> Result<(), Box<dyn std::error::Error>> {
    let (_directory, mut supervisor, record) = fixture()?;
    supervisor.boot = "1".repeat(36);
    supervisor.close_run([1; 16])?;
    assert!(
        fs::read(
            supervisor
                .config
                .cgroup_root
                .join("runs")
                .join(&record.scope)
                .join("cgroup.kill")
        )?
        .is_empty()
    );
    assert!(
        read_state(&supervisor.config.state_directory.join("registry.json"))?.records[&record.run]
            .closed
    );
    Ok(())
}

#[test]
fn malformed_registry_scope_or_version_fails_closed() -> Result<(), Box<dyn std::error::Error>> {
    let (_directory, supervisor, record) = fixture()?;
    let mut state = supervisor
        .registry
        .lock()
        .map_err(unavailable)?
        .state
        .clone();
    state
        .records
        .get_mut(&record.run)
        .ok_or("record missing")?
        .scope = "../../another-service".into();
    assert!(validate_state(&state).is_err());
    state.records.clear();
    state.version = 2;
    assert!(validate_state(&state).is_err());
    assert!(!valid_scope_name("harness-run-../-scope"));
    Ok(())
}

#[test]
fn missing_population_proof_is_not_empty() -> Result<(), Box<dyn std::error::Error>> {
    let (_directory, supervisor, record) = fixture()?;
    let group = supervisor
        .config
        .cgroup_root
        .join("runs")
        .join(&record.scope);
    fs::write(group.join("cgroup.events"), b"frozen 0\n")?;
    assert!(group_empty(&group).is_err());
    assert!(supervisor.close_run([1; 16]).is_err());
    Ok(())
}
