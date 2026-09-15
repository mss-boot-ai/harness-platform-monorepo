//! Local ACP process boundary with non-blocking bidirectional dispatch.
//! Gateway calls submit/poll; prompt remains only for the CLI probe and legacy tests.
pub mod probe;
#[cfg(target_os = "linux")]
pub mod provider;
#[cfg(target_os = "linux")]
pub mod supervision;
#[cfg(not(target_os = "linux"))]
#[path = "process/supervision_unsupported.rs"]
pub mod supervision;
mod transport;

use std::collections::{BTreeMap, BTreeSet, VecDeque};
use std::fs;
use std::process::{Child, Command, Stdio};
use std::sync::mpsc::{Receiver, RecvTimeoutError, SyncSender, TryRecvError};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::schema::v1::{
    InitializeRequest, InitializeResponse, NewSessionRequest, NewSessionResponse, PromptRequest,
};
use rand_core::{OsRng, RngCore as _};
use serde_json::{Value, json};
use thiserror::Error;

use crate::config::{RuntimeProfile, WorkspaceProfile};
use transport::{QUEUE_DEPTH, TransportEvent, WriteCommand};

const MAX_ACP_MESSAGE_BYTES: usize = 64 * 1024;
const MAX_TURN_MESSAGES: usize = 256;
const START_TIMEOUT: Duration = Duration::from_secs(10);
const TURN_TIMEOUT: Duration = Duration::from_secs(3_600);
const REQUEST_TIMEOUT: Duration = Duration::from_secs(30);
const APPROVAL_TIMEOUT: Duration = Duration::from_secs(300);

#[derive(Debug, Error, Clone, Copy, PartialEq, Eq)]
pub enum ProcessError {
    #[error("local agent profile is no longer safe")]
    UnsafeProfile,
    #[error("local agent could not be started")]
    Spawn,
    #[error("local agent transport failed")]
    Transport,
    #[error("local agent protocol message is invalid")]
    Protocol,
    #[error("local agent response timed out")]
    Timeout,
    #[error("local agent response exceeded a bounded limit")]
    Limit,
    #[error("local workspace already has an agent process")]
    WorkspaceBusy,
    #[error("local process scope cleanup is not confirmed")]
    CleanupUnconfirmed,
    #[error("scope records require explicit reconciliation in their original delegation")]
    ScopeMigrationRequired,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct AgentEvent {
    /// Empty for a durable transport-dispatch receipt, not an ACP execution result.
    pub message: Vec<u8>,
    pub completed_dispatch: Option<[u8; 16]>,
}

struct RequestBinding {
    original_id: Value,
    dispatch_id: [u8; 16],
    method: String,
    requested_value: Option<Value>,
    deadline: Instant,
}

struct PermissionBinding {
    original_id: Value,
    option_ids: BTreeSet<String>,
    deadline: Instant,
}

pub struct AgentProcess {
    child: Option<Child>,
    // Directory inode lock: aliases and other ABA processes cannot bypass it.
    _workspace_lease: Option<fs::File>,
    scope: Option<supervision::Scope>,
    writer: Option<SyncSender<WriteCommand>>,
    receiver: Option<Receiver<TransportEvent>>,
    threads: Vec<JoinHandle<()>>,
    agent_session_id: String,
    platform_session_id: Option<String>,
    initialized: Value,
    session_info: Value,
    requests: BTreeMap<String, RequestBinding>,
    permissions: BTreeMap<String, PermissionBinding>,
    immediate: VecDeque<AgentEvent>,
    active_turn: Option<String>,
    next_id: u64,
    process_epoch: u64,
}

impl AgentProcess {
    pub fn start(
        runtime: &RuntimeProfile,
        workspace: &WorkspaceProfile,
    ) -> Result<Self, ProcessError> {
        Self::start_inner(runtime, workspace, None)
    }

    pub fn start_supervised(
        runtime: &RuntimeProfile,
        workspace: &WorkspaceProfile,
        supervisor: &supervision::Supervisor,
        run: [u8; 16],
    ) -> Result<Self, ProcessError> {
        let scope = supervisor.prepare(runtime, workspace, run)?;
        Self::start_inner(runtime, workspace, Some(scope))
    }

