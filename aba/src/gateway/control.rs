use std::collections::HashMap;
use std::fs;
#[cfg(unix)]
use std::os::unix::fs::PermissionsExt as _;
use std::time::{SystemTime, UNIX_EPOCH};

use p256::ecdsa::VerifyingKey;
use prost::Message as _;

use super::GatewayError;
use super::transcript::{ControlInput, control};
use crate::config::AgentConfig;
use crate::crypto::ack::{sign_ack, verify_ack};
use crate::crypto::frame::{frame_content_hash, open_frame, seal_frame, session_channel_id};
use crate::crypto::key_package::{
    KeyPackageEnvelope, KeyPackageMaterial, SUITE_NAME, key_package_envelope_transcript,
    p256_public_jwk_from_sec1, seal_key_package,
};
use crate::crypto::{sign_p1363_low_s, verify_p1363_low_s};
use crate::identity::EndpointIdentity;
use crate::journal::{
    ChannelKey, InboundFrame, InboundState, IntakeOutcome, Journal, OutboundFrame,
};
use crate::process::AgentProcess;
use crate::protocol::awpv1::{
    AckFrame, CloseTunnelRequest, CloseTunnelResult, CloseTunnelStatus, ControlFrame, ControlType,
    Direction as WireDirection, EncryptedFrame, FrameType as WireFrameType, OpenTunnelRequest,
    OpenTunnelResult, OpenTunnelStatus, SessionKeyPackage, SessionKeyPackageAck, WirePacket,
    wire_packet,
};
use crate::wire::{CryptoSuite, Direction, FrameAadV1, FrameType};

const CONTROL_TIME_SKEW_MS: i64 = 60_000;

pub(super) struct ControlState {
    platform_inbound_sequence: u64,
    hc_inbound_sequences: HashMap<[u8; 16], u64>,
    outbound_sequence: u64,
    sessions: HashMap<[u8; 16], LocalSession>,
    journal: Journal,
}

struct LocalSession {
    agent: AgentProcess,
    material: KeyPackageMaterial,
    hc_signing_key: VerifyingKey,
    key_package_id: [u8; 16],
    active: bool,
    hc_frame_sequence: u64,
    aba_frame_sequence: u64,
}

pub(super) struct ControlContext<'a> {
    pub endpoint_id: &'a [u8; 16],
    pub online_key: &'a VerifyingKey,
    pub identity: &'a EndpointIdentity,
    pub config: &'a AgentConfig,
    pub credential_id: &'a [u8; 16],
    pub now: SystemTime,
}

struct PolicyDecision {
    status: OpenTunnelStatus,
    stable_error_code: String,
    negotiated_capabilities: Vec<String>,
}

impl ControlState {
    pub fn new(journal: Journal) -> Self {
        Self {
            platform_inbound_sequence: 0,
            hc_inbound_sequences: HashMap::new(),
            outbound_sequence: 0,
            sessions: HashMap::new(),
            journal,
        }
    }

