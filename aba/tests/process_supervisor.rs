use std::fs;

use aba::config::{RuntimeProfile, WorkspaceProfile};
use aba::process::{AgentProcess, ProcessError};
use agent_client_protocol::schema::v1::{ContentBlock, PromptRequest, TextContent};

#[test]
fn runs_stable_v1_prompt_through_external_agent() -> Result<(), Box<dyn std::error::Error>> {
    let directory = tempfile::tempdir()?;
    let command = fs::canonicalize(env!("CARGO_BIN_EXE_test-agent"))?;
    let workspace = fs::canonicalize(directory.path())?;
    let runtime = RuntimeProfile {
        id: "test-agent".to_owned(),
        display_name: "Harness Test Agent".to_owned(),
        command,
        args: Vec::new(),
        env_allow: Vec::new(),
        max_sessions: Some(1),
    };
    let workspace_profile = WorkspaceProfile {
        id: "fixture".to_owned(),
        display_name: "Fixture".to_owned(),
        path: workspace,
        allowed_runtimes: vec![runtime.id.clone()],
        follow_symlinks: false,
    };
    let mut process = AgentProcess::start(&runtime, &workspace_profile)?;
    let platform_session_id = "01010101010101010101010101010101";
    let prompt = PromptRequest::new(
        platform_session_id,
        vec![ContentBlock::Text(TextContent::new("hello over ACP"))],
    );
    let request = serde_json::to_vec(&serde_json::json!({
        "jsonrpc": "2.0",
        "id": "request-1",
        "method": "session/prompt",
        "params": prompt,
    }))?;
    let responses = process.prompt(&request, platform_session_id)?;
    assert_eq!(responses.len(), 2);
    let update: serde_json::Value = serde_json::from_slice(&responses[0])?;
    let completed: serde_json::Value = serde_json::from_slice(&responses[1])?;
    assert_eq!(update["method"], "session/update");
    assert_eq!(update["params"]["sessionId"], platform_session_id);
    assert_eq!(
        update["params"]["update"]["sessionUpdate"],
        "agent_message_chunk"
    );
    assert_eq!(
        update["params"]["update"]["content"]["text"],
        "Harness deterministic test agent received: hello over ACP"
    );
    assert_eq!(completed["id"], "request-1");
    assert_eq!(completed["result"]["stopReason"], "end_turn");

    let wrong_method = serde_json::to_vec(&serde_json::json!({
        "jsonrpc": "2.0",
        "id": "request-2",
        "method": "session/unknown",
        "params": {},
    }))?;
    assert_eq!(
        process.prompt(&wrong_method, platform_session_id),
        Err(ProcessError::Protocol)
    );
    let malformed = serde_json::to_vec(&serde_json::json!({
        "jsonrpc": "2.0",
        "id": "request-3",
        "method": "session/prompt",
        "params": {"sessionId": platform_session_id},
    }))?;
    assert_eq!(
        process.prompt(&malformed, platform_session_id),
        Err(ProcessError::Protocol)
    );
    Ok(())
}