    fn start_inner(
        runtime: &RuntimeProfile,
        workspace: &WorkspaceProfile,
        scope: Option<supervision::Scope>,
    ) -> Result<Self, ProcessError> {
        let command_path = canonical_safe_file(&runtime.command)?;
        let workspace_path = canonical_safe_directory(&workspace.path)?;
        let workspace_lease = if scope.is_none() {
            let lease = fs::File::open(&workspace_path).map_err(|_| ProcessError::UnsafeProfile)?;
            #[cfg(unix)]
            rustix::fs::flock(&lease, rustix::fs::FlockOperation::NonBlockingLockExclusive)
                .map_err(|_| ProcessError::WorkspaceBusy)?;
            Some(lease)
        } else {
            None
        };
        let mut command = Command::new(if scope.is_some() {
            std::env::current_exe().map_err(|_| ProcessError::Spawn)?
        } else {
            command_path
        });
        command
            .current_dir(&workspace_path)
            .env_clear()
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::null());
        for name in &runtime.env_allow {
            if let Some(value) = std::env::var_os(name) {
                command.env(name, value);
            }
        }
        if let Some(scope) = &scope {
            scope.configure(&mut command, runtime)?;
        } else {
            command.args(&runtime.args);
        }
        #[cfg(unix)]
        {
            use std::os::unix::process::CommandExt as _;
            command.process_group(0);
        }
        let mut child = command.spawn().map_err(|_| ProcessError::Spawn)?;
        let Some(stdin) = child.stdin.take() else {
            terminate_child(&mut child);
            return Err(ProcessError::Spawn);
        };
        let Some(stdout) = child.stdout.take() else {
            terminate_child(&mut child);
            return Err(ProcessError::Spawn);
        };
        let pumps = match transport::spawn(stdin, stdout) {
            Ok(value) => value,
            Err(error) => {
                terminate_child(&mut child);
                return Err(error);
            }
        };
        let mut process = Self {
            child: Some(child),
            _workspace_lease: workspace_lease,
            scope,
            writer: Some(pumps.writer),
            receiver: Some(pumps.events),
            threads: pumps.threads,
            agent_session_id: String::new(),
            platform_session_id: None,
            initialized: Value::Null,
            session_info: Value::Null,
            requests: BTreeMap::new(),
            permissions: BTreeMap::new(),
            immediate: VecDeque::new(),
            active_turn: None,
            next_id: 0,
            process_epoch: OsRng.next_u64(),
        };
        if process.scope.is_some() {
            let ready = process.receive(Instant::now() + START_TIMEOUT)?;
            if ready != json!({"scopeReady": true}) {
                return Err(ProcessError::UnsafeProfile);
            }
        }
        process.initialize(&workspace_path)?;
        Ok(process)
    }

    /// Caller must run this off the Gateway I/O loop. Drop alone is never a close receipt.
    pub fn shutdown(&mut self) -> Result<(), ProcessError> {
        self.receiver.take();
        self.writer.take();
        if let Some(child) = &mut self.child {
            terminate_child(child);
        }
        if let Some(scope) = &self.scope {
            scope.close()?;
            self.scope.take();
            self.child.take();
            Ok(())
        } else {
            Err(ProcessError::CleanupUnconfirmed)
        }
    }