    pub fn handle(
        &mut self,
        packet: WirePacket,
        context: ControlContext<'_>,
    ) -> Result<Vec<Vec<u8>>, GatewayError> {
        let ControlContext {
            endpoint_id,
            online_key,
            identity,
            config,
            credential_id,
            now,
        } = context;
        if packet.wire_major != 1 || packet.wire_minor != 0 || packet.packet_id.len() != 16 {
            return Err(GatewayError::Protocol);
        }
        let control_frame = match packet.body {
            Some(wire_packet::Body::Control(value)) => value,
            Some(wire_packet::Body::Encrypted(value)) => {
                return self.handle_encrypted_frame(value, endpoint_id, identity, now);
            }
            Some(wire_packet::Body::Ack(value)) => {
                return self.handle_ack_frame(value, endpoint_id, now);
            }
            _ => return Err(GatewayError::Protocol),
        };
        if control_frame.r#type == ControlType::SessionKeyPackageAck as i32 {
            return self.handle_key_package_ack(control_frame, endpoint_id, now);
        }
        if control_frame.r#type == ControlType::CloseTunnelRequest as i32 {
            return self.handle_close_tunnel(control_frame, endpoint_id, online_key, identity, now);
        }
        if control_frame.r#type != ControlType::OpenTunnelRequest as i32
            || control_frame.message_id.len() != 16
            || control_frame.sender_endpoint_id != [0_u8; 16]
            || control_frame.receiver_endpoint_id != endpoint_id
            || control_frame.control_sequence != self.platform_inbound_sequence + 1
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
        let mut decision = if self.sessions.len() >= usize::from(config.limits.max_sessions) {
            PolicyDecision {
                status: OpenTunnelStatus::ResourceBusy,
                stable_error_code: "RESOURCE_BUSY".to_owned(),
                negotiated_capabilities: Vec::new(),
            }
        } else {
            evaluate_policy(config, &request)
        };
        let mut agent = None;
        if decision.status == OpenTunnelStatus::Accepted {
            match local_profiles(config, &request)
                .and_then(|(runtime, workspace)| AgentProcess::start(runtime, workspace).ok())
            {
                Some(process) => agent = Some(process),
                None => decision = rejected("AGENT_START_FAILED"),
            }
        }
        self.platform_inbound_sequence = control_frame.control_sequence;
        let payload = OpenTunnelResult {
            session_id: request.session_id.clone(),
            status: decision.status as i32,
            stable_error_code: decision.stable_error_code.clone(),
            accepted_authorization_revision: if decision.status == OpenTunnelStatus::Accepted {
                request.authorization_revision
            } else {
                0
            },
            active_key_generation: 0,
            negotiated_capability_hints: decision.negotiated_capabilities.clone(),
        }
        .encode_to_vec();
        let mut responses = vec![self.signed_control(
            endpoint_id,
            request.hc_endpoint_id.as_slice(),
            ControlType::OpenTunnelResult,
            payload,
            identity,
            now_ms,
        )?];
        if decision.status != OpenTunnelStatus::Accepted {
            return Ok(responses);
        }
        let agent = agent.ok_or(GatewayError::Protocol)?;

        let session_id: [u8; 16] = request
            .session_id
            .as_slice()
            .try_into()
            .map_err(|_| GatewayError::Protocol)?;
        let recipient = p256_public_jwk_from_sec1(&request.hc_kem_public_key, &request.hc_kem_jkt)?;
        let mut hc_to_aba_prefix = random_array::<4>();
        let aba_to_hc_prefix = random_array::<4>();
        if hc_to_aba_prefix == aba_to_hc_prefix {
            hc_to_aba_prefix[0] ^= 1;
        }
        let expires_at_ms = now_ms
            .checked_add(3_600_000)
            .ok_or(GatewayError::Protocol)?;
        let material = KeyPackageMaterial {
            session_id,
            generation: request.requested_key_generation,
            sender_aba_endpoint_id: *endpoint_id,
            recipient_hc_endpoint_id: request
                .hc_endpoint_id
                .as_slice()
                .try_into()
                .map_err(|_| GatewayError::Protocol)?,
            policy_revision: request.authorization_revision,
            key_id: random_array::<16>(),
            srk: random_array::<32>(),
            session_nonce: random_array::<32>(),
            hc_to_aba_nonce_prefix: hc_to_aba_prefix,
            aba_to_hc_nonce_prefix: aba_to_hc_prefix,
            not_before_ms: now_ms,
            expires_at_ms,
        };
        let sealed = seal_key_package(&recipient, &material)?;
        let key_package_id = random_array::<16>();
        let envelope = key_package_envelope_transcript(KeyPackageEnvelope {
            key_package_id: &key_package_id,
            session_id: &session_id,
            generation: material.generation,
            issuer_aba_endpoint_id: endpoint_id,
            recipient_hc_endpoint_id: &material.recipient_hc_endpoint_id,
            issuer_credential_id: credential_id,
            policy_revision: material.policy_revision,
            not_before_ms: material.not_before_ms,
            expires_at_ms: material.expires_at_ms,
            hpke_enc: &sealed.enc,
            hpke_ciphertext: &sealed.ciphertext,
        })?;
        let package = SessionKeyPackage {
            session_id: session_id.to_vec(),
            key_generation: material.generation,
            issuer_aba_endpoint_id: endpoint_id.to_vec(),
            recipient_hc_endpoint_id: material.recipient_hc_endpoint_id.to_vec(),
            crypto_suite: SUITE_NAME.to_owned(),
            hpke_enc: sealed.enc,
            hpke_ciphertext: sealed.ciphertext,
            not_before_ms: material.not_before_ms,
            expires_at_ms: material.expires_at_ms,
            issuer_signature: sign_p1363_low_s(identity.signing_key(), &envelope).to_vec(),
            key_package_id: key_package_id.to_vec(),
            issuer_credential_id: credential_id.to_vec(),
            policy_revision: material.policy_revision,
        }
        .encode_to_vec();
        responses.push(self.signed_control(
            endpoint_id,
            &material.recipient_hc_endpoint_id,
            ControlType::SessionKeyPackage,
            package,
            identity,
            now_ms,
        )?);
        let hc_signing_jwk =
            p256_public_jwk_from_sec1(&request.hc_signing_public_key, &request.hc_signing_jkt)?;
        self.sessions.insert(
            session_id,
            LocalSession {
                agent,
                material,
                hc_signing_key: hc_signing_jwk.verifying_key()?,
                key_package_id,
                active: false,
                hc_frame_sequence: 0,
                aba_frame_sequence: 0,
            },
        );
        Ok(responses)
    }

    fn signed_control(
        &mut self,
        endpoint_id: &[u8; 16],
        receiver_endpoint_id: &[u8],
        control_type: ControlType,
        payload: Vec<u8>,
        identity: &EndpointIdentity,
        created_at_ms: i64,
    ) -> Result<Vec<u8>, GatewayError> {
        self.outbound_sequence = self
            .outbound_sequence
            .checked_add(1)
            .ok_or(GatewayError::Protocol)?;
        let message_id = random_array::<16>();
        let transcript = control(ControlInput {
            message_id: &message_id,
            sender_endpoint_id: endpoint_id,
            receiver_endpoint_id,
            sequence: self.outbound_sequence,
            created_at_ms,
            control_type: control_type as u32,
            payload: &payload,
        })?;
        let packet = WirePacket {
            wire_major: 1,
            wire_minor: 0,
            packet_id: random_array::<16>().to_vec(),
            body: Some(wire_packet::Body::Control(ControlFrame {
                message_id: message_id.to_vec(),
                sender_endpoint_id: endpoint_id.to_vec(),
                receiver_endpoint_id: receiver_endpoint_id.to_vec(),
                control_sequence: self.outbound_sequence,
                created_at_ms,
                r#type: control_type as i32,
                payload,
                signature: sign_p1363_low_s(identity.signing_key(), &transcript).to_vec(),
            })),
        };
        Ok(packet.encode_to_vec())
    }

    fn handle_close_tunnel(
        &mut self,
        control_frame: ControlFrame,
        endpoint_id: &[u8; 16],
        online_key: &VerifyingKey,
        identity: &EndpointIdentity,
        now: SystemTime,
    ) -> Result<Vec<Vec<u8>>, GatewayError> {
        let expected_sequence = self
            .platform_inbound_sequence
            .checked_add(1)
            .ok_or(GatewayError::Protocol)?;
        let now_ms = unix_millis(now)?;
        if control_frame.message_id.len() != 16
            || control_frame.sender_endpoint_id != [0; 16]
            || control_frame.receiver_endpoint_id != endpoint_id
            || control_frame.control_sequence != expected_sequence
            || control_frame.payload.is_empty()
            || control_frame.signature.len() != 64
            || control_frame.created_at_ms < now_ms - CONTROL_TIME_SKEW_MS
            || control_frame.created_at_ms > now_ms + CONTROL_TIME_SKEW_MS
        {
            return Err(GatewayError::ProtocolStage("CloseTunnel control"));
        }
        let transcript = control(ControlInput {
            message_id: &control_frame.message_id,
            sender_endpoint_id: &control_frame.sender_endpoint_id,
            receiver_endpoint_id: &control_frame.receiver_endpoint_id,
            sequence: control_frame.control_sequence,
            created_at_ms: control_frame.created_at_ms,
            control_type: ControlType::CloseTunnelRequest as u32,
            payload: &control_frame.payload,
        })?;
        if !verify_p1363_low_s(online_key, &transcript, &control_frame.signature) {
            return Err(GatewayError::Trust);
        }
        let request = CloseTunnelRequest::decode(control_frame.payload.as_slice())
            .map_err(|_| GatewayError::Protocol)?;
        if request.session_id.len() != 16
            || request.session_id.iter().all(|value| *value == 0)
            || !stable_reason_code(&request.stable_reason_code)
            || request.expires_at_ms <= now_ms
            || request.expires_at_ms > now_ms + CONTROL_TIME_SKEW_MS
        {
            return Err(GatewayError::ProtocolStage("CloseTunnel payload"));
        }
        self.platform_inbound_sequence = control_frame.control_sequence;
        let session_id: [u8; 16] = request
            .session_id
            .as_slice()
            .try_into()
            .map_err(|_| GatewayError::Protocol)?;
        let Some(session) = self.sessions.remove(&session_id) else {
            self.journal.close_session(session_id)?;
            return Ok(Vec::new());
        };
        let receiver = session.material.recipient_hc_endpoint_id;
        drop(session);
        self.journal.close_session(session_id)?;
        let payload = CloseTunnelResult {
            session_id: session_id.to_vec(),
            status: CloseTunnelStatus::Accepted as i32,
            stable_error_code: String::new(),
        }
        .encode_to_vec();
        Ok(vec![self.signed_control(
            endpoint_id,
            &receiver,
            ControlType::CloseTunnelResult,
            payload,
            identity,
            now_ms,
        )?])
    }

    fn handle_key_package_ack(
        &mut self,
        control_frame: ControlFrame,
        endpoint_id: &[u8; 16],
        now: SystemTime,
    ) -> Result<Vec<Vec<u8>>, GatewayError> {
        let sender_id: [u8; 16] = control_frame
            .sender_endpoint_id
            .as_slice()
            .try_into()
            .map_err(|_| GatewayError::Protocol)?;
        let sequence_is_contiguous = self
            .hc_inbound_sequences
            .get(&sender_id)
            .map_or(control_frame.control_sequence > 0, |previous| {
                previous.checked_add(1) == Some(control_frame.control_sequence)
            });
        let now_ms = unix_millis(now)?;
        if control_frame.message_id.len() != 16
            || sender_id.iter().all(|value| *value == 0)
            || control_frame.receiver_endpoint_id != endpoint_id
            || !sequence_is_contiguous
            || control_frame.payload.is_empty()
            || control_frame.signature.len() != 64
            || control_frame.created_at_ms < now_ms - CONTROL_TIME_SKEW_MS
            || control_frame.created_at_ms > now_ms + CONTROL_TIME_SKEW_MS
        {
            return Err(GatewayError::Protocol);
        }
        let acknowledgment = SessionKeyPackageAck::decode(control_frame.payload.as_slice())
            .map_err(|_| GatewayError::Protocol)?;
        let session_id: [u8; 16] = acknowledgment
            .session_id
            .as_slice()
            .try_into()
            .map_err(|_| GatewayError::Protocol)?;
        let session = self
            .sessions
            .get(&session_id)
            .ok_or(GatewayError::Protocol)?;
        if session.active
            || sender_id != session.material.recipient_hc_endpoint_id
            || acknowledgment.recipient_hc_endpoint_id != sender_id
            || acknowledgment.key_package_id != session.key_package_id
            || acknowledgment.key_generation != session.material.generation
            || acknowledgment.acknowledged_at_ms != control_frame.created_at_ms
        {
            return Err(GatewayError::Protocol);
        }
        let transcript = control(ControlInput {
            message_id: &control_frame.message_id,
            sender_endpoint_id: &control_frame.sender_endpoint_id,
            receiver_endpoint_id: &control_frame.receiver_endpoint_id,
            sequence: control_frame.control_sequence,
            created_at_ms: control_frame.created_at_ms,
            control_type: ControlType::SessionKeyPackageAck as u32,
            payload: &control_frame.payload,
        })?;
        if !verify_p1363_low_s(
            &session.hc_signing_key,
            &transcript,
            &control_frame.signature,
        ) {
            return Err(GatewayError::Trust);
        }
        self.hc_inbound_sequences
            .insert(sender_id, control_frame.control_sequence);
        self.sessions
            .get_mut(&session_id)
            .ok_or(GatewayError::Protocol)?
            .active = true;
        Ok(Vec::new())
    }

    fn handle_encrypted_frame(
        &mut self,
        frame: EncryptedFrame,
        endpoint_id: &[u8; 16],
        identity: &EndpointIdentity,
        now: SystemTime,
    ) -> Result<Vec<Vec<u8>>, GatewayError> {
        let journal = self.journal.clone();
        let session_id: [u8; 16] = frame
            .session_id
            .as_slice()
            .try_into()
            .map_err(|_| GatewayError::Protocol)?;
        let session = self
            .sessions
            .get_mut(&session_id)
            .ok_or(GatewayError::Protocol)?;
        let channel_id = session_channel_id(
            &session_id,
            endpoint_id,
            &session.material.recipient_hc_endpoint_id,
        )?;
        if !session.active {
            return Err(GatewayError::ProtocolStage("session is not active"));
        }
        if frame.crypto_suite_id != 1
            || frame.frame_type != WireFrameType::AcpTransportFrame as i32
            || frame.flags != 0
        {
            return Err(GatewayError::ProtocolStage("encrypted frame type"));
        }
        if frame.message_id.len() != 16
            || frame.channel_id != channel_id
            || frame.sender_endpoint_id != session.material.recipient_hc_endpoint_id
            || frame.receiver_endpoint_id != endpoint_id
            || frame.direction != WireDirection::HcToAba as i32
        {
            return Err(GatewayError::ProtocolStage("encrypted frame route"));
        }
        let next_sequence = session
            .hc_frame_sequence
            .checked_add(1)
            .ok_or(GatewayError::ProtocolStage("encrypted frame sequence"))?;
        if frame.sequence > next_sequence {
            return Err(GatewayError::ProtocolStage("encrypted frame sequence gap"));
        }
        if frame.key_generation != session.material.generation
            || frame.key_id != session.material.key_id
        {
            return Err(GatewayError::ProtocolStage("encrypted frame key"));
        }
        if frame.ciphertext.len() < 16
            || frame.ciphertext.len() > 1 << 20
            || frame.signature.len() != 64
        {
            return Err(GatewayError::ProtocolStage("encrypted frame bounds"));
        }
        let now_ms = unix_millis(now)?;
        if frame.created_at_ms < now_ms - 300_000 || frame.created_at_ms > now_ms + 300_000 {
            return Err(GatewayError::ProtocolStage("encrypted frame time"));
        }
        let aad = FrameAadV1 {
            crypto_suite: CryptoSuite::Suite0001,
            frame_type: FrameType::AcpTransportFrame,
            flags: frame.flags,
            message_id: frame
                .message_id
                .as_slice()
                .try_into()
                .map_err(|_| GatewayError::Protocol)?,
            channel_id,
            session_id,
            sender_endpoint_id: session.material.recipient_hc_endpoint_id,
            receiver_endpoint_id: *endpoint_id,
            direction: Direction::HcToAba,
            sequence: frame.sequence,
            key_generation: frame.key_generation,
            key_id: session.material.key_id,
            created_at_ms: frame.created_at_ms,
            ciphertext_length: u32::try_from(frame.ciphertext.len())
                .map_err(|_| GatewayError::Protocol)?,
        };
        let keys = crate::crypto::key_package::derive_session_direction_keys(&session.material)?;
        let plaintext = open_frame(
            &keys.hc_to_aba,
            &session.material.hc_to_aba_nonce_prefix,
            &session.hc_signing_key,
            &aad,
            &frame.ciphertext,
            &frame.signature,
        )?;
        let encoded_aad = aad.encode().map_err(|_| GatewayError::Protocol)?;
        let intake = journal.record_inbound(InboundFrame {
            message_id: aad.message_id,
            session_id,
            channel_id,
            direction: Direction::HcToAba as u8,
            sequence: frame.sequence,
            key_generation: frame.key_generation,
            content_hash: frame_content_hash(&encoded_aad, &frame.ciphertext, &frame.signature),
            updated_at_ms: now_ms,
        })?;
        if intake == IntakeOutcome::New {
            if frame.sequence != next_sequence {
                return Err(GatewayError::ProtocolStage(
                    "encrypted frame journal cursor",
                ));
            }
            session.hc_frame_sequence = frame.sequence;
        } else if frame.sequence > session.hc_frame_sequence {
            return Err(GatewayError::ProtocolStage("encrypted frame replay cursor"));
        }
        let acknowledgment = signed_frame_ack(session, endpoint_id, identity, channel_id, now_ms)?;
        match intake {
            IntakeOutcome::Duplicate(InboundState::Responded) => {
                let mut packets = vec![acknowledgment];
                packets.extend(journal.unacknowledged(session_id, Direction::AbaToHc as u8)?);
                return Ok(packets);
            }
            IntakeOutcome::Duplicate(InboundState::DispatchStarted | InboundState::Uncertain) => {
                return Ok(vec![acknowledgment]);
            }
            IntakeOutcome::New | IntakeOutcome::Duplicate(InboundState::Received) => {}
        }
        journal.start_dispatch(aad.message_id, now_ms)?;
        let responses = match session.agent.prompt(&plaintext, &lower_hex(&session_id)) {
            Ok(responses) => responses,
            Err(error) => {
                let _ = journal.mark_uncertain(aad.message_id, now_ms);
                return Err(GatewayError::from(error));
            }
        };
        let mut packets = Vec::with_capacity(responses.len() + 1);
        packets.push(acknowledgment);
        for response in responses {
            match seal_aba_frame(
                session,
                endpoint_id,
                identity,
                channel_id,
                &response,
                now_ms,
                &journal,
            ) {
                Ok(packet) => packets.push(packet),
                Err(error) => {
                    let _ = journal.mark_uncertain(aad.message_id, now_ms);
                    return Err(error);
                }
            }
        }
        journal.mark_responded(aad.message_id, now_ms)?;
        Ok(packets)
    }

    fn handle_ack_frame(
        &mut self,
        frame: AckFrame,
        endpoint_id: &[u8; 16],
        now: SystemTime,
    ) -> Result<Vec<Vec<u8>>, GatewayError> {
        let session_id: [u8; 16] = frame
            .session_id
            .as_slice()
            .try_into()
            .map_err(|_| GatewayError::Protocol)?;
        let session = self
            .sessions
            .get(&session_id)
            .ok_or(GatewayError::ProtocolStage("ACK session"))?;
        let channel_id = session_channel_id(
            &session_id,
            endpoint_id,
            &session.material.recipient_hc_endpoint_id,
        )?;
        let now_ms = unix_millis(now)?;
        let ranges = ack_ranges(&frame, session.aba_frame_sequence)?;
        if !session.active
            || frame.channel_id != channel_id
            || frame.endpoint_id != session.material.recipient_hc_endpoint_id
            || frame.acknowledged_direction != WireDirection::AbaToHc as i32
            || frame.key_generation != session.material.generation
            || frame.highest_contiguous_sequence > session.aba_frame_sequence
            || frame.created_at_ms < now_ms - 300_000
            || frame.created_at_ms > now_ms + 300_000
        {
            return Err(GatewayError::ProtocolStage("ACK binding"));
        }
        verify_ack(&frame, &session.hc_signing_key)?;
        self.journal.acknowledge_outbound(
            session_id,
            Direction::AbaToHc as u8,
            frame.key_generation,
            frame.highest_contiguous_sequence,
            &ranges,
        )?;
        Ok(Vec::new())
    }
}

