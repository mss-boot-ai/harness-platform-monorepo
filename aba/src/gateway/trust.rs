use std::time::{SystemTime, UNIX_EPOCH};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use p256::ecdsa::VerifyingKey;
use serde::Deserialize;

use super::GatewayError;
use crate::crypto::{P256PublicJwk, verify_p1363_low_s};
use crate::identity::DevFileKeyStore;

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub(super) struct TrustManifestEnvelope {
    expires_at: String,
    payload_base64_url: String,
    revision: u64,
    root_public_jwk: P256PublicJwk,
    signature_base64_url: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct TrustManifestPayload {
    expires_at_ms: i64,
    online_public_jwk: P256PublicJwk,
    revision: u64,
    root_jkt: String,
}

pub(super) struct VerifiedTrust {
    pub online_key: VerifyingKey,
    pub revision: u64,
    pub root_jkt: String,
}

impl TrustManifestEnvelope {
    pub fn verify_and_pin(
        self,
        store: &DevFileKeyStore,
        now: SystemTime,
    ) -> Result<VerifiedTrust, GatewayError> {
        if self.expires_at.is_empty() || self.revision == 0 {
            return Err(GatewayError::Trust);
        }
        let payload = decode_bounded(&self.payload_base64_url, 4096)?;
        let signature = decode_fixed(&self.signature_base64_url, 64)?;
        let root_key = self
            .root_public_jwk
            .verifying_key()
            .map_err(|_| GatewayError::Trust)?;
        if !verify_p1363_low_s(&root_key, &payload, &signature) {
            return Err(GatewayError::Trust);
        }
        let payload: TrustManifestPayload =
            serde_json::from_slice(&payload).map_err(|_| GatewayError::Trust)?;
        let root_jkt = self
            .root_public_jwk
            .thumbprint()
            .map_err(|_| GatewayError::Trust)?;
        let now_ms = now
            .duration_since(UNIX_EPOCH)
            .map_err(|_| GatewayError::Trust)?
            .as_millis();
        let now_ms = i64::try_from(now_ms).map_err(|_| GatewayError::Trust)?;
        if payload.revision != self.revision
            || payload.root_jkt != root_jkt
            || payload.expires_at_ms <= now_ms
        {
            return Err(GatewayError::Trust);
        }
        let online_key = payload
            .online_public_jwk
            .verifying_key()
            .map_err(|_| GatewayError::Trust)?;
        if payload
            .online_public_jwk
            .thumbprint()
            .map_err(|_| GatewayError::Trust)?
            == root_jkt
        {
            return Err(GatewayError::Trust);
        }
        store.pin_gateway_trust(&root_jkt, self.revision, &payload.online_public_jwk)?;
        Ok(VerifiedTrust {
            online_key,
            revision: self.revision,
            root_jkt,
        })
    }
}

fn decode_fixed(value: &str, length: usize) -> Result<Vec<u8>, GatewayError> {
    let decoded = URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| GatewayError::Trust)?;
    if decoded.len() != length {
        return Err(GatewayError::Trust);
    }
    Ok(decoded)
}

fn decode_bounded(value: &str, max: usize) -> Result<Vec<u8>, GatewayError> {
    let decoded = URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| GatewayError::Trust)?;
    if decoded.is_empty() || decoded.len() > max {
        return Err(GatewayError::Trust);
    }
    Ok(decoded)
}
