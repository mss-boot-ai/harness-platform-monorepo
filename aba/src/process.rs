use std::fs;
use std::io::{BufRead, BufReader, Write as _};
use std::process::{Child, ChildStdin, Command, Stdio};
use std::sync::mpsc::{self, Receiver, RecvTimeoutError, SyncSender};
use std::thread::{self, JoinHandle};
use std::time::{Duration, Instant};

use agent_client_protocol::schema::ProtocolVersion;
use agent_client_protocol::schema::v1::{
    InitializeRequest, InitializeResponse, NewSessionRequest, NewSessionResponse, PromptRequest,
    PromptResponse, SessionNotification,
};
use serde_json::Value;
use thiserror::Error;

use crate::config::{RuntimeProfile, WorkspaceProfile};

const MAX_ACP_MESSAGE_BYTES: usize = 64 * 1024;
const MAX_READER_LINE_BYTES: usize = 1024 * 1024;
const MAX_TURN_MESSAGES: usize = 256;
const START_TIMEOUT: Duration = Duration::from_secs(10);
const PROMPT_TIMEOUT: Duration = Duration::from_secs(60);
const READER_QUEUE_DEPTH: usize = 64;

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
}

#[derive(Debug, Clone, Copy)]
enum ReaderError {
    Transport,
    Limit,
}

pub struct AgentProcess {
    child: Child,
    stdin: ChildStdin,
    receiver: Option<Receiver<Result<Vec<u8>, ReaderError>>>,
    reader: Option<JoinHandle<()>>,
    agent_session_id: String,
}

impl AgentProcess {
    pub fn start(
        runtime: &RuntimeProfile,
        workspace: &WorkspaceProfile,
    ) -> Result<Self, ProcessError> {
        let command_path = canonical_safe_file(&runtime.command)?;
        let workspace_path = canonical_safe_directory(&workspace.path)?;
        let mut command = Command::new(command_path);
        command
            .args(&runtime.args)
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
        let (sender, receiver) = mpsc::sync_channel(READER_QUEUE_DEPTH);
        let reader = match thread::Builder::new()
            .name("aba-agent-stdout".to_owned())
            .spawn(move || read_agent_stdout(stdout, &sender))
        {
            Ok(reader) => reader,
            Err(_) => {
                terminate_child(&mut child);
                return Err(ProcessError::Spawn);
            }
        };
        let mut process = Self {
            child,
            stdin,
            receiver: Some(receiver),
            reader: Some(reader),
            agent_session_id: String::new(),
        };
        process.initialize(&workspace_path)?;
        Ok(process)
    }

    pub fn prompt(
        &mut self,
        plaintext: &[u8],
        platform_session_id: &str,
    ) -> Result<Vec<Vec<u8>>, ProcessError> {
        if plaintext.is_empty() || plaintext.len() > MAX_ACP_MESSAGE_BYTES {
            return Err(ProcessError::Limit);
        }
        let mut request: Value =
            serde_json::from_slice(plaintext).map_err(|_| ProcessError::Protocol)?;
        if request.get("jsonrpc").and_then(Value::as_str) != Some("2.0")
            || request.get("method").and_then(Value::as_str) != Some("session/prompt")
        {
            return Err(ProcessError::Protocol);
        }
        let request_id = request
            .get("id")
            .filter(|value| !value.is_null())
            .cloned()
            .ok_or(ProcessError::Protocol)?;
        let prompt: PromptRequest = serde_json::from_value(
            request
                .get("params")
                .cloned()
                .ok_or(ProcessError::Protocol)?,
        )
        .map_err(|_| ProcessError::Protocol)?;
        if prompt.session_id.0.as_ref() != platform_session_id || prompt.prompt.is_empty() {
            return Err(ProcessError::Protocol);
        }
        request
            .get_mut("params")
            .and_then(Value::as_object_mut)
            .ok_or(ProcessError::Protocol)?
            .insert(
                "sessionId".to_owned(),
                Value::String(self.agent_session_id.clone()),
            );
        self.send(&request)?;

        let deadline = Instant::now() + PROMPT_TIMEOUT;
        let mut messages = Vec::new();
        for _ in 0..MAX_TURN_MESSAGES {
            let mut message = self.receive(deadline)?;
            if message.get("jsonrpc").and_then(Value::as_str) != Some("2.0") {
                return Err(ProcessError::Protocol);
            }
            if message.get("method").and_then(Value::as_str) == Some("session/update") {
                if message.get("id").is_some() || message.get("result").is_some() {
                    return Err(ProcessError::Protocol);
                }
                let notification: SessionNotification = serde_json::from_value(
                    message
                        .get("params")
                        .cloned()
                        .ok_or(ProcessError::Protocol)?,
                )
                .map_err(|_| ProcessError::Protocol)?;
                if notification.session_id.0.as_ref() != self.agent_session_id {
                    return Err(ProcessError::Protocol);
                }
                message
                    .get_mut("params")
                    .and_then(Value::as_object_mut)
                    .ok_or(ProcessError::Protocol)?
                    .insert(
                        "sessionId".to_owned(),
                        Value::String(platform_session_id.to_owned()),
                    );
                messages.push(serialize_bounded(&message)?);
                continue;
            }
            if message.get("method").is_some()
                || message.get("id") != Some(&request_id)
                || message.get("result").is_none()
            {
                return Err(ProcessError::Protocol);
            }
            serde_json::from_value::<PromptResponse>(
                message
                    .get("result")
                    .cloned()
                    .ok_or(ProcessError::Protocol)?,
            )
            .map_err(|_| ProcessError::Protocol)?;
            messages.push(serialize_bounded(&message)?);
            return Ok(messages);
        }
        Err(ProcessError::Limit)
    }

