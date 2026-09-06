use super::GatewayError;

const PROTOCOL: &str = "mss.awp.v1";

pub(super) struct ServerChallengeInput<'a> {
    pub connection_id: &'a [u8],
    pub connection_generation: u64,
    pub server_nonce: &'a [u8],
    pub server_time_ms: i64,
    pub manifest_revision: u64,
    pub credential_revision: u64,
    pub endpoint_id: &'a [u8],
}

pub(super) fn server_challenge(input: ServerChallengeInput<'_>) -> Result<Vec<u8>, GatewayError> {
    if input.connection_id.len() != 16
        || input.connection_generation == 0
        || input.server_nonce.len() != 32
        || input.endpoint_id.len() != 16
    {
        return Err(GatewayError::Protocol);
    }
    let mut output = b"mss-awp-server-challenge-v1".to_vec();
    output.extend_from_slice(input.connection_id);
    output.extend_from_slice(&input.connection_generation.to_be_bytes());
    output.extend_from_slice(input.server_nonce);
    output.extend_from_slice(&input.server_time_ms.to_be_bytes());
    output.extend_from_slice(&input.manifest_revision.to_be_bytes());
    output.extend_from_slice(&input.credential_revision.to_be_bytes());
    output.extend_from_slice(input.endpoint_id);
    Ok(output)
}

pub(super) struct ClientChallengeInput<'a> {
    pub connection_id: &'a [u8],
    pub connection_generation: u64,
    pub server_nonce: &'a [u8],
    pub client_nonce: &'a [u8],
    pub endpoint_id: &'a [u8],
    pub credential_id: &'a [u8],
    pub manifest_revision: u64,
    pub credential_revision: u64,
}

pub(super) fn client_challenge(input: ClientChallengeInput<'_>) -> Result<Vec<u8>, GatewayError> {
    if input.connection_id.len() != 16
        || input.connection_generation == 0
        || input.server_nonce.len() != 32
        || input.client_nonce.len() != 32
        || input.endpoint_id.len() != 16
        || input.credential_id.len() != 16
    {
        return Err(GatewayError::Protocol);
    }
    let mut output = b"mss-awp-client-challenge-v1".to_vec();
    output.extend_from_slice(input.connection_id);
    output.extend_from_slice(&input.connection_generation.to_be_bytes());
    output.extend_from_slice(input.server_nonce);
    output.extend_from_slice(input.client_nonce);
    output.extend_from_slice(input.endpoint_id);
    output.extend_from_slice(input.credential_id);
    output.extend_from_slice(PROTOCOL.as_bytes());
    output.extend_from_slice(&input.manifest_revision.to_be_bytes());
    output.extend_from_slice(&input.credential_revision.to_be_bytes());
    Ok(output)
}

pub(super) struct ConnectionReadyInput<'a> {
    pub connection_id: &'a [u8],
    pub connection_generation: u64,
    pub fencing_token: &'a [u8],
    pub ready_at_ms: i64,
    pub max_packet_bytes: u32,
    pub max_inflight_frames: u32,
    pub heartbeat_interval_ms: u32,
    pub endpoint_id: &'a [u8],
}

pub(super) fn connection_ready(input: ConnectionReadyInput<'_>) -> Result<Vec<u8>, GatewayError> {
    if input.connection_id.len() != 16
        || input.connection_generation == 0
        || input.fencing_token.len() != 32
        || input.max_packet_bytes == 0
        || input.max_inflight_frames == 0
        || input.heartbeat_interval_ms == 0
        || input.endpoint_id.len() != 16
    {
        return Err(GatewayError::Protocol);
    }
    let mut output = b"mss-awp-connection-ready-v1".to_vec();
    output.extend_from_slice(input.connection_id);
    output.extend_from_slice(&input.connection_generation.to_be_bytes());
    output.extend_from_slice(input.fencing_token);
    output.extend_from_slice(&input.ready_at_ms.to_be_bytes());
    output.extend_from_slice(&input.max_packet_bytes.to_be_bytes());
    output.extend_from_slice(&input.max_inflight_frames.to_be_bytes());
    output.extend_from_slice(&input.heartbeat_interval_ms.to_be_bytes());
    output.extend_from_slice(input.endpoint_id);
    Ok(output)
}

pub(super) struct ControlInput<'a> {
    pub message_id: &'a [u8],
    pub sender_endpoint_id: &'a [u8],
    pub receiver_endpoint_id: &'a [u8],
    pub sequence: u64,
    pub created_at_ms: i64,
    pub control_type: u32,
    pub payload: &'a [u8],
}

pub(super) fn control(input: ControlInput<'_>) -> Result<Vec<u8>, GatewayError> {
    if input.message_id.len() != 16
        || input.sender_endpoint_id.len() != 16
        || input.receiver_endpoint_id.len() != 16
        || input.sequence == 0
        || input.control_type == 0
        || input.payload.is_empty()
        || input.payload.len() > 1 << 20
    {
        return Err(GatewayError::Protocol);
    }
    let payload_length = u32::try_from(input.payload.len()).map_err(|_| GatewayError::Protocol)?;
    let mut output = b"mss-awp-control-v1".to_vec();
    output.extend_from_slice(input.message_id);
    output.extend_from_slice(input.sender_endpoint_id);
    output.extend_from_slice(input.receiver_endpoint_id);
    output.extend_from_slice(&input.sequence.to_be_bytes());
    output.extend_from_slice(&input.created_at_ms.to_be_bytes());
    output.extend_from_slice(&input.control_type.to_be_bytes());
    output.extend_from_slice(&payload_length.to_be_bytes());
    output.extend_from_slice(input.payload);
    Ok(output)
}
