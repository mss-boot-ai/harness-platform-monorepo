//! Explicit CLI-only, synthetic live-runtime acceptance. No remote management method.
use std::path::{Path, PathBuf};
use std::time::{Duration, Instant};

use serde_json::{Value, json};

use super::{AgentProcess, ProcessError, supervision::Supervisor};

enum Permission {
    Reject,
    AllowFile(PathBuf),
}

pub fn controls(
    process: &mut AgentProcess,
    supervisor: &Supervisor,
    run: [u8; 16],
    workspace: &Path,
) -> Result<(), ProcessError> {
    let session: String = run.iter().map(|byte| format!("{byte:02x}")).collect();
    let denied = workspace.join("scope-denied.txt");
    let approved = workspace.join("scope-approved.txt");
    if denied.exists() || approved.exists() {
        return Err(ProcessError::UnsafeProfile);
    }
    let (denial, permissions) = request(
        process,
        supervisor,
        run,
        &session,
        20,
        "session/prompt",
        json!({"prompt":[{"type":"text","text":"Use apply_patch to create scope-denied.txt containing HARNESS_SCOPE_DENIED. Do not use shell writes. If permission is denied, stop and report the denial."}]}),
        Permission::Reject,
        false,
    )?;
    if permissions != 1 || denied.exists() || denial.get("error").is_some() {
        return Err(ProcessError::Protocol);
    }
    let (acceptance, permissions) = request(
        process,
        supervisor,
        run,
        &session,
        21,
        "session/prompt",
        json!({"prompt":[{"type":"text","text":"Use apply_patch to create scope-approved.txt containing exactly HARNESS_SCOPE_APPROVED. Do not use shell writes or change any other file."}]}),
        Permission::AllowFile(approved.clone()),
        false,
    )?;
    if permissions != 1
        || acceptance
            .pointer("/result/stopReason")
            .and_then(Value::as_str)
            != Some("end_turn")
        || std::fs::read_to_string(approved)
            .map_err(|_| ProcessError::Protocol)?
            .trim()
            != "HARNESS_SCOPE_APPROVED"
    {
        return Err(ProcessError::Protocol);
    }
    println!("Isolated file approval denial and approval confirmed.");

    let (configured, _) = request(
        process,
        supervisor,
        run,
        &session,
        22,
        "session/set_config_option",
        json!({"configId":"effort","value":"low"}),
        Permission::Reject,
        false,
    )?;
    if configured
        .pointer("/result/configOptions")
        .and_then(Value::as_array)
        .is_none_or(|options| {
            !options.iter().any(|option| {
                option.get("id").and_then(Value::as_str) == Some("effort")
                    && option.get("currentValue").and_then(Value::as_str) == Some("low")
            })
        })
    {
        return Err(ProcessError::Protocol);
    }
    println!("Isolated effective configuration change confirmed.");

    let (cancelled, _) = request(
        process,
        supervisor,
        run,
        &session,
        23,
        "session/prompt",
        json!({"prompt":[{"type":"text","text":"Run the shell command sleep 30 using exec_command, wait for it to finish, then reply HARNESS_SCOPE_SLEEP_FINISHED. Do not write any files."}]}),
        Permission::Reject,
        true,
    )?;
    if cancelled
        .pointer("/result/stopReason")
        .and_then(Value::as_str)
        != Some("cancelled")
    {
        return Err(ProcessError::Protocol);
    }
    let (continued, _) = request(
        process,
        supervisor,
        run,
        &session,
        24,
        "session/prompt",
        json!({"prompt":[{"type":"text","text":"Reply with exactly HARNESS_SCOPE_CONTINUED. No tools are needed."}]}),
        Permission::Reject,
        false,
    )?;
    if continued
        .pointer("/result/stopReason")
        .and_then(Value::as_str)
        != Some("end_turn")
    {
        return Err(ProcessError::Protocol);
    }
    println!("Actual sleep tool start, turn cancellation and continuation confirmed.");
    Ok(())
}

