use std::thread;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use prost::Message as _;
use rand_core::{OsRng, RngCore};
use reqwest::StatusCode;
use reqwest::blocking::{Client, Response};
use serde::Deserialize;
use tungstenite::client::IntoClientRequest as _;
use tungstenite::http::HeaderValue;
use tungstenite::http::header::SEC_WEBSOCKET_PROTOCOL;
use tungstenite::protocol::WebSocket;
use tungstenite::stream::MaybeTlsStream;
use tungstenite::{Message, connect};
use url::Url;

use super::GatewayError;
use super::control::{ControlContext, ControlState};
use super::transcript::{
    ClientChallengeInput, ConnectionReadyInput, ServerChallengeInput, client_challenge,
    connection_ready, server_challenge,
};
use super::trust::{TrustManifestEnvelope, VerifiedTrust};
use crate::config::AgentConfig;
use crate::crypto::dpop::{DpopInput, NonceDpopInput, create_dpop_proof, create_nonce_dpop_proof};
use crate::crypto::{sign_p1363_low_s, verify_p1363_low_s};
use crate::identity::{DevFileKeyStore, EndpointCredentials, EndpointIdentity};
use crate::journal::Journal;
use crate::protocol::awpv1::{ChallengeResponse, WirePacket, wire_packet};

const PROTOCOL: &str = "mss.awp.v1";
const MAX_PACKET_BYTES: usize = 1 << 20;

pub struct GatewayClient {
    base: Url,
    http: Client,
}

