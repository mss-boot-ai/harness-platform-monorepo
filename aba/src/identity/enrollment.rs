use std::thread;
use std::time::{Duration, Instant};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use rand_core::{OsRng, RngCore};
use reqwest::StatusCode;
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use thiserror::Error;
use url::Url;

use super::{DevFileKeyStore, EndpointCredentials, EndpointIdentity};
use crate::crypto::{P256PublicJwk, sign_p1363_low_s};

#[derive(Debug, Error)]
pub enum EnrollmentError {
    #[error("ABA enrollment input is invalid")]
    InvalidInput,
    #[error("ABA enrollment HTTP request failed")]
    Http(#[from] reqwest::Error),
    #[error("ABA enrollment was rejected with HTTP {0}")]
    Rejected(StatusCode),
    #[error("ABA enrollment response is invalid")]
    InvalidResponse,
    #[error("ABA enrollment timed out")]
    Timeout,
    #[error(transparent)]
    KeyStore(#[from] super::KeyStoreError),
}

pub struct EnrollmentClient {
    base: Url,
    http: Client,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct StartRequest<'a> {
    endpoint_name: &'a str,
    software_version: &'a str,
    signing_public_jwk: &'a P256PublicJwk,
    kem_public_jwk: &'a P256PublicJwk,
    client_nonce: String,
    proof: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Started {
    enrollment_id: String,
    device_code: String,
    user_code: String,
    verification_uri: String,
    expires_at: String,
    interval_seconds: u64,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Polled {
    enrollment_id: String,
    status: String,
    expires_at: String,
}

#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct ConsumeRequest {
    device_code: String,
    client_nonce: String,
    proof: String,
}

#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct Consumed {
    endpoint_id: String,
    credential_id: String,
    token_type: String,
    access_token: String,
    access_expires_at: String,
    refresh_token: String,
    refresh_expires_at: String,
}

pub struct EnrollmentDisplay {
    pub user_code: String,
    pub verification_uri: String,
}

impl EnrollmentClient {
    pub fn new(base: Url) -> Result<Self, EnrollmentError> {
        if !matches!(base.scheme(), "http" | "https") || base.host_str().is_none() {
            return Err(EnrollmentError::InvalidInput);
        }
        let http = Client::builder()
            .connect_timeout(Duration::from_secs(10))
            .timeout(Duration::from_secs(20))
            .build()?;
        Ok(Self { base, http })
    }

    pub fn enroll<F>(
        &self,
        identity: &EndpointIdentity,
        store: &DevFileKeyStore,
        endpoint_name: &str,
        timeout: Duration,
        display: F,
    ) -> Result<String, EnrollmentError>
    where
        F: FnOnce(EnrollmentDisplay),
    {
        let started = self.start(identity, endpoint_name)?;
        display(EnrollmentDisplay {
            user_code: started.user_code.clone(),
            verification_uri: started.verification_uri.clone(),
        });
        let deadline = Instant::now() + timeout;
        loop {
            if Instant::now() >= deadline {
                return Err(EnrollmentError::Timeout);
            }
            thread::sleep(Duration::from_secs(started.interval_seconds.clamp(1, 10)));
            let polled = self.poll(&started)?;
            match polled.status.as_str() {
                "PENDING" => continue,
                "APPROVED" => break,
                "DENIED" | "EXPIRED" | "CONSUMED" => {
                    return Err(EnrollmentError::InvalidResponse);
                }
                _ => return Err(EnrollmentError::InvalidResponse),
            }
        }
        let consumed = self.consume(identity, &started)?;
        store.save_credentials(EndpointCredentials {
            endpoint_id: consumed.endpoint_id.clone(),
            credential_id: consumed.credential_id,
            access_token: consumed.access_token,
            access_expires_at: consumed.access_expires_at,
            refresh_token: consumed.refresh_token,
            refresh_expires_at: consumed.refresh_expires_at,
        })?;
        Ok(consumed.endpoint_id)
    }

    fn start(
        &self,
        identity: &EndpointIdentity,
        endpoint_name: &str,
    ) -> Result<Started, EnrollmentError> {
        let signing = identity.signing_public_jwk()?;
        let kem = identity.kem_public_jwk()?;
        let signing_jkt = signing
            .thumbprint()
            .map_err(|_| EnrollmentError::InvalidInput)?;
        let kem_jkt = kem
            .thumbprint()
            .map_err(|_| EnrollmentError::InvalidInput)?;
        let nonce = random_bytes();
        let transcript = start_transcript(
            &nonce,
            &signing_jkt,
            &kem_jkt,
            endpoint_name,
            env!("CARGO_PKG_VERSION"),
        )?;
        let proof = sign_p1363_low_s(identity.signing_key(), &transcript);
        let response = self
            .http
            .post(self.endpoint("/gateway/v1/enrollments")?)
            .json(&StartRequest {
                endpoint_name,
                software_version: env!("CARGO_PKG_VERSION"),
                signing_public_jwk: &signing,
                kem_public_jwk: &kem,
                client_nonce: URL_SAFE_NO_PAD.encode(nonce),
                proof: URL_SAFE_NO_PAD.encode(proof),
            })
            .send()?;
        if response.status() != StatusCode::CREATED {
            return Err(EnrollmentError::Rejected(response.status()));
        }
        let started: Started = response.json()?;
        if started.enrollment_id.len() != 32
            || started.device_code.len() != 43
            || started.user_code.len() != 9
            || started.interval_seconds == 0
            || started.expires_at.is_empty()
            || started.verification_uri.is_empty()
        {
            return Err(EnrollmentError::InvalidResponse);
        }
        Ok(started)
    }

    fn poll(&self, started: &Started) -> Result<Polled, EnrollmentError> {
        let response = self
            .http
            .get(self.endpoint(&format!(
                "/gateway/v1/enrollments/{}",
                started.enrollment_id
            ))?)
            .header("Authorization", format!("Device {}", started.device_code))
            .send()?;
        if response.status() != StatusCode::OK {
            return Err(EnrollmentError::Rejected(response.status()));
        }
        let polled: Polled = response.json()?;
        if polled.enrollment_id != started.enrollment_id || polled.expires_at != started.expires_at
        {
            return Err(EnrollmentError::InvalidResponse);
        }
        Ok(polled)
    }

    fn consume(
        &self,
        identity: &EndpointIdentity,
        started: &Started,
    ) -> Result<Consumed, EnrollmentError> {
        let id = decode_hex_id(&started.enrollment_id)?;
        let device = decode_fixed(&started.device_code, 32)?;
        let nonce = random_bytes();
        let proof = sign_p1363_low_s(
            identity.signing_key(),
            &consume_transcript(&id, &device, &nonce),
        );
        let response = self
            .http
            .post(self.endpoint(&format!(
                "/gateway/v1/enrollments/{}/consume",
                started.enrollment_id
            ))?)
            .json(&ConsumeRequest {
                device_code: started.device_code.clone(),
                client_nonce: URL_SAFE_NO_PAD.encode(nonce),
                proof: URL_SAFE_NO_PAD.encode(proof),
            })
            .send()?;
        if response.status() != StatusCode::CREATED {
            return Err(EnrollmentError::Rejected(response.status()));
        }
        let consumed: Consumed = response.json()?;
        if consumed.token_type != "DPoP"
            || consumed.endpoint_id.len() != 32
            || consumed.credential_id.len() != 32
            || consumed.access_token.len() != 43
            || consumed.refresh_token.len() != 43
        {
            return Err(EnrollmentError::InvalidResponse);
        }
        Ok(consumed)
    }

    fn endpoint(&self, path: &str) -> Result<Url, EnrollmentError> {
        self.base
            .join(path)
            .map_err(|_| EnrollmentError::InvalidInput)
    }
}

fn start_transcript(
    nonce: &[u8],
    signing_jkt: &str,
    kem_jkt: &str,
    name: &str,
    version: &str,
) -> Result<Vec<u8>, EnrollmentError> {
    let signing = decode_fixed(signing_jkt, 32)?;
    let kem = decode_fixed(kem_jkt, 32)?;
    if nonce.len() != 32
        || signing == kem
        || name.is_empty()
        || name.len() > 120
        || version.is_empty()
        || version.len() > 64
    {
        return Err(EnrollmentError::InvalidInput);
    }
    let mut output = b"mss-aba-enrollment-start-v1".to_vec();
    output.extend_from_slice(nonce);
    output.extend_from_slice(&signing);
    output.extend_from_slice(&kem);
    write_text(&mut output, name);
    write_text(&mut output, version);
    Ok(output)
}

fn consume_transcript(id: &[u8; 16], device: &[u8], nonce: &[u8]) -> Vec<u8> {
    let mut output = b"mss-aba-enrollment-consume-v1".to_vec();
    output.extend_from_slice(id);
    output.extend_from_slice(device);
    output.extend_from_slice(nonce);
    output
}

fn write_text(output: &mut Vec<u8>, value: &str) {
    output.extend_from_slice(&(value.len() as u16).to_be_bytes());
    output.extend_from_slice(value.as_bytes());
}
fn random_bytes() -> [u8; 32] {
    let mut value = [0_u8; 32];
    OsRng.fill_bytes(&mut value);
    value
}
fn decode_fixed(value: &str, length: usize) -> Result<Vec<u8>, EnrollmentError> {
    let decoded = URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| EnrollmentError::InvalidInput)?;
    if decoded.len() != length {
        return Err(EnrollmentError::InvalidInput);
    }
    Ok(decoded)
}
fn decode_hex_id(value: &str) -> Result<[u8; 16], EnrollmentError> {
    if value.len() != 32 {
        return Err(EnrollmentError::InvalidInput);
    }
    let mut out = [0_u8; 16];
    for (index, item) in out.iter_mut().enumerate() {
        *item = u8::from_str_radix(&value[index * 2..index * 2 + 2], 16)
            .map_err(|_| EnrollmentError::InvalidInput)?;
    }
    Ok(out)
}
