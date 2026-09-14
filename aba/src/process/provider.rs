//! A purpose-specific provider data path, not a general HTTP/SOCKS or host-control proxy.
//! Runs inside the Run's cgroup but outside its network/PID namespace; no root privileges.
use std::collections::BTreeSet;
use std::io::{BufReader, Read, Write};
use std::net::{Shutdown, TcpListener, TcpStream};
use std::os::fd::AsRawFd as _;
use std::os::unix::fs::OpenOptionsExt as _;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::Path;
use std::process::Command;
use std::sync::{
    Arc, Mutex,
    atomic::{AtomicUsize, Ordering},
};
use std::thread;
use std::time::{Duration, Instant};

use reqwest::Client;
use sha2::{Digest as _, Sha256};
use url::Url;

use super::ProcessError;

const MAX_HEADERS: usize = 16 * 1024;
const MAX_BODY: usize = 4 * 1024 * 1024;
const MAX_RESPONSE: u64 = 128 * 1024 * 1024;
const TIMEOUT: Duration = Duration::from_secs(120);
const MAX_CONNECTIONS: usize = 4;
const MAX_REQUESTS: usize = 256;
const LOCAL_PROVIDER: &str = "http://127.0.0.1:39121/v1";

fn failed<T>(_: T) -> ProcessError {
    ProcessError::Transport
}

struct Permit(Arc<AtomicUsize>);
impl Permit {
    fn acquire(count: &Arc<AtomicUsize>) -> Option<Self> {
        count
            .fetch_update(Ordering::AcqRel, Ordering::Acquire, |value| {
                (value < MAX_CONNECTIONS).then_some(value + 1)
            })
            .ok()?;
        Some(Self(Arc::clone(count)))
    }
}
impl Drop for Permit {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::AcqRel);
    }
}

pub(super) fn start_host_proxy(socket: &Path) -> Result<(), ProcessError> {
    let base = std::env::var("HARNESS_CODEX_API_BASE_URL").map_err(failed)?;
    let key = std::env::var("HARNESS_CODEX_API_KEY").map_err(failed)?;
    let base = provider_base(&base)?;
    let model = std::env::var("HARNESS_CODEX_MODEL").map_err(failed)?;
    let models = std::env::var("HARNESS_CODEX_MODELS").unwrap_or(model.clone());
    let mut policy = ProviderPolicy::new(&model, &models)?;
    policy.audit = Some(Mutex::new(
        std::fs::OpenOptions::new()
            .create_new(true)
            .append(true)
            .mode(0o600)
            .open(socket.with_extension("audit.jsonl"))
            .map_err(failed)?,
    ));
    let policy = Arc::new(policy);
    if key.is_empty() || key.len() > 4096 || key.bytes().any(|byte| byte < 0x20 || byte == 0x7f) {
        return Err(ProcessError::UnsafeProfile);
    }
    let listener = UnixListener::bind(socket).map_err(failed)?; // Never removes/replaces an old socket.
    let count = Arc::new(AtomicUsize::new(0));
    thread::Builder::new()
        .name("aba-provider-listener".into())
        .spawn(move || {
            for incoming in listener.incoming() {
                let Ok(mut stream) = incoming else {
                    break;
                };
                let Some(permit) = Permit::acquire(&count) else {
                    continue;
                };
                let policy = Arc::clone(&policy);
                let base = base.clone();
                let key = key.clone();
                let _ = thread::Builder::new()
                    .name("aba-provider-request".into())
                    .spawn(move || {
                        let _permit = permit;
                        let _ = stream.set_read_timeout(Some(TIMEOUT));
                        let _ = stream.set_write_timeout(Some(TIMEOUT));
                        // Only constant diagnostics go on the wire; no local credentials/error chains in logs.
                        if proxy_request(&mut stream, &base, &key, &policy).is_err() {
                            let _ = stream.shutdown(Shutdown::Both);
                        }
                    });
            }
        })
        .map_err(failed)?;
    Ok(())
}

fn provider_base(value: &str) -> Result<String, ProcessError> {
    let url = Url::parse(value).map_err(failed)?;
    if url.scheme() != "https"
        || !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || url.host_str().is_none()
    {
        return Err(ProcessError::UnsafeProfile);
    }
    // Only the namespace-local relay uses HTTP. The fixed upstream always authenticates TLS.
    Ok(value.trim_end_matches('/').to_owned())
}

struct Request {
    method: &'static str,
    suffix: &'static str,
    body: Vec<u8>,
}

