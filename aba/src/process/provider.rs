//! A purpose-specific provider data path, not a general HTTP/SOCKS or host-control proxy.
//! Runs inside the Run's cgroup but outside its network/PID namespace; no root privileges.
use std::io::{BufReader, Read, Write};
use std::net::{Shutdown, TcpListener, TcpStream};
use std::os::fd::AsRawFd as _;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::Path;
use std::process::Command;
use std::sync::{
    Arc,
    atomic::{AtomicUsize, Ordering},
};
use std::thread;
use std::time::Duration;

use reqwest::blocking::Client;
use url::Url;

use super::ProcessError;

const MAX_HEADERS: usize = 16 * 1024;
const MAX_BODY: usize = 4 * 1024 * 1024;
const MAX_RESPONSE: u64 = 128 * 1024 * 1024;
const TIMEOUT: Duration = Duration::from_secs(120);
const MAX_CONNECTIONS: usize = 4;
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
    if key.is_empty() || key.len() > 4096 || key.bytes().any(|byte| byte < 0x20 || byte == 0x7f) {
        return Err(ProcessError::UnsafeProfile);
    }
    let listener = UnixListener::bind(socket).map_err(failed)?; // Never removes/replaces an old socket.
    let client = Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .connect_timeout(Duration::from_secs(10))
        .timeout(Duration::from_secs(300))
        .build()
        .map_err(failed)?;
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
                let client = client.clone();
                let base = base.clone();
                let key = key.clone();
                let _ = thread::Builder::new()
                    .name("aba-provider-request".into())
                    .spawn(move || {
                        let _permit = permit;
                        let _ = stream.set_read_timeout(Some(TIMEOUT));
                        let _ = stream.set_write_timeout(Some(TIMEOUT));
                        // Only constant diagnostics go on the wire; no local credentials/error chains in logs.
                        if proxy_request(&mut stream, &client, &base, &key).is_err() {
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
    client: &Client,
    base: &str,
    key: &str,
) -> Result<(), ProcessError> {
    let request = match read_request(&mut BufReader::new(&mut *stream)) {
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
    let url = format!("{base}{}", request.suffix);
    let builder = if request.method == "POST" {
        client.post(url)
    } else {
        client.get(url)
    };
    let response = builder
        .bearer_auth(key)
        .header("Accept", "text/event-stream, application/json")
        .header("Content-Type", "application/json")
        .body(request.body)
        .send()
        .map_err(failed)?;
    let content_type = response
        .headers()
        .get("content-type")
        .and_then(|value| value.to_str().ok())
        .unwrap_or("application/octet-stream");
    write!(stream, "HTTP/1.1 {} Upstream\r\nConnection: close\r\nContent-Type: {content_type}\r\nCache-Control: no-store\r\n\r\n",
        response.status().as_u16()).map_err(failed)?;
    let mut response = response.take(MAX_RESPONSE + 1);
    let mut bytes = [0; 16 * 1024];
    let mut total = 0u64;
    loop {
        let count = response.read(&mut bytes).map_err(failed)?;
        if count == 0 {
            return Ok(());
        }
        total += count as u64;
        if total > MAX_RESPONSE {
            return Err(ProcessError::Limit);
        }
        stream.write_all(&bytes[..count]).map_err(failed)?;
        stream.flush().map_err(failed)?;
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
    for syscall in [101, 248, 249, 250, 310, 311, 438] {
        program.extend([(0x15, 0, 1, syscall), (0x06, 0, 0, 0x00050001)]);
    }
    // socket(AF_UNIX, ...) is denied. socketpair remains available for private process IPC.
    program.extend([
        (0x15, 0, 3, 41),
        (0x20, 0, 0, 16),
        (0x15, 0, 1, 1),
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
}