    /// Validate and enqueue without waiting for an Agent response or a writable pipe.
    pub fn submit(
        &mut self,
        plaintext: &[u8],
        platform_session_id: &str,
        dispatch_id: [u8; 16],
    ) -> Result<(), ProcessError> {
        if plaintext.is_empty() || plaintext.len() > MAX_ACP_MESSAGE_BYTES {
            return Err(ProcessError::Limit);
        }
        if let Some(bound) = &self.platform_session_id {
            if bound != platform_session_id {
                return Err(ProcessError::Protocol);
            }
        } else {
            self.platform_session_id = Some(platform_session_id.to_owned());
        }
        let mut message: Value =
            serde_json::from_slice(plaintext).map_err(|_| ProcessError::Protocol)?;
        if !message.is_object() || message.get("jsonrpc").and_then(Value::as_str) != Some("2.0") {
            return Err(ProcessError::Protocol);
        }
        let method = message
            .get("method")
            .and_then(Value::as_str)
            .map(str::to_owned);
        let Some(method) = method else {
            return self.submit_permission(message, dispatch_id);
        };
        if message.get("result").is_some() || message.get("error").is_some() {
            return Err(ProcessError::Protocol);
        }
        if method == "session/cancel" {
            if message.get("id").is_some()
                || message
                    .get("params")
                    .and_then(Value::as_object)
                    .is_none_or(|params| params.len() != 1)
                || message.pointer("/params/sessionId").and_then(Value::as_str)
                    != Some(platform_session_id)
            {
                return Err(ProcessError::Protocol);
            }
            self.cancel_permissions()?;
            message["params"]["sessionId"] = Value::String(self.agent_session_id.clone());
            return self.queue(&message, Some(dispatch_id));
        }
        let id = message
            .get("id")
            .filter(|value| valid_id(value))
            .cloned()
            .ok_or(ProcessError::Protocol)?;
        if self
            .requests
            .values()
            .any(|request| request.original_id == id)
        {
            return Err(ProcessError::Protocol);
        }
        if self.requests.len() >= QUEUE_DEPTH || self.immediate.len() >= QUEUE_DEPTH {
            return Err(ProcessError::Limit);
        }
        if message.pointer("/params/sessionId").and_then(Value::as_str) != Some(platform_session_id)
        {
            return Err(ProcessError::Protocol);
        }
        if method == "_mss/session/describe" {
            let mut session = self.session_info.clone();
            session["sessionId"] = Value::String(platform_session_id.to_owned());
            return self.immediate_result(id, json!({
                "initialize": self.initialized, "session": session,
                "bridge": {"duplex": true, "turnCancellation": self.initialized.pointer("/_meta/mss/turnCancellation").and_then(Value::as_bool).unwrap_or(true), "protocolVersion": 1, "processEpoch": self.process_epoch.to_string()}
            }), dispatch_id);
        }
        if !matches!(
            method.as_str(),
            "session/prompt"
                | "session/set_config_option"
                | "session/set_mode"
                | "session/set_model"
        ) {
            return self.immediate_error(id, -32601, "UNSUPPORTED_SESSION_METHOD", dispatch_id);
        }
        if method == "session/prompt" {
            let parsed: PromptRequest = serde_json::from_value(message["params"].clone())
                .map_err(|_| ProcessError::Protocol)?;
            if parsed.prompt.is_empty() {
                return Err(ProcessError::Protocol);
            }
        }
        if !self.requests.is_empty() {
            return self.immediate_error(id, -32000, "TURN_IN_PROGRESS", dispatch_id);
        }
        if method != "session/prompt" && !self.configuration_allowed(&method, &message["params"]) {
            return self.immediate_error(id, -32602, "UNSUPPORTED_CONFIGURATION", dispatch_id);
        }
        let wire_id = self.allocate_id("request")?;
        message["id"] = Value::String(wire_id.clone());
        message["params"]["sessionId"] = Value::String(self.agent_session_id.clone());
        self.queue(&message, None)?;
        if method == "session/prompt" {
            self.active_turn = Some(wire_id.clone());
        }
        let timeout = if method == "session/prompt" {
            TURN_TIMEOUT
        } else {
            REQUEST_TIMEOUT
        };
        self.requests.insert(
            wire_id,
            RequestBinding {
                original_id: id,
                dispatch_id,
                requested_value: message
                    .pointer("/params/modeId")
                    .or_else(|| message.pointer("/params/modelId"))
                    .cloned(),
                method,
                deadline: Instant::now() + timeout,
            },
        );
        Ok(())
    }