struct ProviderPolicy {
    models: BTreeSet<String>,
    requests: AtomicUsize,
    audit: Option<Mutex<std::fs::File>>,
    audit_count: AtomicUsize,
}
impl ProviderPolicy {
    fn new(model: &str, models: &str) -> Result<Self, ProcessError> {
        let models: BTreeSet<String> = models
            .split(',')
            .map(|value| value.trim().to_owned())
            .collect();
        if !models.contains(model)
            || models.is_empty()
            || models.len() > 16
            || models
                .iter()
                .any(|value| value.is_empty() || value.len() > 128)
        {
            return Err(ProcessError::UnsafeProfile);
        }
        Ok(Self {
            models,
            requests: AtomicUsize::new(0),
            audit: None,
            audit_count: AtomicUsize::new(0),
        })
    }
    fn audit(&self, code: &'static str, shape: Option<&serde_json::Value>) {
        let Some(file) = &self.audit else {
            return;
        };
        if self
            .audit_count
            .fetch_update(Ordering::AcqRel, Ordering::Acquire, |count| {
                (count < 512).then_some(count + 1)
            })
            .is_err()
        {
            return;
        }
        // Only fixed control-field presence and closed tool-kind enums; never body text,
        // model values, definitions, arguments, upstream URLs, credentials or error chains.
        let known: Vec<_> = [
            "context_management",
            "prompt",
            "cache_control",
            "user",
            "conversation",
            "previous_response_id",
            "background",
            "max_tokens",
            "max_completion_tokens",
            "response_format",
            "generate",
            "type",
            "model",
            "input",
            "instructions",
            "tools",
            "tool_choice",
            "parallel_tool_calls",
            "reasoning",
            "text",
            "stream",
            "stream_options",
            "store",
            "include",
            "max_output_tokens",
            "metadata",
            "service_tier",
            "prompt_cache_key",
            "prompt_cache_retention",
            "safety_identifier",
            "truncation",
        ]
        .into_iter()
        .filter(|name| shape.is_some_and(|value| value.get(name).is_some()))
        .collect();
        let mut tool_kinds = BTreeSet::new();
        fn kinds(value: &serde_json::Value, output: &mut BTreeSet<&'static str>) {
            if let Some(items) = value.as_array() {
                for item in items.iter().take(64) {
                    output.insert(match item.get("type").and_then(serde_json::Value::as_str) {
                        Some("function") => "function",
                        Some("custom") => "custom",
                        Some("namespace") => "namespace",
                        Some("tool_search") => "tool_search",
                        _ => "other",
                    });
                }
            }
        }
        if let Some(tools) = shape.and_then(|value| value.get("tools")) {
            kinds(tools, &mut tool_kinds);
        }
        let null_controls: Vec<_> = known
            .iter()
            .filter(|name| {
                shape
                    .and_then(|value| value.get(**name))
                    .is_some_and(serde_json::Value::is_null)
            })
            .collect();
        let model_allowed = shape
            .and_then(|value| value.get("model"))
            .and_then(serde_json::Value::as_str)
            .is_some_and(|model| self.models.contains(model));
        let excessive_output = shape
            .and_then(|value| value.get("max_output_tokens"))
            .and_then(serde_json::Value::as_u64)
            .is_some_and(|value| value > 16_384);
        let store_disabled = shape
            .and_then(|value| value.get("store"))
            .is_none_or(|value| value == &serde_json::Value::Bool(false));
        let include_allowed = shape
            .and_then(|value| value.get("include"))
            .is_none_or(|value| {
                value.as_array().is_some_and(|items| {
                    items.len() <= 1
                        && items
                            .iter()
                            .all(|value| value.as_str() == Some("reasoning.encrypted_content"))
                })
            });
        let generation_disabled =
            shape.and_then(|value| value.get("generate")) == Some(&serde_json::Value::Bool(false));
        let input_allowed = shape
            .and_then(|value| value.get("input"))
            .is_none_or(|value| safe_input(value, 0));
        let choice_allowed =
            shape
                .and_then(|value| value.get("tool_choice"))
                .is_none_or(|choice| {
                    matches!(choice.as_str(), Some("auto" | "none" | "required"))
                        || matches!(
                            choice.get("type").and_then(serde_json::Value::as_str),
                            Some("function" | "custom")
                        )
                });
        let unknown_control_count = shape
            .and_then(serde_json::Value::as_object)
            .map_or(0, |object| {
                object.keys().filter(|key| !allowed_control(key)).count()
            });
        let unknown_control_fingerprints: Vec<_> = shape
            .and_then(serde_json::Value::as_object)
            .into_iter()
            .flat_map(|object| object.keys())
            .filter(|key| !allowed_control(key))
            .take(4)
            .map(|key| {
                serde_json::json!({"length":key.len(),"sha256":Sha256::digest(key.as_bytes()).iter()
                .map(|byte| format!("{byte:02x}")).collect::<String>()})
            })
            .collect();
        if let Ok(bytes) = serde_json::to_vec(
            &serde_json::json!({"code":code,"known_controls_present":known,"null_controls":null_controls,
                "tool_kinds":tool_kinds,"model_allowed":model_allowed,"excessive_output":excessive_output,
                "store_disabled":store_disabled,"include_allowed":include_allowed,"generation_disabled":generation_disabled,
                "input_allowed":input_allowed,"choice_allowed":choice_allowed,"unknown_control_count":unknown_control_count,
                "unknown_control_fingerprints":unknown_control_fingerprints}),
        ) && let Ok(mut file) = file.lock()
        {
            let _ = file.write_all(&bytes).and_then(|()| file.write_all(b"\n"));
        }
    }
    fn authorize(&self, request: &mut Request) -> Result<(), ProcessError> {
        if request.method == "POST" {
            let mut value: serde_json::Value =
                serde_json::from_slice(&request.body).map_err(failed)?;
            let object = value.as_object_mut().ok_or(ProcessError::Protocol)?;
            if object.keys().any(|key| !allowed_control(key))
                || object
                    .get("model")
                    .and_then(serde_json::Value::as_str)
                    .is_none_or(|model| !self.models.contains(model))
                || object
                    .get("store")
                    .is_some_and(|value| value != &serde_json::Value::Bool(false))
                || object
                    .get("service_tier")
                    .is_some_and(|value| !matches!(value.as_str(), Some("auto" | "default")))
                || object
                    .get("input")
                    .is_some_and(|input| !safe_input(input, 0))
            {
                return Err(ProcessError::Protocol);
            }
            if let Some(tools) = object.get("tools") {
                validate_tools(tools, 0)?;
            }
            if let Some(choice) = object.get("tool_choice") {
                if !matches!(choice.as_str(), Some("auto" | "none" | "required"))
                    && !matches!(
                        choice.get("type").and_then(serde_json::Value::as_str),
                        Some("function" | "custom")
                    )
                {
                    return Err(ProcessError::Protocol);
                }
            }
            if let Some(include) = object.get("include") {
                if include.as_array().is_none_or(|values| {
                    values.len() > 1
                        || values
                            .iter()
                            .any(|value| value.as_str() != Some("reasoning.encrypted_content"))
                }) {
                    return Err(ProcessError::Protocol);
                }
            }
            if object.get("max_output_tokens").is_some_and(|value| {
                value
                    .as_u64()
                    .is_none_or(|value| value == 0 || value > 16_384)
            }) {
                return Err(ProcessError::Limit);
            }
            object.insert("store".into(), false.into());
            object.entry("max_output_tokens").or_insert(16_384.into());
            request.body = serde_json::to_vec(&value).map_err(failed)?;
        }
        self.requests
            .fetch_update(Ordering::AcqRel, Ordering::Acquire, |value| {
                (value < MAX_REQUESTS).then_some(value + 1)
            })
            .map_err(|_| ProcessError::Limit)?;
        Ok(())
    }
}
fn allowed_control(key: &str) -> bool {
    matches!(
        key,
        "model"
            | "input"
            | "instructions"
            | "tools"
            | "tool_choice"
            | "parallel_tool_calls"
            | "reasoning"
            | "text"
            | "stream"
            | "stream_options"
            | "store"
            | "include"
            | "temperature"
            | "top_p"
            | "max_output_tokens"
            | "metadata"
            | "service_tier"
            | "prompt_cache_key"
            | "prompt_cache_retention"
            | "safety_identifier"
            | "truncation"
    )
}
fn validate_tools(tools: &serde_json::Value, depth: usize) -> Result<(), ProcessError> {
    let tools = tools.as_array().ok_or(ProcessError::Protocol)?;
    if tools.len() > 64 || depth > 2 {
        return Err(ProcessError::Limit);
    }
    for tool in tools {
        match tool.get("type").and_then(serde_json::Value::as_str) {
            Some("function" | "custom") => {}
            Some("namespace") => {
                validate_tools(tool.get("tools").ok_or(ProcessError::Protocol)?, depth + 1)?
            }
            _ => return Err(ProcessError::Protocol),
        }
    }
    Ok(())
}
fn safe_input(value: &serde_json::Value, depth: usize) -> bool {
    if depth > 32 {
        return false;
    }
    match value {
        serde_json::Value::Array(items) => items.iter().all(|item| safe_input(item, depth + 1)),
        serde_json::Value::Object(object) => {
            !matches!(
                object.get("type").and_then(serde_json::Value::as_str),
                Some("input_file" | "input_image" | "input_video" | "item_reference")
            ) && object.values().all(|item| safe_input(item, depth + 1))
        }
        _ => true, // Never inspect prompt or tool-output text as instructions/policy.
    }
}