#[allow(clippy::too_many_arguments)]
fn request(
    process: &mut AgentProcess,
    supervisor: &Supervisor,
    run: [u8; 16],
    session: &str,
    number: u8,
    method: &str,
    mut params: Value,
    permission: Permission,
    cancel_sleep: bool,
) -> Result<(Value, usize), ProcessError> {
    params["sessionId"] = session.into();
    let id = format!("scope-controls-{number}");
    process.submit(
        &serde_json::to_vec(&json!({"jsonrpc":"2.0","id":id,"method":method,"params":params}))
            .map_err(|_| ProcessError::Protocol)?,
        session,
        [number; 16],
    )?;
    let deadline = Instant::now() + Duration::from_secs(120);
    let mut decisions = 0usize;
    let mut cancelled = false;
    loop {
        if Instant::now() >= deadline {
            return Err(ProcessError::Timeout);
        }
        if cancel_sleep && !cancelled && supervisor.has_process_command(run, &["sleep", "30"])? {
            process.submit(&serde_json::to_vec(&json!({"jsonrpc":"2.0","method":"session/cancel","params":{"sessionId":session}}))
                .map_err(|_| ProcessError::Protocol)?, session, [number + 64; 16])?;
            cancelled = true;
        }
        let Some(event) = process.poll()? else {
            std::thread::sleep(Duration::from_millis(10));
            continue;
        };
        if event.message.is_empty() {
            continue;
        }
        let value: Value =
            serde_json::from_slice(&event.message).map_err(|_| ProcessError::Protocol)?;
        if value.get("method").and_then(Value::as_str) == Some("session/request_permission") {
            if decisions >= 4 {
                return Err(ProcessError::Limit);
            }
            let allow = match &permission {
                Permission::Reject => false,
                Permission::AllowFile(path) => exact_addition(&value, path),
            };
            let kind = if allow { "allow_once" } else { "reject_once" };
            let option = value
                .pointer("/params/options")
                .and_then(Value::as_array)
                .and_then(|options| {
                    options
                        .iter()
                        .find(|option| option.get("kind").and_then(Value::as_str) == Some(kind))
                })
                .and_then(|option| option.get("optionId"))
                .and_then(Value::as_str)
                .ok_or(ProcessError::Protocol)?;
            let response = json!({"jsonrpc":"2.0","id":value.get("id").ok_or(ProcessError::Protocol)?,
                "result":{"outcome":{"outcome":"selected","optionId":option}}});
            process.submit(
                &serde_json::to_vec(&response).map_err(|_| ProcessError::Protocol)?,
                session,
                [number + 32 + decisions as u8; 16],
            )?;
            decisions += 1;
        }
        if value.get("id").and_then(Value::as_str) == Some(id.as_str()) {
            if cancel_sleep && !cancelled {
                return Err(ProcessError::Protocol);
            }
            return Ok((value, decisions));
        }
    }
}

fn exact_addition(permission: &Value, path: &Path) -> bool {
    permission
        .pointer("/params/toolCall/kind")
        .and_then(Value::as_str)
        == Some("edit")
        && permission
            .pointer("/params/toolCall/rawInput/changes")
            .and_then(Value::as_array)
            .is_some_and(|changes| {
                changes.len() == 1
                    && changes[0]
                        .get("path")
                        .and_then(Value::as_str)
                        .is_some_and(|value| {
                            Path::new(value) == path
                                || path
                                    .file_name()
                                    .is_some_and(|name| Path::new(value) == Path::new(name))
                        })
                    && changes[0]
                        .get("diff")
                        .and_then(Value::as_str)
                        .is_some_and(|diff| diff.contains("HARNESS_SCOPE_APPROVED"))
                    && changes[0].pointer("/kind/move_path").is_none()
            })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn probe_approval_is_scoped_to_one_synthetic_file() {
        let mut message = json!({"params":{"toolCall":{"kind":"edit","rawInput":{"changes":[{
            "path":"/srv/harness-workspaces/probe/scope-approved.txt","diff":"+HARNESS_SCOPE_APPROVED","kind":{"type":"add"}
        }]}}}});
        let path = Path::new("/srv/harness-workspaces/probe/scope-approved.txt");
        assert!(exact_addition(&message, path));
        message["params"]["toolCall"]["rawInput"]["changes"][0]["path"] =
            "/var/lib/harness-aba/identity.json".into();
        assert!(!exact_addition(&message, path));
    }
}