pub struct ReadyConnection {
    pub connection_generation: u64,
    pub connection_id: [u8; 16],
    pub endpoint_id: String,
    pub heartbeat_interval_ms: u32,
    pub root_jkt: String,
    endpoint_id_bytes: [u8; 16],
    credential_id_bytes: [u8; 16],
    online_key: p256::ecdsa::VerifyingKey,
    socket: WebSocket<MaybeTlsStream<std::net::TcpStream>>,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RefreshedCredentials {
    endpoint_id: String,
    credential_id: String,
    token_type: String,
    access_token: String,
    access_expires_at: String,
    refresh_token: String,
    refresh_expires_at: String,
    signing_jkt: String,
    kem_jkt: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Ticket {
    ticket: String,
    expires_at: String,
    protocol: String,
    websocket_url: String,
}

impl GatewayClient {
    pub fn new(base: Url) -> Result<Self, GatewayError> {
        if !matches!(base.scheme(), "http" | "https")
            || base.host_str().is_none()
            || !base.username().is_empty()
            || base.password().is_some()
            || base.query().is_some()
            || base.fragment().is_some()
        {
            return Err(GatewayError::InvalidInput);
        }
        let http = Client::builder()
            .connect_timeout(Duration::from_secs(10))
            .timeout(Duration::from_secs(20))
            .build()?;
        Ok(Self { base, http })
    }

    pub fn connect(
        &self,
        identity: &EndpointIdentity,
        store: &DevFileKeyStore,
    ) -> Result<ReadyConnection, GatewayError> {
        let credentials = self.refresh(identity, store)?;
        let trust = self.fetch_trust(store)?;
        let ticket = self.issue_ticket(identity, &credentials)?;
        self.open_websocket(identity, &credentials, &trust, ticket)
    }

    pub fn run(
        &self,
        identity: &EndpointIdentity,
        store: &DevFileKeyStore,
        config: &AgentConfig,
        journal: Journal,
    ) -> Result<(), GatewayError> {
        let mut controls = ControlState::new(journal);
        let mut retry_attempt = 0_u32;
        loop {
            let ready = match self.connect(identity, store) {
                Ok(ready) => ready,
                Err(error) if retryable_connect_error(&error) => {
                    retry_attempt = retry_attempt.saturating_add(1);
                    thread::sleep(reconnect_delay(retry_attempt));
                    continue;
                }
                Err(error) => return Err(error),
            };
            println!(
                "gateway ready: endpoint {} generation {}",
                ready.endpoint_id, ready.connection_generation
            );
            let connected_at = Instant::now();
            match ready.run_once(identity, config, &mut controls) {
                Ok(()) | Err(GatewayError::WebSocket(_)) => {}
                Err(error) => return Err(error),
            }
            if connected_at.elapsed() >= Duration::from_secs(30) {
                retry_attempt = 0;
            } else {
                retry_attempt = retry_attempt.saturating_add(1);
            }
            thread::sleep(reconnect_delay(retry_attempt));
        }
    }

    fn refresh(
        &self,
        identity: &EndpointIdentity,
        store: &DevFileKeyStore,
    ) -> Result<EndpointCredentials, GatewayError> {
        let current = store.load_credentials()?;
        let target = self.endpoint("/gateway/v1/tokens/refresh")?;
        let response = self
            .http
            .post(target.clone())
            .header(
                "Authorization",
                format!("Refresh {}", current.refresh_token),
            )
            .send()?;
        let nonce = nonce_challenge(response)?;
        let public = identity.signing_public_jwk()?;
        let proof = create_nonce_dpop_proof(
            identity.signing_key(),
            NonceDpopInput {
                htm: "POST",
                htu: target.as_str(),
                issued_at: unix_seconds()?,
                jti: &random_uuid_v4(),
                nonce: &nonce,
                public_jwk: &public,
            },
        )?;
        let response = self
            .http
            .post(target)
            .header(
                "Authorization",
                format!("Refresh {}", current.refresh_token),
            )
            .header("DPoP", proof)
            .send()?;
        if response.status() != StatusCode::OK {
            return Err(GatewayError::Rejected(response.status()));
        }
        let refreshed: RefreshedCredentials = response.json()?;
        let summary = identity.summary()?;
        if refreshed.endpoint_id != current.endpoint_id
            || refreshed.token_type != "DPoP"
            || refreshed.signing_jkt != summary.signing_jkt
            || refreshed.kem_jkt != summary.kem_jkt
        {
            return Err(GatewayError::InvalidResponse);
        }
        let updated = EndpointCredentials {
            endpoint_id: refreshed.endpoint_id,
            credential_id: refreshed.credential_id,
            access_token: refreshed.access_token,
            access_expires_at: refreshed.access_expires_at,
            refresh_token: refreshed.refresh_token,
            refresh_expires_at: refreshed.refresh_expires_at,
        };
        store.save_credentials(updated)?;
        store.load_credentials().map_err(GatewayError::from)
    }

    fn fetch_trust(&self, store: &DevFileKeyStore) -> Result<VerifiedTrust, GatewayError> {
        let response = self
            .http
            .get(self.endpoint("/gateway/v1/trust-manifest")?)
            .send()?;
        if response.status() != StatusCode::OK {
            return Err(GatewayError::Rejected(response.status()));
        }
        let manifest: TrustManifestEnvelope = response.json()?;
        manifest.verify_and_pin(store, SystemTime::now())
    }

    fn issue_ticket(
        &self,
        identity: &EndpointIdentity,
        credentials: &EndpointCredentials,
    ) -> Result<Ticket, GatewayError> {
        let target = self.endpoint("/gateway/v1/ws/tickets")?;
        let response = self
            .http
            .post(target.clone())
            .header(
                "Authorization",
                format!("DPoP {}", credentials.access_token),
            )
            .send()?;
        let nonce = nonce_challenge(response)?;
        let public = identity.signing_public_jwk()?;
        let proof = create_dpop_proof(
            identity.signing_key(),
            DpopInput {
                access_token: &credentials.access_token,
                htm: "POST",
                htu: target.as_str(),
                issued_at: unix_seconds()?,
                jti: &random_uuid_v4(),
                nonce: &nonce,
                public_jwk: &public,
            },
        )?;
        let response = self
            .http
            .post(target)
            .header(
                "Authorization",
                format!("DPoP {}", credentials.access_token),
            )
            .header("DPoP", proof.proof)
            .send()?;
        if response.status() != StatusCode::CREATED {
            return Err(GatewayError::Rejected(response.status()));
        }
        let ticket: Ticket = response.json()?;
        if ticket.protocol != PROTOCOL
            || ticket.ticket.len() != 43
            || ticket.expires_at.is_empty()
            || !matches!(Url::parse(&ticket.websocket_url), Ok(url) if matches!(url.scheme(), "ws" | "wss") && url.host_str().is_some())
        {
            return Err(GatewayError::InvalidResponse);
        }
        Ok(ticket)
    }

    fn open_websocket(
        &self,
        identity: &EndpointIdentity,
        credentials: &EndpointCredentials,
        trust: &VerifiedTrust,
        ticket: Ticket,
    ) -> Result<ReadyConnection, GatewayError> {
        let endpoint_id = decode_hex_id(&credentials.endpoint_id)?;
        let credential_id = decode_hex_id(&credentials.credential_id)?;
        let mut request = ticket.websocket_url.as_str().into_client_request()?;
        let protocols = HeaderValue::from_str(&format!("{PROTOCOL}, mss.ticket.{}", ticket.ticket))
            .map_err(|_| GatewayError::InvalidResponse)?;
        request
            .headers_mut()
            .insert(SEC_WEBSOCKET_PROTOCOL, protocols);
        let (mut socket, response) = connect(request)?;
        if response
            .headers()
            .get(SEC_WEBSOCKET_PROTOCOL)
            .and_then(|value| value.to_str().ok())
            != Some(PROTOCOL)
        {
            return Err(GatewayError::Protocol);
        }

        let challenge_packet = read_packet(&mut socket)?;
        if challenge_packet.wire_major != 1 || challenge_packet.packet_id.len() != 16 {
            return Err(GatewayError::Protocol);
        }
        let challenge = match challenge_packet.body {
            Some(wire_packet::Body::ServerChallenge(value)) => value,
            _ => return Err(GatewayError::Protocol),
        };
        if challenge.connection_id.len() != 16
            || challenge.connection_generation == 0
            || challenge.server_nonce.len() != 32
            || challenge.server_signature.len() != 64
            || challenge.trust_manifest_revision != trust.revision
            || challenge.credential_status_revision == 0
        {
            return Err(GatewayError::Protocol);
        }
        let server_transcript = server_challenge(ServerChallengeInput {
            connection_id: &challenge.connection_id,
            connection_generation: challenge.connection_generation,
            server_nonce: &challenge.server_nonce,
            server_time_ms: challenge.server_time_ms,
            manifest_revision: challenge.trust_manifest_revision,
            credential_revision: challenge.credential_status_revision,
            endpoint_id: &endpoint_id,
        })?;
        if !verify_p1363_low_s(
            &trust.online_key,
            &server_transcript,
            &challenge.server_signature,
        ) {
            return Err(GatewayError::Trust);
        }

        let client_nonce = random_array::<32>();
        let endpoint_signature = sign_p1363_low_s(
            identity.signing_key(),
            &client_challenge(ClientChallengeInput {
                connection_id: &challenge.connection_id,
                connection_generation: challenge.connection_generation,
                server_nonce: &challenge.server_nonce,
                client_nonce: &client_nonce,
                endpoint_id: &endpoint_id,
                credential_id: &credential_id,
                manifest_revision: challenge.trust_manifest_revision,
                credential_revision: challenge.credential_status_revision,
            })?,
        );
        let response_packet = WirePacket {
            wire_major: 1,
            wire_minor: 0,
            packet_id: random_array::<16>().to_vec(),
            body: Some(wire_packet::Body::ChallengeResponse(ChallengeResponse {
                connection_id: challenge.connection_id.clone(),
                connection_generation: challenge.connection_generation,
                endpoint_id: endpoint_id.to_vec(),
                credential_serial: credential_id.to_vec(),
                client_nonce: client_nonce.to_vec(),
                last_manifest_revision: challenge.trust_manifest_revision,
                last_credential_status_revision: challenge.credential_status_revision,
                endpoint_signature: endpoint_signature.to_vec(),
            })),
        };
        socket.send(Message::binary(response_packet.encode_to_vec()))?;

        let ready_packet = read_packet(&mut socket)?;
        if ready_packet.wire_major != 1 || ready_packet.packet_id.len() != 16 {
            return Err(GatewayError::Protocol);
        }
        let ready = match ready_packet.body {
            Some(wire_packet::Body::ConnectionReady(value)) => value,
            _ => return Err(GatewayError::Protocol),
        };
        if ready.connection_id != challenge.connection_id
            || ready.connection_generation != challenge.connection_generation
            || ready.fencing_token.len() != 32
            || ready.max_packet_bytes == 0
            || ready.max_inflight_frames == 0
            || ready.heartbeat_interval_ms == 0
            || ready.server_signature.len() != 64
        {
            return Err(GatewayError::Protocol);
        }
        let ready_transcript = connection_ready(ConnectionReadyInput {
            connection_id: &ready.connection_id,
            connection_generation: ready.connection_generation,
            fencing_token: &ready.fencing_token,
            ready_at_ms: ready.ready_at_ms,
            max_packet_bytes: ready.max_packet_bytes,
            max_inflight_frames: ready.max_inflight_frames,
            heartbeat_interval_ms: ready.heartbeat_interval_ms,
            endpoint_id: &endpoint_id,
        })?;
        if !verify_p1363_low_s(
            &trust.online_key,
            &ready_transcript,
            &ready.server_signature,
        ) {
            return Err(GatewayError::Trust);
        }
        let connection_id: [u8; 16] = ready
            .connection_id
            .as_slice()
            .try_into()
            .map_err(|_| GatewayError::Protocol)?;
        Ok(ReadyConnection {
            connection_generation: ready.connection_generation,
            connection_id,
            endpoint_id: credentials.endpoint_id.clone(),
            heartbeat_interval_ms: ready.heartbeat_interval_ms,
            root_jkt: trust.root_jkt.clone(),
            endpoint_id_bytes: endpoint_id,
            credential_id_bytes: credential_id,
            online_key: trust.online_key,
            socket,
        })
    }

    fn endpoint(&self, path: &str) -> Result<Url, GatewayError> {
        self.base.join(path).map_err(|_| GatewayError::InvalidInput)
    }
}

fn retryable_connect_error(error: &GatewayError) -> bool {
    match error {
        GatewayError::Http(_) | GatewayError::WebSocket(_) => true,
        GatewayError::Rejected(status) => status.is_server_error(),
        _ => false,
    }
}

fn reconnect_delay(attempt: u32) -> Duration {
    let maximum_ms = (1_u64 << attempt.min(5)).saturating_mul(1_000).min(30_000);
    Duration::from_millis(OsRng.next_u64() % (maximum_ms + 1))
}

impl ReadyConnection {
    fn run_once(
        mut self,
        identity: &EndpointIdentity,
        config: &AgentConfig,
        controls: &mut ControlState,
    ) -> Result<(), GatewayError> {
        for packet in
            controls.begin_connection(&self.endpoint_id_bytes, identity, SystemTime::now())?
        {
            self.socket.send(Message::binary(packet))?;
        }
        loop {
            let message = match self.socket.read() {
                Ok(message) => message,
                Err(tungstenite::Error::ConnectionClosed | tungstenite::Error::AlreadyClosed) => {
                    return Ok(());
                }
                Err(error) => return Err(GatewayError::WebSocket(error)),
            };
            match message {
                Message::Binary(encoded) => {
                    if encoded.is_empty() || encoded.len() > MAX_PACKET_BYTES {
                        return Err(GatewayError::Protocol);
                    }
                    let packet = WirePacket::decode(encoded).map_err(|_| GatewayError::Protocol)?;
                    let response = controls.handle(
                        packet,
                        ControlContext {
                            endpoint_id: &self.endpoint_id_bytes,
                            online_key: &self.online_key,
                            identity,
                            config,
                            credential_id: &self.credential_id_bytes,
                            now: SystemTime::now(),
                        },
                    )?;
                    for packet in response {
                        self.socket.send(Message::binary(packet))?;
                    }
                }
                Message::Ping(_) | Message::Pong(_) => self.socket.flush()?,
                Message::Close(_) => return Ok(()),
                Message::Text(_) | Message::Frame(_) => return Err(GatewayError::Protocol),
            }
        }
    }

    pub fn close(mut self) -> Result<(), GatewayError> {
        self.socket.close(None)?;
        Ok(())
    }
}

fn nonce_challenge(response: Response) -> Result<String, GatewayError> {
    if response.status() != StatusCode::UNAUTHORIZED {
        return Err(GatewayError::Rejected(response.status()));
    }
    let nonce = response
        .headers()
        .get("DPoP-Nonce")
        .and_then(|value| value.to_str().ok())
        .ok_or(GatewayError::InvalidResponse)?;
    let decoded = URL_SAFE_NO_PAD
        .decode(nonce)
        .map_err(|_| GatewayError::InvalidResponse)?;
    if decoded.len() != 32 {
        return Err(GatewayError::InvalidResponse);
    }
    Ok(nonce.to_owned())
}

fn read_packet(
    socket: &mut WebSocket<MaybeTlsStream<std::net::TcpStream>>,
) -> Result<WirePacket, GatewayError> {
    let message = socket.read()?;
    let Message::Binary(encoded) = message else {
        return Err(GatewayError::Protocol);
    };
    if encoded.is_empty() || encoded.len() > MAX_PACKET_BYTES {
        return Err(GatewayError::Protocol);
    }
    WirePacket::decode(encoded).map_err(|_| GatewayError::Protocol)
}

fn decode_hex_id(value: &str) -> Result<[u8; 16], GatewayError> {
    if value.len() != 32 {
        return Err(GatewayError::InvalidResponse);
    }
    let mut result = [0_u8; 16];
    for (index, item) in result.iter_mut().enumerate() {
        *item = u8::from_str_radix(&value[index * 2..index * 2 + 2], 16)
            .map_err(|_| GatewayError::InvalidResponse)?;
    }
    Ok(result)
}

fn random_array<const N: usize>() -> [u8; N] {
    let mut value = [0_u8; N];
    OsRng.fill_bytes(&mut value);
    value
}

fn random_uuid_v4() -> String {
    let mut value = random_array::<16>();
    value[6] = (value[6] & 0x0f) | 0x40;
    value[8] = (value[8] & 0x3f) | 0x80;
    format!(
        "{:02x}{:02x}{:02x}{:02x}-{:02x}{:02x}-{:02x}{:02x}-{:02x}{:02x}-{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}",
        value[0],
        value[1],
        value[2],
        value[3],
        value[4],
        value[5],
        value[6],
        value[7],
        value[8],
        value[9],
        value[10],
        value[11],
        value[12],
        value[13],
        value[14],
        value[15]
    )
}

fn unix_seconds() -> Result<i64, GatewayError> {
    let value = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| GatewayError::InvalidInput)?
        .as_secs();
    i64::try_from(value).map_err(|_| GatewayError::InvalidInput)
}