struct DeadlineReader<'a> {
    stream: &'a mut UnixStream,
    deadline: Instant,
}
impl Read for DeadlineReader<'_> {
    fn read(&mut self, buffer: &mut [u8]) -> std::io::Result<usize> {
        let remaining = self
            .deadline
            .checked_duration_since(Instant::now())
            .filter(|duration| !duration.is_zero())
            .ok_or(std::io::ErrorKind::TimedOut)?;
        self.stream.set_read_timeout(Some(remaining))?;
        self.stream.read(buffer)
    }
}

fn read_request(input: &mut impl Read) -> Result<Request, ProcessError> {
    let mut headers = Vec::new();
    let mut byte = [0];
    while headers.len() < MAX_HEADERS {
        input.read_exact(&mut byte).map_err(failed)?;
        headers.push(byte[0]);
        if headers.ends_with(b"\r\n\r\n") {
            break;
        }
    }
    if !headers.ends_with(b"\r\n\r\n") {
        return Err(ProcessError::Limit);
    }
    let text = std::str::from_utf8(&headers).map_err(failed)?;
    let mut lines = text.split("\r\n");
    let first = lines.next().ok_or(ProcessError::Protocol)?;
    let (method, suffix) = match first {
        "POST /v1/responses HTTP/1.1" => ("POST", "/responses"),
        "POST /v1/responses/compact HTTP/1.1" => ("POST", "/responses/compact"),
        "GET /v1/models HTTP/1.1" => ("GET", "/models"),
        _ => return Err(ProcessError::Protocol),
    };
    let mut length = None;
    for line in lines.filter(|line| !line.is_empty()) {
        let (name, value) = line.split_once(':').ok_or(ProcessError::Protocol)?;
        if name.is_empty()
            || !name
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
        {
            return Err(ProcessError::Protocol);
        }
        match name.to_ascii_lowercase().as_str() {
            "transfer-encoding" | "upgrade" | "expect" => return Err(ProcessError::Protocol),
            "content-length" => {
                if length.is_some() {
                    return Err(ProcessError::Protocol);
                }
                let value = value.trim();
                if value.is_empty() || !value.bytes().all(|byte| byte.is_ascii_digit()) {
                    return Err(ProcessError::Protocol);
                }
                length = Some(value.parse::<usize>().map_err(failed)?);
            }
            _ => {} // Never forward client Authorization, Host, proxy or other header overrides.
        }
    }
    let length = length.unwrap_or(0);
    if length > MAX_BODY || (method == "GET" && length != 0) {
        return Err(ProcessError::Limit);
    }
    let mut body = vec![0; length];
    input.read_exact(&mut body).map_err(failed)?;
    if method == "POST"
        && !serde_json::from_slice::<serde_json::Value>(&body)
            .map_err(failed)?
            .is_object()
    {
        return Err(ProcessError::Protocol);
    }
    Ok(Request {
        method,
        suffix,
        body,
    })
}

