use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

use agent_client_protocol::schema::v1::{
    AgentCapabilities, ContentBlock, ContentChunk, InitializeRequest, InitializeResponse,
    NewSessionRequest, NewSessionResponse, PromptRequest, PromptResponse, SessionId,
    SessionNotification, SessionUpdate, StopReason, TextContent,
};
use agent_client_protocol::{Agent, ConnectionTo, Result, Stdio};

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<()> {
    let next_session = Arc::new(AtomicU64::new(1));
    let sessions = Arc::clone(&next_session);
    Agent
        .builder()
        .name("harness-deterministic-test-agent")
        .on_receive_request(
            async |request: InitializeRequest, responder, _connection| {
                responder.respond(
                    InitializeResponse::new(request.protocol_version)
                        .agent_capabilities(AgentCapabilities::new()),
                )
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |_request: NewSessionRequest, responder, _connection| {
                let number = sessions.fetch_add(1, Ordering::Relaxed);
                responder.respond(NewSessionResponse::new(SessionId::new(format!(
                    "harness-test-session-{number}"
                ))))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .on_receive_request(
            async move |request: PromptRequest,
                        responder: agent_client_protocol::Responder<PromptResponse>,
                        connection: ConnectionTo<agent_client_protocol::Client>| {
                let prompt = request
                    .prompt
                    .iter()
                    .filter_map(|block| match block {
                        ContentBlock::Text(text) => Some(text.text.as_str()),
                        _ => None,
                    })
                    .collect::<Vec<_>>()
                    .join("\n");
                if prompt.starts_with("crash:") {
                    std::process::exit(70);
                }
                if prompt.starts_with("delay:") {
                    tokio::time::sleep(std::time::Duration::from_millis(500)).await;
                }
                connection.send_notification(SessionNotification::new(
                    request.session_id,
                    SessionUpdate::AgentMessageChunk(ContentChunk::new(ContentBlock::Text(
                        TextContent::new(format!(
                            "Harness deterministic test agent received: {prompt}"
                        )),
                    ))),
                ))?;
                responder.respond(PromptResponse::new(StopReason::EndTurn))
            },
            agent_client_protocol::on_receive_request!(),
        )
        .connect_to(Stdio::new())
        .await
}