fn seal_aba_frame(
    session: &mut LocalSession,
    endpoint_id: &[u8; 16],
    identity: &EndpointIdentity,
    channel_id: [u8; 16],
    plaintext: &[u8],
    now_ms: i64,
    journal: &Journal,
) -> Result<Vec<u8>, GatewayError> {
    let sequence = journal.reserve_outbound_sequence(ChannelKey {
        session_id: session.material.session_id,
        channel_id,
        direction: Direction::AbaToHc as u8,
        key_generation: session.material.generation,
    })?;
    if sequence <= session.aba_frame_sequence {
        return Err(GatewayError::ProtocolStage("outbound journal sequence"));
    }
    session.aba_frame_sequence = sequence;
    let message_id = random_array::<16>();
    let aad = FrameAadV1 {
        crypto_suite: CryptoSuite::Suite0001,
        frame_type: FrameType::AcpTransportFrame,
        flags: 0,
        message_id,
        channel_id,
        session_id: session.material.session_id,
        sender_endpoint_id: *endpoint_id,
        receiver_endpoint_id: session.material.recipient_hc_endpoint_id,
        direction: Direction::AbaToHc,
        sequence,
        key_generation: session.material.generation,
        key_id: session.material.key_id,
        created_at_ms: now_ms,
        ciphertext_length: 0,
    };
    let keys = crate::crypto::key_package::derive_session_direction_keys(&session.material)?;
    let protected = seal_frame(
        &keys.aba_to_hc,
        &session.material.aba_to_hc_nonce_prefix,
        identity.signing_key(),
        aad,
        plaintext,
    )?;
    let encoded = WirePacket {
        wire_major: 1,
        wire_minor: 0,
        packet_id: random_array::<16>().to_vec(),
        body: Some(wire_packet::Body::Encrypted(EncryptedFrame {
            crypto_suite_id: 1,
            frame_type: WireFrameType::AcpTransportFrame as i32,
            flags: 0,
            message_id: message_id.to_vec(),
            channel_id: channel_id.to_vec(),
            session_id: session.material.session_id.to_vec(),
            sender_endpoint_id: endpoint_id.to_vec(),
            receiver_endpoint_id: session.material.recipient_hc_endpoint_id.to_vec(),
            direction: WireDirection::AbaToHc as i32,
            sequence,
            key_generation: session.material.generation,
            key_id: session.material.key_id.to_vec(),
            created_at_ms: now_ms,
            ciphertext: protected.ciphertext,
            signature: protected.signature.to_vec(),
        })),
    }
    .encode_to_vec();
    journal.record_outbound(OutboundFrame {
        message_id,
        session_id: session.material.session_id,
        channel_id,
        direction: Direction::AbaToHc as u8,
        sequence,
        key_generation: session.material.generation,
        packet: encoded.clone(),
        created_at_ms: now_ms,
    })?;
    Ok(encoded)
}