fn proxy_request(
    stream: &mut UnixStream,
    base: &str,
    key: &str,
    policy: &ProviderPolicy,
) -> Result<(), ProcessError> {
    let request = read_request(&mut BufReader::new(DeadlineReader {
        stream: &mut *stream,
        deadline: Instant::now() + Duration::from_secs(30),
    }))
    .and_then(|mut request| {
        if let Err(error) = policy.authorize(&mut request) {
            policy.audit(
                "POLICY_REJECTED",
                serde_json::from_slice::<serde_json::Value>(&request.body)
                    .ok()
                    .as_ref(),
            );
            return Err(error);
        }
        policy.audit("REQUEST_ACCEPTED", None);
        Ok(request)
    });
    let request = match request {
        Ok(request) => request,
        Err(error) => {
            stream
                .write_all(
                    b"HTTP/1.1 400 Bad Request\r\nConnection: close\r\nContent-Length: 0\r\n\r\n",
                )
                .map_err(failed)?;
            return Err(error);
        }
    };
    stream
        .set_write_timeout(Some(Duration::from_secs(2)))
        .map_err(failed)?;
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build()
        .map_err(failed)?;
    runtime.block_on(async {
    // A connection-local async client gives cancellation a real owned future to drop,
    // without retaining a blocking upstream worker or a pool tied to a dead I/O runtime.
    let client = Client::builder().no_proxy().retry(reqwest::retry::never())
        .redirect(reqwest::redirect::Policy::none()).connect_timeout(Duration::from_secs(10))
        .timeout(TIMEOUT).build().map_err(failed)?;
    let url = format!("{base}{}", request.suffix);
    let builder = if request.method == "POST" { client.post(url) } else { client.get(url) };
    let sending = builder.bearer_auth(key).header("Accept", "text/event-stream, application/json")
        .header("Content-Type", "application/json").body(request.body).send();
    let mut response = tokio::select! {
            response = sending => response.map_err(|error| { policy.audit("UPSTREAM_TRANSPORT_FAILED", None); failed(error) })?,
            _ = disconnected(stream) => { policy.audit("DOWNSTREAM_ABANDONED", None); return Err(ProcessError::Transport); },
        };
        policy.audit(if response.status().is_success() { "UPSTREAM_2XX" } else { "UPSTREAM_REJECTED" }, None);
    let content_type = response
        .headers()
        .get("content-type")
        .and_then(|value| value.to_str().ok())
        .unwrap_or("application/octet-stream");
    write!(stream, "HTTP/1.1 {} Upstream\r\nConnection: close\r\nContent-Type: {content_type}\r\nCache-Control: no-store\r\n\r\n",
        response.status().as_u16()).map_err(failed)?;
    let mut total = 0u64;
    loop {
        let chunk = tokio::select! {
            chunk = response.chunk() => chunk.map_err(failed)?,
            _ = disconnected(stream) => return Err(ProcessError::Transport),
        };
        let Some(chunk) = chunk else { return Ok(()); };
        total += chunk.len() as u64;
        if total > MAX_RESPONSE {
            return Err(ProcessError::Limit);
        }
        stream.write_all(&chunk).map_err(failed)?;
        stream.flush().map_err(failed)?;
    }
    })
}

