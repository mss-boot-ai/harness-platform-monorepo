use std::fs;
use std::time::{Duration, Instant};

use aba::config::{RuntimeProfile, WorkspaceProfile};
use aba::process::{AgentEvent, AgentProcess, ProcessError};
use serde_json::{Value, json};

type TestResult = Result<(), Box<dyn std::error::Error>>;
const SESSION: &str = "01010101010101010101010101010101";

fn fixture() -> Result<(AgentProcess, tempfile::TempDir), Box<dyn std::error::Error>> {
    let directory = tempfile::tempdir()?;
    let runtime = RuntimeProfile {
        id: "fixture".to_owned(), display_name: "Deterministic fixture".to_owned(),
        command: fs::canonicalize("/usr/bin/python3")?,
        args: vec![format!("{}/tests/fixtures/duplex_agent.py", env!("CARGO_MANIFEST_DIR"))],
        env_allow: Vec::new(), max_sessions: Some(1),
    };
    let workspace = WorkspaceProfile {
        id: "fixture".to_owned(), display_name: "Fixture".to_owned(),
        path: fs::canonicalize(directory.path())?, allowed_runtimes: vec![runtime.id.clone()],
        follow_symlinks: false,
    };
    Ok((AgentProcess::start(&runtime, &workspace)?, directory))
}

fn next(process: &mut AgentProcess) -> Result<AgentEvent, Box<dyn std::error::Error>> {
    let deadline = Instant::now() + Duration::from_secs(5);
    while Instant::now() < deadline {
        if let Some(event) = process.poll()? { return Ok(event); }
        std::thread::sleep(Duration::from_millis(5));
    }
    Err("fixture did not emit a bounded event".into())
}

fn submit(process: &mut AgentProcess, value: Value, id: u8) -> Result<(), ProcessError> {
    let bytes = serde_json::to_vec(&value).map_err(|_| ProcessError::Protocol)?;
    process.submit(&bytes, SESSION, [id; 16])
}

fn prompt(id: &str, text: &str) -> Value {
    json!({"jsonrpc": "2.0", "id": id, "method": "session/prompt", "params": {"sessionId": SESSION, "prompt": [{"type": "text", "text": text}]}})
}

#[test]
fn first_chunk_arrives_before_completion_and_cancel_keeps_session_usable() -> TestResult {
    let (mut process, _directory) = fixture()?;
    let began = Instant::now();
    submit(&mut process, prompt("one", "wait"), 1)?;
    assert!(began.elapsed() < Duration::from_secs(1));
    let first = next(&mut process)?;
    let message: Value = serde_json::from_slice(&first.message)?;
    assert_eq!(message["params"]["update"]["content"]["text"], "early chunk");
    assert_eq!(first.completed_dispatch, None);
    assert_eq!(process.pending_dispatches(), vec![[1; 16]]);
    submit(&mut process, json!({"jsonrpc": "2.0", "method": "session/cancel", "params": {"sessionId": SESSION}}), 2)?;
    let mut completed = false;
    let mut dispatched = false;
    for _ in 0..2 {
        let event = next(&mut process)?;
        if !event.message.is_empty() {
            let reply: Value = serde_json::from_slice(&event.message)?;
            assert_eq!(reply["id"], "one");
            assert_eq!(reply["result"]["stopReason"], "cancelled");
            completed = event.completed_dispatch == Some([1; 16]);
        } else { dispatched = event.completed_dispatch == Some([2; 16]); }
    }
    assert!(completed && dispatched);
    submit(&mut process, prompt("two", "echo"), 3)?;
    let _chunk = next(&mut process)?;
    let completed = next(&mut process)?;
    assert_eq!(completed.completed_dispatch, Some([3; 16]));
    Ok(())
}

#[test]
fn permission_is_bound_to_the_original_request_and_cannot_be_reused() -> TestResult {
    let (mut process, _directory) = fixture()?;
    submit(&mut process, prompt("one", "permission"), 1)?;
    let _chunk = next(&mut process)?;
    let request = next(&mut process)?;
    let request: Value = serde_json::from_slice(&request.message)?;
    assert_eq!(request["method"], "session/request_permission");
    assert_eq!(request["params"]["sessionId"], SESSION);
    assert_ne!(request["id"], 77);
    let bad = json!({"jsonrpc": "2.0", "id": request["id"], "result": {"outcome": {"outcome": "selected", "optionId": "forged"}}});
    assert_eq!(submit(&mut process, bad, 2), Err(ProcessError::Protocol));
    let accepted = json!({"jsonrpc": "2.0", "id": request["id"], "result": {"outcome": {"outcome": "selected", "optionId": "allow"}}});
    submit(&mut process, accepted.clone(), 3)?;
    assert_eq!(submit(&mut process, accepted, 4), Err(ProcessError::Protocol));
    let first = next(&mut process)?;
    let second = next(&mut process)?;
    let completion = if !first.message.is_empty() { first } else { second };
    assert_eq!(completion.completed_dispatch, Some([1; 16]));
    Ok(())
}

#[test]
fn exposes_real_configuration_and_accepts_only_announced_values() -> TestResult {
    let (mut process, _directory) = fixture()?;
    submit(&mut process, json!({"jsonrpc": "2.0", "id": "describe", "method": "_mss/session/describe", "params": {"sessionId": SESSION}}), 1)?;
    let event = next(&mut process)?;
    let info: Value = serde_json::from_slice(&event.message)?;
    assert_eq!(info["result"]["session"]["configOptions"][0]["currentValue"], "small");
    let set = |value: &str| json!({"jsonrpc": "2.0", "id": "set", "method": "session/set_config_option", "params": {"sessionId": SESSION, "configId": "model", "value": value}});
    submit(&mut process, set("invented-model"), 2)?;
    let rejected: Value = serde_json::from_slice(&next(&mut process)?.message)?;
    assert_eq!(rejected["error"]["message"], "UNSUPPORTED_CONFIGURATION");
    submit(&mut process, set("large"), 3)?;
    let accepted: Value = serde_json::from_slice(&next(&mut process)?.message)?;
    assert_eq!(accepted["result"]["configOptions"][0]["currentValue"], "large");
    Ok(())
}

#[test]
fn rejects_cross_session_binding_and_does_not_forward_arbitrary_methods() -> TestResult {
    let (mut process, _directory) = fixture()?;
    let command = json!({"jsonrpc": "2.0", "id": "evil", "method": "terminal/create", "params": {"sessionId": SESSION, "command": "do not execute"}});
    submit(&mut process, command, 1)?;
    let error: Value = serde_json::from_slice(&next(&mut process)?.message)?;
    assert_eq!(error["error"]["code"], -32601);
    let request = serde_json::to_vec(&prompt("other", "echo"))?;
    assert_eq!(process.submit(&request, "different-session", [2; 16]), Err(ProcessError::Protocol));
    Ok(())
}