fn signed_frame_ack(
    session: &LocalSession,
    endpoint_id: &[u8; 16],
    identity: &EndpointIdentity,
    channel_id: [u8; 16],
    now_ms: i64,
) -> Result<Vec<u8>, GatewayError> {
    let mut acknowledgment = AckFrame {
        ack_id: random_array::<16>().to_vec(),
        channel_id: channel_id.to_vec(),
        session_id: session.material.session_id.to_vec(),
        endpoint_id: endpoint_id.to_vec(),
        acknowledged_direction: WireDirection::HcToAba as i32,
        highest_contiguous_sequence: session.hc_frame_sequence,
        received_ranges: Vec::new(),
        key_generation: session.material.generation,
        created_at_ms: now_ms,
        signature: Vec::new(),
    };
    sign_ack(&mut acknowledgment, identity.signing_key())?;
    Ok(WirePacket {
        wire_major: 1,
        wire_minor: 0,
        packet_id: random_array::<16>().to_vec(),
        body: Some(wire_packet::Body::Ack(acknowledgment)),
    }
    .encode_to_vec())
}

fn ack_ranges(frame: &AckFrame, maximum: u64) -> Result<Vec<(u64, u64)>, GatewayError> {
    if frame.received_ranges.len() > 32
        || (frame.highest_contiguous_sequence == 0 && frame.received_ranges.is_empty())
    {
        return Err(GatewayError::ProtocolStage("ACK ranges"));
    }
    let mut previous = frame.highest_contiguous_sequence;
    let mut output = Vec::with_capacity(frame.received_ranges.len());
    for value in &frame.received_ranges {
        if value.start == 0
            || value.start > value.end
            || value.start <= previous
            || value.end > maximum
        {
            return Err(GatewayError::ProtocolStage("ACK ranges"));
        }
        output.push((value.start, value.end));
        previous = value.end;
    }
    Ok(output)
}