    fn initialize(&mut self, workspace_path: &std::path::Path) -> Result<(), ProcessError> {
        let initialize_id = Value::String("aba-initialize".to_owned());
        self.send(&serde_json::json!({
            "jsonrpc": "2.0",
            "id": initialize_id,
            "method": "initialize",
            "params": InitializeRequest::new(ProtocolVersion::V1),
        }))?;
        let deadline = Instant::now() + START_TIMEOUT;
        let response = self.receive(deadline)?;
        let result = response_result(&response, &initialize_id)?;
        let initialized: InitializeResponse =
            serde_json::from_value(result).map_err(|_| ProcessError::Protocol)?;
        if initialized.protocol_version != ProtocolVersion::V1 {
            return Err(ProcessError::Protocol);
        }

        let session_id = Value::String("aba-session-new".to_owned());
        self.send(&serde_json::json!({
            "jsonrpc": "2.0",
            "id": session_id,
            "method": "session/new",
            "params": NewSessionRequest::new(workspace_path),
        }))?;
        let response = self.receive(deadline)?;
        let result = response_result(&response, &session_id)?;
        let created: NewSessionResponse =
            serde_json::from_value(result).map_err(|_| ProcessError::Protocol)?;
        if created.session_id.0.is_empty() || created.session_id.0.len() > 256 {
            return Err(ProcessError::Protocol);
        }
        self.agent_session_id = created.session_id.0.to_string();
        Ok(())
    }

    fn send(&mut self, value: &Value) -> Result<(), ProcessError> {
        let encoded = serialize_bounded(value)?;
        self.stdin
            .write_all(&encoded)
            .and_then(|()| self.stdin.write_all(b"\n"))
            .and_then(|()| self.stdin.flush())
            .map_err(|_| ProcessError::Transport)
    }

    fn receive(&self, deadline: Instant) -> Result<Value, ProcessError> {
        let remaining = deadline
            .checked_duration_since(Instant::now())
            .ok_or(ProcessError::Timeout)?;
        let receiver = self.receiver.as_ref().ok_or(ProcessError::Transport)?;
        let bytes = match receiver.recv_timeout(remaining) {
            Ok(Ok(bytes)) => bytes,
            Ok(Err(ReaderError::Transport)) | Err(RecvTimeoutError::Disconnected) => {
                return Err(ProcessError::Transport);
            }
            Ok(Err(ReaderError::Limit)) => return Err(ProcessError::Limit),
            Err(RecvTimeoutError::Timeout) => return Err(ProcessError::Timeout),
        };
        if bytes.is_empty() {
            return Err(ProcessError::Protocol);
        }
        if bytes.len() > MAX_ACP_MESSAGE_BYTES {
            return Err(ProcessError::Limit);
        }
        serde_json::from_slice(&bytes).map_err(|_| ProcessError::Protocol)
    }
}

impl Drop for AgentProcess {
    fn drop(&mut self) {
        self.receiver.take();
        terminate_child(&mut self.child);
        if let Some(reader) = self.reader.take() {
            let _ = reader.join();
        }
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
    if encoded.len() > MAX_ACP_MESSAGE_BYTES {
        return Err(ProcessError::Limit);
    }
    Ok(encoded)
}

fn read_agent_stdout(
    stdout: std::process::ChildStdout,
    sender: &SyncSender<Result<Vec<u8>, ReaderError>>,
) {
    let mut reader = BufReader::new(stdout);
    loop {
        match read_line_bounded(&mut reader) {
            Ok(Some(line)) => {
                if sender.send(Ok(line)).is_err() {
                    return;
                }
            }
            Ok(None) => {
                let _ = sender.send(Err(ReaderError::Transport));
                return;
            }
            Err(error) => {
                let _ = sender.send(Err(error));
                return;
            }
        }
    }
}

fn read_line_bounded(reader: &mut impl BufRead) -> Result<Option<Vec<u8>>, ReaderError> {
    let mut line = Vec::new();
    loop {
        let available = reader.fill_buf().map_err(|_| ReaderError::Transport)?;
        if available.is_empty() {
            return if line.is_empty() {
                Ok(None)
            } else {
                Err(ReaderError::Transport)
            };
        }
        let newline = available.iter().position(|byte| *byte == b'\n');
        let consumed = newline.map_or(available.len(), |position| position + 1);
        let content_length = newline.unwrap_or(available.len());
        if line.len().saturating_add(content_length) > MAX_READER_LINE_BYTES {
            return Err(ReaderError::Limit);
        }
        line.extend_from_slice(&available[..content_length]);
        reader.consume(consumed);
        if newline.is_some() {
            if line.last() == Some(&b'\r') {
                line.pop();
            }
            return Ok(Some(line));
        }
    }
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
                return;
            }
            thread::sleep(Duration::from_millis(10));
        }
        let _ = kill_process_group(pid, Signal::KILL);
    }
    let _ = child.wait();
}

#[cfg(not(unix))]
fn terminate_child(child: &mut Child) {
    if !child.try_wait().is_ok_and(|status| status.is_some()) {
        let _ = child.kill();
    }
    let _ = child.wait();
}
