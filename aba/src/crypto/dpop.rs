use std::net::IpAddr;

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use p256::ecdsa::SigningKey;
use serde::Serialize;
use url::Url;

use super::{CryptoError, P256PublicJwk, access_token_hash, sign_p1363_low_s};

pub struct DpopInput<'a> {
    pub access_token: &'a str,
    pub htm: &'a str,
    pub htu: &'a str,
    pub issued_at: i64,
    pub jti: &'a str,
    pub nonce: &'a str,
    pub public_jwk: &'a P256PublicJwk,
}

#[derive(Debug, PartialEq, Eq)]
pub struct DpopProof {
    pub ath: String,
    pub htm: String,
    pub htu: String,
    pub issued_at: i64,
    pub jti: String,
    pub proof: String,
}

#[derive(Serialize)]
struct Header<'a> {
    alg: &'static str,
    jwk: &'a P256PublicJwk,
    typ: &'static str,
}

#[derive(Serialize)]
struct Claims<'a> {
    ath: &'a str,
    htm: &'a str,
    htu: &'a str,
    iat: i64,
    jti: &'a str,
    nonce: &'a str,
}

pub fn create_dpop_proof(
    signing_key: &SigningKey,
    input: DpopInput<'_>,
) -> Result<DpopProof, CryptoError> {
    if input.access_token.is_empty() {
        return Err(CryptoError::DpopInput("access token is required"));
    }
    if input.nonce.is_empty() {
        return Err(CryptoError::DpopInput("server nonce is required"));
    }
    if !is_uuid_v4(input.jti) {
        return Err(CryptoError::DpopInput("jti must be a canonical UUID v4"));
    }
    input.public_jwk.verifying_key()?;
    let method = input.htm.trim().to_ascii_uppercase();
    if method.is_empty() || !method.bytes().all(|byte| byte.is_ascii_uppercase()) {
        return Err(CryptoError::DpopInput("HTTP method is invalid"));
    }
    let htu = normalize_htu(input.htu)?;
    let ath = access_token_hash(input.access_token);
    let header = serde_json::to_vec(&Header {
        alg: "ES256",
        jwk: input.public_jwk,
        typ: "dpop+jwt",
    })?;
    let claims = serde_json::to_vec(&Claims {
        ath: &ath,
        htm: &method,
        htu: &htu,
        iat: input.issued_at,
        jti: input.jti,
        nonce: input.nonce,
    })?;
    let protected = URL_SAFE_NO_PAD.encode(header);
    let payload = URL_SAFE_NO_PAD.encode(claims);
    let signing_input = format!("{protected}.{payload}");
    let signature = sign_p1363_low_s(signing_key, signing_input.as_bytes());

    Ok(DpopProof {
        ath,
        htm: method,
        htu,
        issued_at: input.issued_at,
        jti: input.jti.to_owned(),
        proof: format!("{signing_input}.{}", URL_SAFE_NO_PAD.encode(signature)),
    })
}

pub fn normalize_htu(value: &str) -> Result<String, CryptoError> {
    let mut parsed = Url::parse(value)?;
    if !parsed.username().is_empty() || parsed.password().is_some() || parsed.host_str().is_none() {
        return Err(CryptoError::DpopInput(
            "target URI must be absolute and must not contain userinfo",
        ));
    }
    let loopback = parsed.host_str().is_some_and(|hostname| {
        hostname.eq_ignore_ascii_case("localhost")
            || hostname
                .parse::<IpAddr>()
                .is_ok_and(|address| address.is_loopback())
    });
    if parsed.scheme() != "https" && !(parsed.scheme() == "http" && loopback) {
        return Err(CryptoError::DpopInput(
            "target URI must use HTTPS except for loopback development",
        ));
    }
    parsed.set_query(None);
    parsed.set_fragment(None);
    Ok(parsed.to_string())
}

fn is_uuid_v4(value: &str) -> bool {
    let bytes = value.as_bytes();
    if bytes.len() != 36
        || bytes[8] != b'-'
        || bytes[13] != b'-'
        || bytes[18] != b'-'
        || bytes[23] != b'-'
        || bytes[14] != b'4'
        || !matches!(bytes[19], b'8' | b'9' | b'a' | b'b')
    {
        return false;
    }
    bytes.iter().enumerate().all(|(index, byte)| {
        matches!(index, 8 | 13 | 18 | 23) || byte.is_ascii_digit() || matches!(byte, b'a'..=b'f')
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::crypto::verify_p1363_low_s;

    #[test]
    fn creates_bound_low_s_proof() -> Result<(), Box<dyn std::error::Error>> {
        let public_jwk = P256PublicJwk {
            curve: "P-256".to_owned(),
            key_type: "EC".to_owned(),
            x: "axfR8uEsQkf4vOblY6RA8ncDfYEt6zOg9KE5RdiYwpY".to_owned(),
            y: "T-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU".to_owned(),
        };
        let scalar = URL_SAFE_NO_PAD.decode("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE")?;
        let key = SigningKey::from_slice(&scalar)?;
        let result = create_dpop_proof(
            &key,
            DpopInput {
                access_token: "harness-vector-access-token-not-a-secret",
                htm: "post",
                htu: "https://platform.example/gateway/v1/ws/tickets?ignored=yes#fragment",
                issued_at: 1_788_534_000,
                jti: "00000000-0000-4000-8000-000000000001",
                nonce: "vector-server-nonce-0001",
                public_jwk: &public_jwk,
            },
        )?;
        assert_eq!(result.htu, "https://platform.example/gateway/v1/ws/tickets");
        let parts: Vec<&str> = result.proof.split('.').collect();
        assert_eq!(parts.len(), 3);
        let signature = URL_SAFE_NO_PAD.decode(parts[2])?;
        assert!(verify_p1363_low_s(
            &public_jwk.verifying_key()?,
            parts[..2].join(".").as_bytes(),
            &signature
        ));
        Ok(())
    }

    #[test]
    fn rejects_plaintext_remote_target_and_missing_nonce() -> Result<(), Box<dyn std::error::Error>>
    {
        assert!(normalize_htu("http://platform.example/gateway").is_err());
        assert_eq!(
            normalize_htu("http://127.0.0.1:8082/gateway?ignored=yes")?,
            "http://127.0.0.1:8082/gateway"
        );
        let public_jwk = P256PublicJwk {
            curve: "P-256".to_owned(),
            key_type: "EC".to_owned(),
            x: "axfR8uEsQkf4vOblY6RA8ncDfYEt6zOg9KE5RdiYwpY".to_owned(),
            y: "T-NC4v4af5uO5-tKfA-eFivOM1drMV7Oy7ZAaDe_UfU".to_owned(),
        };
        let scalar = URL_SAFE_NO_PAD.decode("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAE")?;
        let key = SigningKey::from_slice(&scalar)?;
        assert!(
            create_dpop_proof(
                &key,
                DpopInput {
                    access_token: "token",
                    htm: "POST",
                    htu: "https://platform.example/gateway",
                    issued_at: 1,
                    jti: "00000000-0000-4000-8000-000000000001",
                    nonce: "",
                    public_jwk: &public_jwk,
                }
            )
            .is_err()
        );
        Ok(())
    }
}