async fn disconnected(stream: &UnixStream) {
    use rustix::event::{PollFd, PollFlags, Timespec, poll};
    loop {
        let flags = PollFlags::HUP | PollFlags::ERR | PollFlags::NVAL | PollFlags::RDHUP;
        let mut descriptors = [PollFd::new(stream, flags)];
        if poll(
            &mut descriptors,
            Some(&Timespec {
                tv_sec: 0,
                tv_nsec: 0,
            }),
        )
        .is_err()
            || descriptors[0].revents().intersects(flags)
        {
            return;
        }
        tokio::time::sleep(Duration::from_millis(25)).await;
    }
}

/// Runs inside the allowlisted outer mount/network namespace. Only this trusted relay
/// may create a pathname Unix socket; the actual runtime is in a nested PID namespace
/// with an inherited syscall filter. The relay has no Host management protocol.
pub fn run_contained(
    socket: Option<&Path>,
    runtime: &Path,
    args: &[String],
) -> Result<(), ProcessError> {
    if let Some(socket) = socket {
        if socket != Path::new("/run/harness-provider.sock") {
            return Err(ProcessError::UnsafeProfile);
        }
        start_namespace_relay(socket)?;
    }
    let filter = syscall_filter()?;
    let mut command = Command::new("/usr/bin/bwrap");
    command
        .args([
            "--bind",
            "/",
            "/",
            "--unshare-pid",
            "--proc",
            "/proc",
            "--dev",
            "/dev",
            "--tmpfs",
            "/run", // The actual runtime does not need the outer relay's Unix socket.
            "--die-with-parent",
            "--new-session",
            "--cap-drop",
            "ALL",
            "--seccomp",
        ])
        .arg(filter.as_raw_fd().to_string())
        .arg("--")
        .arg(runtime)
        .args(args);
    if socket.is_some() {
        command.env("HARNESS_CODEX_API_BASE_URL", LOCAL_PROVIDER);
    }
    let status = command.status().map_err(failed)?;
    if status.success() {
        Ok(())
    } else {
        Err(ProcessError::Transport)
    }
}