    /// At most one event per call. Gateway drains a bounded fair batch across sessions.
    pub fn poll(&mut self) -> Result<Option<AgentEvent>, ProcessError> {
        if let Some(event) = self.immediate.pop_front() {
            return Ok(Some(event));
        }
        if self
            .requests
            .values()
            .any(|request| Instant::now() >= request.deadline)
        {
            return Err(ProcessError::Timeout);
        }
        let expired: Vec<String> = self
            .permissions
            .iter()
            .filter(|(_, permission)| Instant::now() >= permission.deadline)
            .map(|(id, _)| id.clone())
            .collect();
        for id in expired {
            self.cancel_permission(&id)?;
        }
        if let Some(event) = self.immediate.pop_front() {
            return Ok(Some(event));
        }
        let receiver = self.receiver.as_ref().ok_or(ProcessError::Transport)?;
        match receiver.try_recv() {
            Ok(TransportEvent::Message(bytes)) => self.process_message(bytes).map(Some),
            Ok(TransportEvent::Written(id)) => Ok(Some(AgentEvent {
                message: Vec::new(),
                completed_dispatch: Some(id),
            })),
            Ok(TransportEvent::Failure(error)) => Err(error),
            Err(TryRecvError::Empty) => Ok(None),
            Err(TryRecvError::Disconnected) => Err(ProcessError::Transport),
        }
    }

    pub fn pending_dispatches(&self) -> Vec<[u8; 16]> {
        self.requests
            .values()
            .map(|value| value.dispatch_id)
            .collect()
    }

    /// Compatibility helper for a local diagnostic only; never called by Gateway.
    pub fn prompt(
        &mut self,
        plaintext: &[u8],
        platform_session_id: &str,
    ) -> Result<Vec<Vec<u8>>, ProcessError> {
        let value: Value = serde_json::from_slice(plaintext).map_err(|_| ProcessError::Protocol)?;
        if value.get("method").and_then(Value::as_str) != Some("session/prompt") {
            return Err(ProcessError::Protocol);
        }
        let dispatch_id = [1; 16];
        self.submit(plaintext, platform_session_id, dispatch_id)?;
        let mut messages = Vec::new();
        loop {
            if let Some(event) = self.poll()? {
                if !event.message.is_empty() {
                    messages.push(event.message);
                }
                if event.completed_dispatch == Some(dispatch_id) {
                    return Ok(messages);
                }
                if messages.len() >= MAX_TURN_MESSAGES {
                    return Err(ProcessError::Limit);
                }
            } else {
                thread::sleep(Duration::from_millis(5));
            }
        }
    }

    fn process_message(&mut self, bytes: Vec<u8>) -> Result<AgentEvent, ProcessError> {
        let mut message: Value =
            serde_json::from_slice(&bytes).map_err(|_| ProcessError::Protocol)?;
        if !message.is_object() || message.get("jsonrpc").and_then(Value::as_str) != Some("2.0") {
            return Err(ProcessError::Protocol);
        }
        if let Some(method) = message
            .get("method")
            .and_then(Value::as_str)
            .map(str::to_owned)
        {
            if message.get("result").is_some() || message.get("error").is_some() {
                return Err(ProcessError::Protocol);
            }
            if message.pointer("/params/sessionId").and_then(Value::as_str)
                != Some(self.agent_session_id.as_str())
            {
                return Err(ProcessError::Protocol);
            }
            if method == "session/request_permission" {
                return self.open_permission(message);
            }
            if method != "session/update"
                || message.get("id").is_some()
                || !message
                    .pointer("/params/update")
                    .is_some_and(Value::is_object)
            {
                return Err(ProcessError::Protocol);
            }
            if message
                .pointer("/params/update/sessionUpdate")
                .and_then(Value::as_str)
                == Some("config_option_update")
            {
                if let Some(options) = message
                    .pointer("/params/update/configOptions")
                    .filter(|value| value.is_array())
                {
                    self.session_info["configOptions"] = options.clone();
                }
            }
            if message
                .pointer("/params/update/sessionUpdate")
                .and_then(Value::as_str)
                == Some("current_mode_update")
            {
                if let Some(mode) = message
                    .pointer("/params/update/currentModeId")
                    .filter(|value| value.is_string())
                {
                    self.session_info["modes"]["currentModeId"] = mode.clone();
                }
            }
            message["params"]["sessionId"] = Value::String(
                self.platform_session_id
                    .clone()
                    .ok_or(ProcessError::Protocol)?,
            );
            return Ok(AgentEvent {
                message: serialize_bounded(&message)?,
                completed_dispatch: None,
            });
        }
        if message.get("result").is_some() == message.get("error").is_some() {
            return Err(ProcessError::Protocol);
        }
        let wire_id = message
            .get("id")
            .and_then(Value::as_str)
            .ok_or(ProcessError::Protocol)?
            .to_owned();
        let request = self
            .requests
            .remove(&wire_id)
            .ok_or(ProcessError::Protocol)?;
        if request.method == "session/prompt" {
            if self.active_turn.as_deref() != Some(wire_id.as_str()) {
                return Err(ProcessError::Protocol);
            }
            self.active_turn = None;
            self.cancel_permissions()?;
        } else if request.method == "session/set_config_option" {
            if let Some(options) = message
                .pointer("/result/configOptions")
                .filter(|value| value.is_array())
            {
                self.session_info["configOptions"] = options.clone();
            }
        }
        if message.get("error").is_none() {
            if let Some(value) = request.requested_value {
                match request.method.as_str() {
                    "session/set_mode" => self.session_info["modes"]["currentModeId"] = value,
                    "session/set_model" => self.session_info["models"]["currentModelId"] = value,
                    _ => {}
                }
            }
        }
        message["id"] = request.original_id;
        Ok(AgentEvent {
            message: serialize_bounded(&message)?,
            completed_dispatch: Some(request.dispatch_id),
        })
    }

