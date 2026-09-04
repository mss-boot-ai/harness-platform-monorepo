use std::fs;
#[cfg(unix)]
use std::os::unix::fs::PermissionsExt as _;
use std::time::{SystemTime, UNIX_EPOCH};

use p256::ecdsa::VerifyingKey;
use prost::Message as _;

use super::GatewayError;
use super::transcript::{ControlInput, control};
use crate::config::AgentConfig;
use crate::crypto::{sign_p1363_low_s, verify_p1363_low_s};
use crate::identity::EndpointIdentity;
use crate::protocol::awpv1::{
    ControlFrame, ControlType, OpenTunnelRequest, OpenTunnelResult, OpenTunnelStatus, WirePacket,
    wire_packet,
};

const CONTROL_TIME_SKEW_MS: i64 = 60_000;

pub(super) struct ControlState {
    inbound_sequence: u64,
    outbound_sequence: u64,
}

struct PolicyDecision {
    status: OpenTunnelStatus,
    stable_error_code: String,
    negotiated_capabilities: Vec<String>,
}

impl ControlState {
    pub fn new() -> Self {
        Self {
            inbound_sequence: 0,
            outbound_sequence: 0,
        }
    }

    pub fn handle(
        &mut self,
        packet: WirePacket,
        endpoint_id: &[u8; 16],
        online_key: &VerifyingKey,
        identity: &EndpointIdentity,
        config: &AgentConfig,
        now: SystemTime,
    ) -> Result<Vec<u8>, GatewayError> {
        if packet.wire_major != 1 || packet.wire_minor != 0 || packet.packet_id.len() != 16 {
            return Err(GatewayError::Protocol);
        }
        let control_frame = match packet.body {
            Some(wire_packet::Body::Control(value)) => value,
            _ => return Err(GatewayError::Protocol),
        };
        if control_frame.r#type != ControlType::OpenTunnelRequest as i32
            || control_frame.message_id.len() != 16
            || control_frame.sender_endpoint_id != [0_u8; 16]
            || control_frame.receiver_endpoint_id != endpoint_id
            || control_frame.control_sequence != self.inbound_sequence + 1
            || control_frame.payload.is_empty()
            || control_frame.signature.len() != 64
        {
            return Err(GatewayError::Protocol);
        }
        let now_ms = unix_millis(now)?;
        if control_frame.created_at_ms < now_ms - CONTROL_TIME_SKEW_MS
            || control_frame.created_at_ms > now_ms + CONTROL_TIME_SKEW_MS
        {
            return Err(GatewayError::Protocol);
        }
        let transcript = control(ControlInput {
            message_id: &control_frame.message_id,
            sender_endpoint_id: &control_frame.sender_endpoint_id,
            receiver_endpoint_id: &control_frame.receiver_endpoint_id,
            sequence: control_frame.control_sequence,
            created_at_ms: control_frame.created_at_ms,
            control_type: u32::try_from(control_frame.r#type)
                .map_err(|_| GatewayError::Protocol)?,
            payload: &control_frame.payload,
        })?;
        if !verify_p1363_low_s(online_key, &transcript, &control_frame.signature) {
            return Err(GatewayError::Trust);
        }
        let request = OpenTunnelRequest::decode(control_frame.payload.as_slice())
            .map_err(|_| GatewayError::Protocol)?;
        validate_open_request(&request, now_ms)?;
        let decision = evaluate_policy(config, &request);
        self.inbound_sequence = control_frame.control_sequence;
        self.outbound_sequence = self
            .outbound_sequence
            .checked_add(1)
            .ok_or(GatewayError::Protocol)?;
        let payload = OpenTunnelResult {
            session_id: request.session_id,
            status: decision.status as i32,
            stable_error_code: decision.stable_error_code,
            accepted_authorization_revision: if decision.status == OpenTunnelStatus::Accepted {
                request.authorization_revision
            } else {
                0
            },
            active_key_generation: 0,
            negotiated_capability_hints: decision.negotiated_capabilities,
        }
        .encode_to_vec();
        let message_id = random_array::<16>();
        let created_at_ms = unix_millis(SystemTime::now())?;
        let transcript = control(ControlInput {
            message_id: &message_id,
            sender_endpoint_id: endpoint_id,
            receiver_endpoint_id: request.hc_endpoint_id.as_slice(),
            sequence: self.outbound_sequence,
            created_at_ms,
            control_type: ControlType::OpenTunnelResult as u32,
            payload: &payload,
        })?;
        let signature = sign_p1363_low_s(identity.signing_key(), &transcript);
        let result = WirePacket {
            wire_major: 1,
            wire_minor: 0,
            packet_id: random_array::<16>().to_vec(),
            body: Some(wire_packet::Body::Control(ControlFrame {
                message_id: message_id.to_vec(),
                sender_endpoint_id: endpoint_id.to_vec(),
                receiver_endpoint_id: request.hc_endpoint_id,
                control_sequence: self.outbound_sequence,
                created_at_ms,
                r#type: ControlType::OpenTunnelResult as i32,
                payload,
                signature: signature.to_vec(),
            })),
        };
        Ok(result.encode_to_vec())
    }
}

fn validate_open_request(request: &OpenTunnelRequest, now_ms: i64) -> Result<(), GatewayError> {
    if request.session_id.len() != 16
        || request.session_id.iter().all(|value| *value == 0)
        || request.hc_endpoint_id.len() != 16
        || request.hc_endpoint_id.iter().all(|value| *value == 0)
        || request.authorization_revision != 1
        || request.requested_key_generation != 1
        || request.expires_at_ms <= now_ms
        || request.expires_at_ms > now_ms + CONTROL_TIME_SKEW_MS
        || request.requested_acp_capabilities.is_empty()
        || request.requested_acp_capabilities.len() > 8
    {
        return Err(GatewayError::Protocol);
    }
    let mut capabilities = request.requested_acp_capabilities.clone();
    capabilities.sort();
    capabilities.dedup();
    if capabilities.len() != request.requested_acp_capabilities.len()
        || capabilities.iter().any(|value| {
            !matches!(
                value.as_str(),
                "prompt" | "permission" | "cancel" | "session"
            )
        })
    {
        return Err(GatewayError::Protocol);
    }
    Ok(())
}

fn evaluate_policy(config: &AgentConfig, request: &OpenTunnelRequest) -> PolicyDecision {
    let Some(runtime) = config
        .runtimes
        .iter()
        .find(|runtime| runtime.id == request.runtime_profile_id)
    else {
        return rejected("RUNTIME_NOT_ALLOWED");
    };
    let Some(workspace) = config
        .workspaces
        .iter()
        .find(|workspace| workspace.id == request.workspace_id)
    else {
        return rejected("WORKSPACE_NOT_ALLOWED");
    };
    if !workspace.allowed_runtimes.contains(&runtime.id)
        || !safe_local_file(&runtime.command)
        || !safe_local_directory(&workspace.path)
    {
        return rejected("LOCAL_POLICY_REJECTED");
    }
    PolicyDecision {
        status: OpenTunnelStatus::Accepted,
        stable_error_code: String::new(),
        negotiated_capabilities: request.requested_acp_capabilities.clone(),
    }
}

fn rejected(code: &str) -> PolicyDecision {
    PolicyDecision {
        status: OpenTunnelStatus::Rejected,
        stable_error_code: code.to_owned(),
        negotiated_capabilities: Vec::new(),
    }
}

fn safe_local_file(path: &std::path::Path) -> bool {
    let Ok(metadata) = fs::symlink_metadata(path) else {
        return false;
    };
    if !metadata.is_file() || metadata.file_type().is_symlink() {
        return false;
    }
    #[cfg(unix)]
    if metadata.permissions().mode() & 0o111 == 0 {
        return false;
    }
    fs::canonicalize(path).is_ok_and(|canonical| canonical == path)
}

fn safe_local_directory(path: &std::path::Path) -> bool {
    let Ok(metadata) = fs::symlink_metadata(path) else {
        return false;
    };
    metadata.is_dir()
        && !metadata.file_type().is_symlink()
        && fs::canonicalize(path).is_ok_and(|canonical| canonical == path)
}

fn unix_millis(now: SystemTime) -> Result<i64, GatewayError> {
    let value = now
        .duration_since(UNIX_EPOCH)
        .map_err(|_| GatewayError::Protocol)?
        .as_millis();
    i64::try_from(value).map_err(|_| GatewayError::Protocol)
}

fn random_array<const N: usize>() -> [u8; N] {
    use rand_core::{OsRng, RngCore as _};
    let mut value = [0_u8; N];
    OsRng.fill_bytes(&mut value);
    value
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::{
        CONFIG_SCHEMA_VERSION, Limits, PlatformConfig, RuntimeProfile, WorkspaceProfile,
    };
    use crate::identity::DevFileKeyStore;
    use p256::ecdsa::SigningKey;
    use url::Url;

    #[test]
    fn verifies_platform_control_and_returns_signed_policy_result()
    -> Result<(), Box<dyn std::error::Error>> {
        let directory = tempfile::tempdir()?;
        let command = directory.path().join("test-agent");
        fs::write(&command, b"test")?;
        #[cfg(unix)]
        fs::set_permissions(&command, fs::Permissions::from_mode(0o700))?;
        let workspace = directory.path().join("workspace");
        fs::create_dir(&workspace)?;
        let platform = Url::parse("http://127.0.0.1:8082")?;
        let config = AgentConfig {
            schema_version: CONFIG_SCHEMA_VERSION,
            platform: PlatformConfig {
                url: platform.clone(),
            },
            limits: Limits::default(),
            runtimes: vec![RuntimeProfile {
                id: "test-agent".to_owned(),
                display_name: "Test Agent".to_owned(),
                command,
                args: Vec::new(),
                env_allow: Vec::new(),
                max_sessions: None,
            }],
            workspaces: vec![WorkspaceProfile {
                id: "fixture".to_owned(),
                display_name: "Fixture".to_owned(),
                path: workspace,
                allowed_runtimes: vec!["test-agent".to_owned()],
                follow_symlinks: false,
            }],
        };
        let store = DevFileKeyStore::new(directory.path().join("identity.json"), &platform, true)?;
        store.initialize()?;
        let identity = store.load()?;
        let online = SigningKey::from_slice(&[7_u8; 32])?;
        let endpoint_id = [2_u8; 16];
        let hc_endpoint_id = [3_u8; 16];
        let now = UNIX_EPOCH + std::time::Duration::from_secs(1_800_000_000);
        let request = OpenTunnelRequest {
            session_id: vec![4_u8; 16],
            hc_endpoint_id: hc_endpoint_id.to_vec(),
            runtime_profile_id: "test-agent".to_owned(),
            workspace_id: "fixture".to_owned(),
            authorization_revision: 1,
            requested_key_generation: 1,
            requested_acp_capabilities: vec!["prompt".to_owned(), "session".to_owned()],
            expires_at_ms: unix_millis(now)? + 30_000,
        };
        let packet = platform_open_packet(&online, &endpoint_id, request, unix_millis(now)?, 1)?;
        let mut state = ControlState::new();
        let encoded = state.handle(
            packet,
            &endpoint_id,
            online.verifying_key(),
            &identity,
            &config,
            now,
        )?;
        let response = WirePacket::decode(encoded.as_slice())?;
        let frame = match response.body {
            Some(wire_packet::Body::Control(value)) => value,
            _ => return Err("response is not a control frame".into()),
        };
        assert_eq!(frame.r#type, ControlType::OpenTunnelResult as i32);
        assert_eq!(frame.sender_endpoint_id, endpoint_id);
        assert_eq!(frame.receiver_endpoint_id, hc_endpoint_id);
        assert_eq!(frame.control_sequence, 1);
        let result = OpenTunnelResult::decode(frame.payload.as_slice())?;
        assert_eq!(result.status, OpenTunnelStatus::Accepted as i32);
        assert_eq!(result.accepted_authorization_revision, 1);
        assert!(result.stable_error_code.is_empty());
        let transcript = control(ControlInput {
            message_id: &frame.message_id,
            sender_endpoint_id: &frame.sender_endpoint_id,
            receiver_endpoint_id: &frame.receiver_endpoint_id,
            sequence: frame.control_sequence,
            created_at_ms: frame.created_at_ms,
            control_type: u32::try_from(frame.r#type)?,
            payload: &frame.payload,
        })?;
        assert!(verify_p1363_low_s(
            &identity.signing_public_jwk()?.verifying_key()?,
            &transcript,
            &frame.signature,
        ));
        Ok(())
    }

    #[test]
    fn rejects_tampered_platform_control() -> Result<(), Box<dyn std::error::Error>> {
        let platform = Url::parse("http://127.0.0.1:8082")?;
        let directory = tempfile::tempdir()?;
        let store = DevFileKeyStore::new(directory.path().join("identity.json"), &platform, true)?;
        store.initialize()?;
        let identity = store.load()?;
        let online = SigningKey::from_slice(&[8_u8; 32])?;
        let endpoint_id = [5_u8; 16];
        let now = SystemTime::now();
        let request = OpenTunnelRequest {
            session_id: vec![6_u8; 16],
            hc_endpoint_id: vec![7_u8; 16],
            runtime_profile_id: "missing".to_owned(),
            workspace_id: "missing".to_owned(),
            authorization_revision: 1,
            requested_key_generation: 1,
            requested_acp_capabilities: vec!["prompt".to_owned()],
            expires_at_ms: unix_millis(now)? + 30_000,
        };
        let mut packet =
            platform_open_packet(&online, &endpoint_id, request, unix_millis(now)?, 1)?;
        let Some(wire_packet::Body::Control(frame)) = packet.body.as_mut() else {
            return Err("test packet is not a control frame".into());
        };
        frame.signature[0] ^= 1;
        let config = AgentConfig {
            schema_version: CONFIG_SCHEMA_VERSION,
            platform: PlatformConfig { url: platform },
            limits: Limits::default(),
            runtimes: Vec::new(),
            workspaces: Vec::new(),
        };
        assert!(matches!(
            ControlState::new().handle(
                packet,
                &endpoint_id,
                online.verifying_key(),
                &identity,
                &config,
                now,
            ),
            Err(GatewayError::Trust)
        ));
        Ok(())
    }

    #[test]
    fn returns_stable_rejection_for_unknown_local_profile() -> Result<(), Box<dyn std::error::Error>>
    {
        let platform = Url::parse("http://127.0.0.1:8082")?;
        let directory = tempfile::tempdir()?;
        let store = DevFileKeyStore::new(directory.path().join("identity.json"), &platform, true)?;
        store.initialize()?;
        let identity = store.load()?;
        let online = SigningKey::from_slice(&[11_u8; 32])?;
        let endpoint_id = [12_u8; 16];
        let now = SystemTime::now();
        let request = OpenTunnelRequest {
            session_id: vec![13_u8; 16],
            hc_endpoint_id: vec![14_u8; 16],
            runtime_profile_id: "missing".to_owned(),
            workspace_id: "missing".to_owned(),
            authorization_revision: 1,
            requested_key_generation: 1,
            requested_acp_capabilities: vec!["prompt".to_owned()],
            expires_at_ms: unix_millis(now)? + 30_000,
        };
        let packet = platform_open_packet(&online, &endpoint_id, request, unix_millis(now)?, 1)?;
        let config = AgentConfig {
            schema_version: CONFIG_SCHEMA_VERSION,
            platform: PlatformConfig { url: platform },
            limits: Limits::default(),
            runtimes: Vec::new(),
            workspaces: Vec::new(),
        };
        let encoded = ControlState::new().handle(
            packet,
            &endpoint_id,
            online.verifying_key(),
            &identity,
            &config,
            now,
        )?;
        let response = WirePacket::decode(encoded.as_slice())?;
        let frame = match response.body {
            Some(wire_packet::Body::Control(value)) => value,
            _ => return Err("response is not a control frame".into()),
        };
        let result = OpenTunnelResult::decode(frame.payload.as_slice())?;
        assert_eq!(result.status, OpenTunnelStatus::Rejected as i32);
        assert_eq!(result.stable_error_code, "RUNTIME_NOT_ALLOWED");
        assert_eq!(result.accepted_authorization_revision, 0);
        assert!(result.negotiated_capability_hints.is_empty());
        Ok(())
    }

    fn platform_open_packet(
        online: &SigningKey,
        endpoint_id: &[u8; 16],
        request: OpenTunnelRequest,
        created_at_ms: i64,
        sequence: u64,
    ) -> Result<WirePacket, GatewayError> {
        let payload = request.encode_to_vec();
        let message_id = [9_u8; 16];
        let sender = [0_u8; 16];
        let transcript = control(ControlInput {
            message_id: &message_id,
            sender_endpoint_id: &sender,
            receiver_endpoint_id: endpoint_id,
            sequence,
            created_at_ms,
            control_type: ControlType::OpenTunnelRequest as u32,
            payload: &payload,
        })?;
        Ok(WirePacket {
            wire_major: 1,
            wire_minor: 0,
            packet_id: vec![10_u8; 16],
            body: Some(wire_packet::Body::Control(ControlFrame {
                message_id: message_id.to_vec(),
                sender_endpoint_id: sender.to_vec(),
                receiver_endpoint_id: endpoint_id.to_vec(),
                control_sequence: sequence,
                created_at_ms,
                r#type: ControlType::OpenTunnelRequest as i32,
                payload,
                signature: sign_p1363_low_s(online, &transcript).to_vec(),
            })),
        })
    }
}