fn start_namespace_relay(socket: &Path) -> Result<(), ProcessError> {
    let socket = socket.to_path_buf();
    let listener = TcpListener::bind("127.0.0.1:39121").map_err(failed)?;
    let count = Arc::new(AtomicUsize::new(0));
    thread::Builder::new()
        .name("aba-provider-relay".into())
        .spawn(move || {
            for incoming in listener.incoming() {
                let Ok(tcp) = incoming else {
                    break;
                };
                let Some(permit) = Permit::acquire(&count) else {
                    continue;
                };
                let socket = socket.clone();
                let _ = thread::Builder::new()
                    .name("aba-provider-stream".into())
                    .spawn(move || {
                        let _permit = permit;
                        let _ = relay(tcp, &socket);
                    });
            }
        })
        .map_err(failed)?;
    Ok(())
}
fn relay(tcp: TcpStream, socket: &Path) -> Result<(), ProcessError> {
    let unix = UnixStream::connect(socket).map_err(failed)?;
    tcp.set_read_timeout(Some(TIMEOUT)).map_err(failed)?;
    tcp.set_write_timeout(Some(TIMEOUT)).map_err(failed)?;
    unix.set_read_timeout(Some(TIMEOUT)).map_err(failed)?;
    unix.set_write_timeout(Some(TIMEOUT)).map_err(failed)?;
    let mut from_tcp = tcp.try_clone().map_err(failed)?;
    let mut to_unix = unix.try_clone().map_err(failed)?;
    let mut from_unix = unix;
    let mut to_tcp = tcp;
    thread::scope(|scope| {
        scope.spawn(move || {
            let _ = std::io::copy(
                &mut (&mut from_tcp).take(MAX_BODY as u64 + MAX_HEADERS as u64 + 1),
                &mut to_unix,
            );
            let _ = to_unix.shutdown(Shutdown::Write);
        });
        let _ = std::io::copy(
            &mut (&mut from_unix).take(MAX_RESPONSE + MAX_HEADERS as u64 + 1),
            &mut to_tcp,
        );
        let _ = to_tcp.shutdown(Shutdown::Both);
    });
    Ok(())
}