    fn open_permission(&mut self, mut message: Value) -> Result<AgentEvent, ProcessError> {
        if self.active_turn.is_none() || self.permissions.len() >= 16 {
            return Err(ProcessError::Protocol);
        }
        let original_id = message
            .get("id")
            .filter(|id| valid_id(id))
            .cloned()
            .ok_or(ProcessError::Protocol)?;
        if self
            .permissions
            .values()
            .any(|value| value.original_id == original_id)
        {
            return Err(ProcessError::Protocol);
        }
        let options = message
            .pointer("/params/options")
            .and_then(Value::as_array)
            .ok_or(ProcessError::Protocol)?;
        if options.is_empty()
            || options.len() > 16
            || !message
                .pointer("/params/toolCall")
                .is_some_and(Value::is_object)
        {
            return Err(ProcessError::Protocol);
        }
        let mut option_ids = BTreeSet::new();
        for option in options {
            let id = option
                .get("optionId")
                .and_then(Value::as_str)
                .ok_or(ProcessError::Protocol)?;
            if id.is_empty() || id.len() > 128 || !option_ids.insert(id.to_owned()) {
                return Err(ProcessError::Protocol);
            }
            if !matches!(
                option.get("kind").and_then(Value::as_str),
                Some("allow_once" | "allow_always" | "reject_once" | "reject_always")
            ) {
                return Err(ProcessError::Protocol);
            }
        }
        let id = self.allocate_id("permission")?;
        message["id"] = Value::String(id.clone());
        message["params"]["sessionId"] = Value::String(
            self.platform_session_id
                .clone()
                .ok_or(ProcessError::Protocol)?,
        );
        self.permissions.insert(
            id,
            PermissionBinding {
                original_id,
                option_ids,
                deadline: Instant::now() + APPROVAL_TIMEOUT,
            },
        );
        Ok(AgentEvent {
            message: serialize_bounded(&message)?,
            completed_dispatch: None,
        })
    }

    fn submit_permission(
        &mut self,
        mut message: Value,
        dispatch_id: [u8; 16],
    ) -> Result<(), ProcessError> {
        let id = message
            .get("id")
            .and_then(Value::as_str)
            .ok_or(ProcessError::Protocol)?
            .to_owned();
        if !message.as_object().is_some_and(|object| {
            object.len() == 3
                && object.contains_key("jsonrpc")
                && object.contains_key("id")
                && object.contains_key("result")
        }) || !message
            .get("result")
            .and_then(Value::as_object)
            .is_some_and(|object| object.len() == 1 && object.contains_key("outcome"))
        {
            return Err(ProcessError::Protocol);
        }
        let permission = self.permissions.get(&id).ok_or(ProcessError::Protocol)?;
        if Instant::now() >= permission.deadline {
            return Err(ProcessError::Protocol);
        }
        match message
            .pointer("/result/outcome/outcome")
            .and_then(Value::as_str)
        {
            Some("selected") => {
                if message
                    .pointer("/result/outcome")
                    .and_then(Value::as_object)
                    .is_none_or(|object| object.len() != 2)
                {
                    return Err(ProcessError::Protocol);
                }
                let option = message
                    .pointer("/result/outcome/optionId")
                    .and_then(Value::as_str)
                    .ok_or(ProcessError::Protocol)?;
                if !permission.option_ids.contains(option) {
                    return Err(ProcessError::Protocol);
                }
            }
            Some("cancelled") => {
                if message
                    .pointer("/result/outcome")
                    .and_then(Value::as_object)
                    .is_none_or(|object| object.len() != 1)
                {
                    return Err(ProcessError::Protocol);
                }
            }
            _ => return Err(ProcessError::Protocol),
        }
        message["id"] = permission.original_id.clone();
        self.queue(&message, Some(dispatch_id))?;
        self.permissions.remove(&id);
        Ok(())
    }