fn stable_reason_code(value: &str) -> bool {
    let bytes = value.as_bytes();
    !bytes.is_empty()
        && bytes.len() <= 64
        && bytes[0].is_ascii_uppercase()
        && bytes
            .iter()
            .all(|byte| byte.is_ascii_uppercase() || byte.is_ascii_digit() || *byte == b'_')
}

fn lower_hex(value: &[u8]) -> String {
    let mut output = String::with_capacity(value.len() * 2);
    for byte in value {
        use std::fmt::Write as _;
        let _ = write!(output, "{byte:02x}");
    }
    output
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
        || request.hc_kem_public_key.len() != 65
        || request.hc_kem_public_key[0] != 4
        || request.hc_kem_jkt.is_empty()
        || request.hc_signing_public_key.len() != 65
        || request.hc_signing_public_key[0] != 4
        || request.hc_signing_jkt.is_empty()
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

fn local_profiles<'a>(
    config: &'a AgentConfig,
    request: &OpenTunnelRequest,
) -> Option<(
    &'a crate::config::RuntimeProfile,
    &'a crate::config::WorkspaceProfile,
)> {
    let runtime = config
        .runtimes
        .iter()
        .find(|runtime| runtime.id == request.runtime_profile_id)?;
    let workspace = config
        .workspaces
        .iter()
        .find(|workspace| workspace.id == request.workspace_id)?;
    Some((runtime, workspace))
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
    use crate::journal::Journal;
    use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
    use p256::ecdsa::SigningKey;
    use url::Url;

    #[test]
    fn verifies_platform_control_and_returns_signed_policy_result()
    -> Result<(), Box<dyn std::error::Error>> {
        let directory = tempfile::tempdir()?;
        let command = directory.path().join("test-agent");
        fs::write(
            &command,
            b"#!/bin/sh\nIFS= read -r ignored || exit 1\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":\"aba-initialize\",\"result\":{\"protocolVersion\":1}}'\nIFS= read -r ignored || exit 1\nprintf '%s\\n' '{\"jsonrpc\":\"2.0\",\"id\":\"aba-session-new\",\"result\":{\"sessionId\":\"fixture-agent-session\"}}'\nwhile IFS= read -r ignored; do :; done\n",
        )?;
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
        let request = with_test_hc_kem(OpenTunnelRequest {
            session_id: vec![4_u8; 16],
            hc_endpoint_id: hc_endpoint_id.to_vec(),
            runtime_profile_id: "test-agent".to_owned(),
            workspace_id: "fixture".to_owned(),
            authorization_revision: 1,
            requested_key_generation: 1,
            requested_acp_capabilities: vec!["prompt".to_owned(), "session".to_owned()],
            expires_at_ms: unix_millis(now)? + 30_000,
            ..Default::default()
        });
        let packet = platform_open_packet(&online, &endpoint_id, request, unix_millis(now)?, 1)?;
        let mut state = ControlState::new(Journal::memory(1 << 20));
        let credential_id = [15_u8; 16];
        let encoded = state.handle(
            packet,
            ControlContext {
                endpoint_id: &endpoint_id,
                online_key: online.verifying_key(),
                identity: &identity,
                config: &config,
                credential_id: &credential_id,
                now,
            },
        )?;
        assert_eq!(encoded.len(), 2);
        let response = WirePacket::decode(encoded[0].as_slice())?;
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
        let package_packet = WirePacket::decode(encoded[1].as_slice())?;
        let package_frame = match package_packet.body {
            Some(wire_packet::Body::Control(value)) => value,
            _ => return Err("second response is not a control frame".into()),
        };
        assert_eq!(package_frame.r#type, ControlType::SessionKeyPackage as i32);
        assert_eq!(package_frame.control_sequence, 2);
        let package = SessionKeyPackage::decode(package_frame.payload.as_slice())?;
        assert_eq!(package.session_id, vec![4_u8; 16]);
        assert_eq!(package.issuer_credential_id, vec![15_u8; 16]);
        assert_eq!(package.crypto_suite, SUITE_NAME);
        assert_eq!(package.hpke_enc.len(), 65);
        assert_eq!(package.hpke_ciphertext.len(), 173);
        let mut hc_scalar = [0_u8; 32];
        hc_scalar[31] = 1;
        let hc_signing = SigningKey::from_slice(&hc_scalar)?;
        let ack_payload = SessionKeyPackageAck {
            session_id: package.session_id.clone(),
            key_generation: package.key_generation,
            key_package_id: package.key_package_id,
            recipient_hc_endpoint_id: hc_endpoint_id.to_vec(),
            acknowledged_at_ms: unix_millis(now)?,
        }
        .encode_to_vec();
        let ack_message_id = [18_u8; 16];
        let ack_transcript = control(ControlInput {
            message_id: &ack_message_id,
            sender_endpoint_id: &hc_endpoint_id,
            receiver_endpoint_id: &endpoint_id,
            sequence: 7,
            created_at_ms: unix_millis(now)?,
            control_type: ControlType::SessionKeyPackageAck as u32,
            payload: &ack_payload,
        })?;
        let ack_packet = WirePacket {
            wire_major: 1,
            wire_minor: 0,
            packet_id: vec![19_u8; 16],
            body: Some(wire_packet::Body::Control(ControlFrame {
                message_id: ack_message_id.to_vec(),
                sender_endpoint_id: hc_endpoint_id.to_vec(),
                receiver_endpoint_id: endpoint_id.to_vec(),
                control_sequence: 7,
                created_at_ms: unix_millis(now)?,
                r#type: ControlType::SessionKeyPackageAck as i32,
                payload: ack_payload,
                signature: sign_p1363_low_s(&hc_signing, &ack_transcript).to_vec(),
            })),
        };
        let ack_response = state.handle(
            ack_packet,
            ControlContext {
                endpoint_id: &endpoint_id,
                online_key: online.verifying_key(),
                identity: &identity,
                config: &config,
                credential_id: &credential_id,
                now,
            },
        )?;
        assert!(ack_response.is_empty());
        assert!(
            state
                .sessions
                .get(&[4_u8; 16])
                .is_some_and(|session| session.active)
        );
        let close_payload = CloseTunnelRequest {
            session_id: vec![4_u8; 16],
            stable_reason_code: "HC_REQUESTED".to_owned(),
            expires_at_ms: unix_millis(now)? + 30_000,
        }
        .encode_to_vec();
        let close_message_id = [20_u8; 16];
        let platform_sender = [0_u8; 16];
        let close_transcript = control(ControlInput {
            message_id: &close_message_id,
            sender_endpoint_id: &platform_sender,
            receiver_endpoint_id: &endpoint_id,
            sequence: 2,
            created_at_ms: unix_millis(now)?,
            control_type: ControlType::CloseTunnelRequest as u32,
            payload: &close_payload,
        })?;
        let closed = state.handle(
            WirePacket {
                wire_major: 1,
                wire_minor: 0,
                packet_id: vec![21_u8; 16],
                body: Some(wire_packet::Body::Control(ControlFrame {
                    message_id: close_message_id.to_vec(),
                    sender_endpoint_id: platform_sender.to_vec(),
                    receiver_endpoint_id: endpoint_id.to_vec(),
                    control_sequence: 2,
                    created_at_ms: unix_millis(now)?,
                    r#type: ControlType::CloseTunnelRequest as i32,
                    payload: close_payload,
                    signature: sign_p1363_low_s(&online, &close_transcript).to_vec(),
                })),
            },
            ControlContext {
                endpoint_id: &endpoint_id,
                online_key: online.verifying_key(),
                identity: &identity,
                config: &config,
                credential_id: &credential_id,
                now,
            },
        )?;
        assert_eq!(closed.len(), 1);
        let closed_packet = WirePacket::decode(closed[0].as_slice())?;
        let closed_control = match closed_packet.body {
            Some(wire_packet::Body::Control(value)) => value,
            _ => return Err("close response is not a control frame".into()),
        };
        let closed_result = CloseTunnelResult::decode(closed_control.payload.as_slice())?;
        assert_eq!(closed_result.status, CloseTunnelStatus::Accepted as i32);
        assert!(state.sessions.is_empty());
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
        let request = with_test_hc_kem(OpenTunnelRequest {
            session_id: vec![6_u8; 16],
            hc_endpoint_id: vec![7_u8; 16],
            runtime_profile_id: "missing".to_owned(),
            workspace_id: "missing".to_owned(),
            authorization_revision: 1,
            requested_key_generation: 1,
            requested_acp_capabilities: vec!["prompt".to_owned()],
            expires_at_ms: unix_millis(now)? + 30_000,
            ..Default::default()
        });
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
            ControlState::new(Journal::memory(1 << 20)).handle(
                packet,
                ControlContext {
                    endpoint_id: &endpoint_id,
                    online_key: online.verifying_key(),
                    identity: &identity,
                    config: &config,
                    credential_id: &[16_u8; 16],
                    now,
                }
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
        let request = with_test_hc_kem(OpenTunnelRequest {
            session_id: vec![13_u8; 16],
            hc_endpoint_id: vec![14_u8; 16],
            runtime_profile_id: "missing".to_owned(),
            workspace_id: "missing".to_owned(),
            authorization_revision: 1,
            requested_key_generation: 1,
            requested_acp_capabilities: vec!["prompt".to_owned()],
            expires_at_ms: unix_millis(now)? + 30_000,
            ..Default::default()
        });
        let packet = platform_open_packet(&online, &endpoint_id, request, unix_millis(now)?, 1)?;
        let config = AgentConfig {
            schema_version: CONFIG_SCHEMA_VERSION,
            platform: PlatformConfig { url: platform },
            limits: Limits::default(),
            runtimes: Vec::new(),
            workspaces: Vec::new(),
        };
        let encoded = ControlState::new(Journal::memory(1 << 20)).handle(
            packet,
            ControlContext {
                endpoint_id: &endpoint_id,
                online_key: online.verifying_key(),
                identity: &identity,
                config: &config,
                credential_id: &[17_u8; 16],
                now,
            },
        )?;
        assert_eq!(encoded.len(), 1);
        let response = WirePacket::decode(encoded[0].as_slice())?;
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

    fn with_test_hc_kem(mut request: OpenTunnelRequest) -> OpenTunnelRequest {
        let mut public = vec![4_u8];
        public.extend_from_slice(
            &URL_SAFE_NO_PAD
                .decode("axfR8uEsQkf4vOblY6RA8ncDfYEt6zOg9KE5RdiYwpY")
                .unwrap_or_default(),
        );
        public.extend_from_slice(
            &URL_SAFE_NO_PAD
                .decode("T-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU")
                .unwrap_or_default(),
        );
        request.hc_kem_public_key = public;
        request.hc_kem_jkt = "xx0BcA-wMohw8atYDJOe6peGModklG2wRHBlXHMvl0M".to_owned();
        request.hc_signing_public_key = request.hc_kem_public_key.clone();
        request.hc_signing_jkt = request.hc_kem_jkt.clone();
        request
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