fn syscall_filter() -> Result<std::fs::File, ProcessError> {
    if !cfg!(target_arch = "x86_64") {
        return Err(ProcessError::UnsafeProfile);
    }
    // Linux x86-64 classic BPF; check architecture and reject x32 before syscall matching.
    // Do not try to inspect sockaddr pointers: destination policy is the private netns+fixed proxy.
    let mut program: Vec<(u16, u8, u8, u32)> = vec![
        (0x20, 0, 0, 4),
        (0x15, 1, 0, 0xc000003e),
        (0x06, 0, 0, 0x80000000),
        (0x20, 0, 0, 0),
        (0x45, 0, 1, 0x40000000),
        (0x06, 0, 0, 0x80000000),
    ];
    for syscall in [101, 248, 249, 250, 310, 311, 425, 426, 427, 438] {
        program.extend([(0x15, 0, 1, syscall), (0x06, 0, 0, 0x00050001)]);
    }
    // Native bwrap needs private connected stream/seqpacket pairs. Datagram pairs can
    // be re-targeted to workspace pathname services, so explicitly deny that path too.
    program.extend([
        (0x15, 0, 4, 41),
        (0x20, 0, 0, 16),
        (0x15, 0, 1, 1),
        (0x06, 0, 0, 0x00050001),
        (0x06, 0, 0, 0x7fff0000),
        (0x15, 0, 4, 53),
        (0x20, 0, 0, 24),
        (0x54, 0, 0, 0xf),
        (0x15, 0, 1, 2),
        (0x06, 0, 0, 0x00050001),
        (0x06, 0, 0, 0x7fff0000),
    ]);
    let mut file = tempfile::tempfile().map_err(failed)?;
    for (code, jt, jf, k) in program {
        file.write_all(&code.to_le_bytes()).map_err(failed)?;
        file.write_all(&[jt, jf]).map_err(failed)?;
        file.write_all(&k.to_le_bytes()).map_err(failed)?;
    }
    use std::io::Seek as _;
    file.rewind().map_err(failed)?;
    // This is the sole deliberately inherited extra FD; bwrap consumes/closes the filter FD.
    rustix::io::fcntl_setfd(&file, rustix::io::FdFlags::empty()).map_err(failed)?;
    Ok(file)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn fixed_provider_path_and_body() -> Result<(), ProcessError> {
        let mut bytes = &b"POST /v1/responses HTTP/1.1\r\nHost: ignored\r\nAuthorization: ignored\r\nContent-Length: 2\r\n\r\n{}"[..];
        let value = read_request(&mut bytes)?;
        assert_eq!(value.suffix, "/responses");
        assert_eq!(value.body, b"{}");
        Ok(())
    }
    #[test]
    fn arbitrary_destinations_and_http_ambiguity_are_rejected() {
        for request in [
            "CONNECT localhost:18082 HTTP/1.1\r\n\r\n",
            "GET http://localhost:18082/v1/models HTTP/1.1\r\n\r\n",
            "GET /v1/../admin HTTP/1.1\r\n\r\n",
            "GET /v1/models?url=http://localhost HTTP/1.1\r\n\r\n",
            "POST /v1/responses HTTP/1.1\r\nTransfer-Encoding: chunked\r\n\r\n",
            "GET /v1/models HTTP/1.1\r\nUpgrade: websocket\r\n\r\n",
            "GET /v1/models HTTP/1.1\r\nContent-Length: 0\r\nContent-Length: 0\r\n\r\n",
            "GET /v1/models HTTP/1.1\r\nContent-Length: 999999999\r\n\r\n",
        ] {
            assert!(read_request(&mut request.as_bytes()).is_err());
        }
    }
    #[test]
    fn configured_origin_cannot_include_credentials_or_redirect_controls() {
        for base in [
            "file:///etc/passwd",
            "https://user:secret@example.com/v1",
            "https://example.com/v1?q=x",
            "http://unverified.example/v1",
        ] {
            assert!(provider_base(base).is_err());
        }
        assert!(provider_base("https://provider.example/v1").is_ok());
        assert!(provider_base("http://127.0.0.1:8317/v1").is_err());
    }
    #[test]
    fn request_concurrency_is_bounded() {
        let count = Arc::new(AtomicUsize::new(0));
        let permits: Vec<_> = (0..4).map(|_| Permit::acquire(&count)).collect();
        assert!(permits.iter().all(Option::is_some));
        assert!(Permit::acquire(&count).is_none());
        drop(permits);
        assert!(Permit::acquire(&count).is_some());
    }

    #[test]
    fn provider_capabilities_are_frozen_to_the_local_policy() -> Result<(), ProcessError> {
        let policy = ProviderPolicy::new("allowed-model", "allowed-model")?;
        for body in [
            serde_json::json!({"model":"other-model","input":"test"}),
            serde_json::json!({"model":"allowed-model","background":true}),
            serde_json::json!({"model":"allowed-model","previous_response_id":"foreign"}),
            serde_json::json!({"model":"allowed-model","store":true}),
            serde_json::json!({"model":"allowed-model","tools":[{"type":"web_search"}]}),
            serde_json::json!({"model":"allowed-model","tools":[{"type":"mcp","server_url":"https://example.com"}]}),
            serde_json::json!({"model":"allowed-model","input":[{"type":"input_file","file_url":"https://example.com"}]}),
            serde_json::json!({"model":"allowed-model","max_output_tokens":1_000_000}),
        ] {
            let mut request = Request {
                method: "POST",
                suffix: "/responses",
                body: serde_json::to_vec(&body).map_err(failed)?,
            };
            assert!(policy.authorize(&mut request).is_err());
        }
        let mut request = Request { method: "POST", suffix: "/responses", body: serde_json::to_vec(&serde_json::json!({
            "model":"allowed-model", "input":"Opaque text mentioning file_url and instructions remains text.",
            "tools":[{"type":"namespace","name":"functions","tools":[{"type":"function","name":"read_file"},{"type":"custom","name":"apply_patch"}]}]
        })).map_err(failed)? };
        policy.authorize(&mut request)?;
        let value: serde_json::Value = serde_json::from_slice(&request.body).map_err(failed)?;
        assert_eq!(value["store"], false);
        assert_eq!(value["max_output_tokens"], 16_384);
        Ok(())
    }

    #[test]
    fn slow_drip_does_not_extend_the_absolute_acquisition_deadline()
    -> Result<(), Box<dyn std::error::Error>> {
        let (mut server, mut client) = UnixStream::pair()?;
        let writer = thread::spawn(move || {
            for _ in 0..100 {
                if client.write_all(b"x").is_err() {
                    break;
                }
                thread::sleep(Duration::from_millis(10));
            }
        });
        let began = Instant::now();
        let result = read_request(&mut BufReader::new(DeadlineReader {
            stream: &mut server,
            deadline: began + Duration::from_millis(55),
        }));
        assert!(result.is_err());
        assert!(began.elapsed() < Duration::from_secs(1));
        drop(server);
        writer.join().map_err(|_| "deadline writer failed")?;
        Ok(())
    }

    fn synthetic_request() -> Vec<u8> {
        let body = r#"{"model":"allowed-model","input":"synthetic"}"#;
        format!("POST /v1/responses HTTP/1.1\r\nHost: attacker.invalid\r\nAuthorization: Bearer wrong\r\nX-Forwarded-Host: attacker.invalid\r\nContent-Length: {}\r\n\r\n{body}", body.len()).into_bytes()
    }

    fn capture_headers(stream: &mut TcpStream) -> std::io::Result<String> {
        let mut header = Vec::new();
        let mut byte = [0];
        while header.len() < MAX_HEADERS && !header.ends_with(b"\r\n\r\n") {
            stream.read_exact(&mut byte)?;
            header.push(byte[0]);
        }
        let header = String::from_utf8(header).map_err(std::io::Error::other)?;
        let length = header
            .lines()
            .find_map(|line| {
                line.to_ascii_lowercase()
                    .strip_prefix("content-length:")
                    .and_then(|value| value.trim().parse::<usize>().ok())
            })
            .unwrap_or(0);
        if length > MAX_BODY {
            return Err(std::io::ErrorKind::InvalidData.into());
        }
        stream.read_exact(&mut vec![0; length])?;
        Ok(header)
    }

    #[test]
    fn actual_forwarding_ignores_header_overrides_and_does_not_follow_redirects()
    -> Result<(), Box<dyn std::error::Error>> {
        let upstream = TcpListener::bind("127.0.0.1:0")?;
        let unexpected = TcpListener::bind("127.0.0.1:0")?;
        unexpected.set_nonblocking(true)?;
        let base = format!("http://{}/v1", upstream.local_addr()?); // Test-only direct helper; public bootstrap requires HTTPS.
        let destination = unexpected.local_addr()?;
        let (captured, headers) = std::sync::mpsc::channel();
        let receiver = thread::spawn(move || -> std::io::Result<()> {
            let (mut stream, _) = upstream.accept()?;
            stream.set_read_timeout(Some(Duration::from_secs(2)))?;
            captured
                .send(capture_headers(&mut stream)?)
                .map_err(std::io::Error::other)?;
            write!(
                stream,
                "HTTP/1.1 302 Found\r\nLocation: http://{destination}/steal\r\nContent-Length: 0\r\n\r\n"
            )?;
            Ok(())
        });
        let (mut server, mut client) = UnixStream::pair()?;
        let worker = thread::spawn(move || -> Result<(), ProcessError> {
            proxy_request(
                &mut server,
                &base,
                "synthetic-upstream-credential",
                &ProviderPolicy::new("allowed-model", "allowed-model")?,
            )
        });
        client.write_all(&synthetic_request())?;
        client.set_read_timeout(Some(Duration::from_secs(2)))?;
        let mut response = String::new();
        client.read_to_string(&mut response)?;
        worker.join().map_err(|_| "proxy worker failed")??;
        receiver.join().map_err(|_| "upstream worker failed")??;
        let headers = headers
            .recv_timeout(Duration::from_secs(1))?
            .to_ascii_lowercase();
        assert!(headers.starts_with("post /v1/responses http/1.1"));
        assert!(headers.contains("authorization: bearer synthetic-upstream-credential\r\n"));
        assert!(!headers.contains("attacker.invalid"));
        assert!(!headers.contains("x-forwarded-host"));
        assert!(response.starts_with("HTTP/1.1 302"));
        assert!(!response.to_ascii_lowercase().contains("location:"));
        assert!(
            matches!(unexpected.accept(), Err(error) if error.kind() == std::io::ErrorKind::WouldBlock)
        );
        Ok(())
    }

    #[test]
    fn repeated_downstream_abandonment_releases_upstream_worker_permits()
    -> Result<(), Box<dyn std::error::Error>> {
        let count = Arc::new(AtomicUsize::new(0));
        for _ in 0..4 {
            let listener = TcpListener::bind("127.0.0.1:0")?;
            let base = format!("http://{}/v1", listener.local_addr()?);
            let (accepted, ready) = std::sync::mpsc::channel();
            let (finish, finished) = std::sync::mpsc::channel();
            let upstream = thread::spawn(move || -> std::io::Result<()> {
                let (mut stream, _) = listener.accept()?;
                stream.set_read_timeout(Some(Duration::from_secs(2)))?;
                capture_headers(&mut stream)?;
                accepted.send(()).map_err(std::io::Error::other)?;
                let _ = finished.recv_timeout(Duration::from_secs(3));
                Ok(())
            });
            let permit = Permit::acquire(&count).ok_or("permit leaked")?;
            let (mut server, mut client) = UnixStream::pair()?;
            let worker = thread::spawn(move || -> Result<(), ProcessError> {
                let _permit = permit;
                proxy_request(
                    &mut server,
                    &base,
                    "synthetic-key",
                    &ProviderPolicy::new("allowed-model", "allowed-model")?,
                )
            });
            client.write_all(&synthetic_request())?;
            ready.recv_timeout(Duration::from_secs(2))?;
            let began = Instant::now();
            drop(client);
            assert!(worker.join().map_err(|_| "cancel worker failed")?.is_err());
            assert!(began.elapsed() < Duration::from_secs(1));
            assert_eq!(count.load(Ordering::Acquire), 0);
            finish.send(())?;
            upstream.join().map_err(|_| "upstream join failed")??;
        }
        Ok(())
    }
}