    fn cancel_permissions(&mut self) -> Result<(), ProcessError> {
        let ids: Vec<String> = self.permissions.keys().cloned().collect();
        for id in ids {
            self.cancel_permission(&id)?;
        }
        Ok(())
    }

    fn cancel_permission(&mut self, id: &str) -> Result<(), ProcessError> {
        let permission = self.permissions.get(id).ok_or(ProcessError::Protocol)?;
        self.queue(&json!({"jsonrpc": "2.0", "id": permission.original_id, "result": {"outcome": {"outcome": "cancelled"}}}), None)?;
        self.permissions.remove(id);
        self.immediate.push_back(AgentEvent {
            message: serialize_bounded(&json!({"jsonrpc": "2.0", "method": "_mss/permission/closed", "params": {"requestId": id, "sessionId": self.platform_session_id}}))?,
            completed_dispatch: None,
        });
        Ok(())
    }

    fn configuration_allowed(&self, method: &str, params: &Value) -> bool {
        let keys: &[&str] = match method {
            "session/set_config_option" => &["sessionId", "configId", "value"],
            "session/set_mode" => &["sessionId", "modeId"],
            "session/set_model" => &["sessionId", "modelId"],
            _ => return false,
        };
        if !params.as_object().is_some_and(|object| {
            object.len() == keys.len() && keys.iter().all(|key| object.contains_key(*key))
        }) {
            return false;
        }
        match method {
            "session/set_config_option" => {
                let Some(id) = params.get("configId").and_then(Value::as_str) else {
                    return false;
                };
                let Some(value) = params.get("value").and_then(Value::as_str) else {
                    return false;
                };
                self.session_info
                    .get("configOptions")
                    .and_then(Value::as_array)
                    .is_some_and(|options| {
                        options.iter().any(|option| {
                            option.get("id").and_then(Value::as_str) == Some(id)
                                && option.get("options").and_then(Value::as_array).is_some_and(
                                    |values| {
                                        values.iter().any(|item| {
                                            item.get("value").and_then(Value::as_str) == Some(value)
                                                || item
                                                    .get("options")
                                                    .and_then(Value::as_array)
                                                    .is_some_and(|group| {
                                                        group.iter().any(|item| {
                                                            item.get("value")
                                                                .and_then(Value::as_str)
                                                                == Some(value)
                                                        })
                                                    })
                                        })
                                    },
                                )
                        })
                    })
            }
            "session/set_mode" => self
                .session_info
                .pointer("/modes/availableModes")
                .and_then(Value::as_array)
                .is_some_and(|modes| {
                    modes
                        .iter()
                        .any(|mode| mode.get("id") == params.get("modeId"))
                }),
            "session/set_model" => self
                .session_info
                .pointer("/models/availableModels")
                .and_then(Value::as_array)
                .is_some_and(|models| {
                    models
                        .iter()
                        .any(|model| model.get("modelId") == params.get("modelId"))
                }),
            _ => false,
        }
    }

    fn immediate_result(
        &mut self,
        id: Value,
        result: Value,
        dispatch_id: [u8; 16],
    ) -> Result<(), ProcessError> {
        self.immediate.push_back(AgentEvent {
            message: serialize_bounded(&json!({"jsonrpc": "2.0", "id": id, "result": result}))?,
            completed_dispatch: Some(dispatch_id),
        });
        Ok(())
    }
    fn immediate_error(
        &mut self,
        id: Value,
        code: i32,
        message: &str,
        dispatch_id: [u8; 16],
    ) -> Result<(), ProcessError> {
        self.immediate.push_back(AgentEvent {
            message: serialize_bounded(
                &json!({"jsonrpc": "2.0", "id": id, "error": {"code": code, "message": message}}),
            )?,
            completed_dispatch: Some(dispatch_id),
        });
        Ok(())
    }
    fn allocate_id(&mut self, kind: &str) -> Result<String, ProcessError> {
        self.next_id = self.next_id.checked_add(1).ok_or(ProcessError::Limit)?;
        Ok(format!(
            "aba/{}/{kind}/{}",
            self.process_epoch, self.next_id
        ))
    }

    fn initialize(&mut self, workspace_path: &std::path::Path) -> Result<(), ProcessError> {
        let initialize_id = Value::String("aba-initialize".to_owned());
        self.queue(&json!({"jsonrpc": "2.0", "id": initialize_id, "method": "initialize", "params": InitializeRequest::new(ProtocolVersion::V1)}), None)?;
        let deadline = Instant::now() + START_TIMEOUT;
        let response = self.receive(deadline)?;
        let result = response_result(&response, &initialize_id)?;
        let initialized: InitializeResponse =
            serde_json::from_value(result.clone()).map_err(|_| ProcessError::Protocol)?;
        if initialized.protocol_version != ProtocolVersion::V1 {
            return Err(ProcessError::Protocol);
        }
        self.initialized = result;
        let session_id = Value::String("aba-session-new".to_owned());
        self.queue(&json!({"jsonrpc": "2.0", "id": session_id, "method": "session/new", "params": NewSessionRequest::new(workspace_path)}), None)?;
        let response = self.receive(Instant::now() + START_TIMEOUT)?;
        let result = response_result(&response, &session_id)?;
        let created: NewSessionResponse =
            serde_json::from_value(result.clone()).map_err(|_| ProcessError::Protocol)?;
        if created.session_id.0.is_empty() || created.session_id.0.len() > 256 {
            return Err(ProcessError::Protocol);
        }
        self.agent_session_id = created.session_id.0.to_string();
        self.session_info = result;
        Ok(())
    }

    fn queue(&self, value: &Value, receipt: Option<[u8; 16]>) -> Result<(), ProcessError> {
        self.writer
            .as_ref()
            .ok_or(ProcessError::Transport)?
            .try_send(WriteCommand {
                bytes: serialize_bounded(value)?,
                receipt,
            })
            .map_err(|error| match error {
                std::sync::mpsc::TrySendError::Full(_) => ProcessError::Limit,
                std::sync::mpsc::TrySendError::Disconnected(_) => ProcessError::Transport,
            })
    }
    fn receive(&self, deadline: Instant) -> Result<Value, ProcessError> {
        let remaining = deadline
            .checked_duration_since(Instant::now())
            .ok_or(ProcessError::Timeout)?;
        match self
            .receiver
            .as_ref()
            .ok_or(ProcessError::Transport)?
            .recv_timeout(remaining)
        {
            Ok(TransportEvent::Message(bytes)) => {
                serde_json::from_slice(&bytes).map_err(|_| ProcessError::Protocol)
            }
            Ok(TransportEvent::Failure(error)) => Err(error),
            Err(RecvTimeoutError::Timeout) => Err(ProcessError::Timeout),
            _ => Err(ProcessError::Transport),
        }
    }
}

impl Drop for AgentProcess {
    fn drop(&mut self) {
        if self.scope.is_some() {
            // Error paths also clean up off the network loop; durable scope claims survive Drop.
            self.receiver.take();
            self.writer.take();
            let mut child = self.child.take();
            let scope = self.scope.take();
            let _ = thread::Builder::new()
                .name("aba-abandoned-scope".into())
                .spawn(move || {
                    if let Some(child) = &mut child {
                        terminate_child(child);
                    }
                    if let Some(scope) = scope {
                        let _ = scope.close();
                    }
                });
        } else {
            let _ = self.shutdown();
        }
        for thread in self.threads.drain(..) {
            // A hostile escaped descendant must not block the Gateway in an unbounded join.
            if thread.is_finished() {
                let _ = thread.join();
            }
        }
    }
}

fn valid_id(value: &Value) -> bool {
    match value {
        Value::String(text) => !text.is_empty() && text.len() <= 256,
        Value::Number(number) => number
            .as_i64()
            .is_some_and(|value| (-9_007_199_254_740_991..=9_007_199_254_740_991).contains(&value)),
        _ => false,
    }
}
fn response_result(response: &Value, expected_id: &Value) -> Result<Value, ProcessError> {
    if response.get("jsonrpc").and_then(Value::as_str) != Some("2.0")
        || response.get("id") != Some(expected_id)
        || response.get("method").is_some()
        || response.get("error").is_some()
    {
        return Err(ProcessError::Protocol);
    }
    response
        .get("result")
        .cloned()
        .ok_or(ProcessError::Protocol)
}
fn serialize_bounded(value: &Value) -> Result<Vec<u8>, ProcessError> {
    let encoded = serde_json::to_vec(value).map_err(|_| ProcessError::Protocol)?;
    if encoded.is_empty() || encoded.len() > MAX_ACP_MESSAGE_BYTES {
        return Err(ProcessError::Limit);
    }
    Ok(encoded)
}

fn canonical_safe_file(path: &std::path::Path) -> Result<std::path::PathBuf, ProcessError> {
    let metadata = fs::symlink_metadata(path).map_err(|_| ProcessError::UnsafeProfile)?;
    if !path.is_absolute() || !metadata.is_file() || metadata.file_type().is_symlink() {
        return Err(ProcessError::UnsafeProfile);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt as _;
        if metadata.permissions().mode() & 0o111 == 0 {
            return Err(ProcessError::UnsafeProfile);
        }
    }
    let canonical = fs::canonicalize(path).map_err(|_| ProcessError::UnsafeProfile)?;
    if canonical != path {
        return Err(ProcessError::UnsafeProfile);
    }
    Ok(canonical)
}

fn canonical_safe_directory(path: &std::path::Path) -> Result<std::path::PathBuf, ProcessError> {
    let metadata = fs::symlink_metadata(path).map_err(|_| ProcessError::UnsafeProfile)?;
    if !path.is_absolute() || !metadata.is_dir() || metadata.file_type().is_symlink() {
        return Err(ProcessError::UnsafeProfile);
    }
    let canonical = fs::canonicalize(path).map_err(|_| ProcessError::UnsafeProfile)?;
    if canonical != path {
        return Err(ProcessError::UnsafeProfile);
    }
    Ok(canonical)
}

#[cfg(unix)]
fn terminate_child(child: &mut Child) {
    use rustix::process::{Pid, Signal, kill_process_group};

    if !child.try_wait().is_ok_and(|status| status.is_some()) {
        let pid = Pid::from_child(child);
        let _ = kill_process_group(pid, Signal::TERM);
        let deadline = Instant::now() + Duration::from_millis(500);
        while Instant::now() < deadline {
            if child.try_wait().is_ok_and(|status| status.is_some()) {
                break;
            }
            thread::sleep(Duration::from_millis(10));
        }
        // Do not send a group signal after reaping the leader: the numeric ID can be reused.
        // The supervised path kills the verified cgroup below, irrespective of leader state.
        if !child.try_wait().is_ok_and(|status| status.is_some()) {
            let _ = kill_process_group(pid, Signal::KILL);
        }
    }
    let deadline = Instant::now() + Duration::from_secs(2);
    while Instant::now() < deadline {
        if child.try_wait().is_ok_and(|status| status.is_some()) {
            break;
        }
        thread::sleep(Duration::from_millis(10));
    }
}

#[cfg(not(unix))]
fn terminate_child(child: &mut Child) {
    if !child.try_wait().is_ok_and(|status| status.is_some()) {
        let _ = child.kill();
    }
    let deadline = Instant::now() + Duration::from_secs(2);
    while Instant::now() < deadline {
        if child.try_wait().is_ok_and(|status| status.is_some()) {
            break;
        }
        thread::sleep(Duration::from_millis(10));
    }
}
