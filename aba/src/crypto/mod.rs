use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use p256::ecdsa::{
    Signature, SigningKey, VerifyingKey,
    signature::{Signer, Verifier},
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use thiserror::Error;

#[derive(Debug, Error)]
pub enum CryptoError {
    #[error("invalid base64url coordinate")]
    Base64(#[from] base64::DecodeError),
    #[error("JWK coordinate must be exactly 32 bytes")]
    CoordinateLength,
    #[error("JWK must use EC P-256")]
    Curve,
    #[error("JWK point is not on P-256")]
    Point,
    #[error("invalid DPoP input: {0}")]
    DpopInput(&'static str),
    #[error("serialize DPoP proof")]
    Json(#[from] serde_json::Error),
    #[error("invalid DPoP target URI")]
    Url(#[from] url::ParseError),
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct P256PublicJwk {
    #[serde(rename = "crv")]
    pub curve: String,
    #[serde(rename = "kty")]
    pub key_type: String,
    pub x: String,
    pub y: String,
}

impl P256PublicJwk {
    pub fn verifying_key(&self) -> Result<VerifyingKey, CryptoError> {
        if self.curve != "P-256" || self.key_type != "EC" {
            return Err(CryptoError::Curve);
        }
        let x = coordinate(&self.x)?;
        let y = coordinate(&self.y)?;
        let mut encoded = [0_u8; 65];
        encoded[0] = 4;
        encoded[1..33].copy_from_slice(&x);
        encoded[33..].copy_from_slice(&y);
        VerifyingKey::from_sec1_bytes(&encoded).map_err(|_| CryptoError::Point)
    }

    pub fn thumbprint(&self) -> Result<String, CryptoError> {
        self.verifying_key()?;
        let canonical = format!(
            r#"{{"crv":"{}","kty":"{}","x":"{}","y":"{}"}}"#,
            self.curve, self.key_type, self.x, self.y
        );
        Ok(URL_SAFE_NO_PAD.encode(Sha256::digest(canonical.as_bytes())))
    }
}

pub fn access_token_hash(token: &str) -> String {
    URL_SAFE_NO_PAD.encode(Sha256::digest(token.as_bytes()))
}

pub fn verify_p1363_low_s(key: &VerifyingKey, message: &[u8], signature_bytes: &[u8]) -> bool {
    let Ok(signature) = Signature::from_slice(signature_bytes) else {
        return false;
    };
    if signature.normalize_s().is_some() {
        return false;
    }
    key.verify(message, &signature).is_ok()
}

pub fn sign_p1363_low_s(key: &SigningKey, message: &[u8]) -> [u8; 64] {
    let signature: Signature = key.sign(message);
    let normalized = signature.normalize_s().unwrap_or(signature);
    normalized.to_bytes().into()
}

fn coordinate(value: &str) -> Result<[u8; 32], CryptoError> {
    let decoded = URL_SAFE_NO_PAD.decode(value)?;
    decoded
        .try_into()
        .map_err(|_| CryptoError::CoordinateLength)
}

pub mod ack;
pub mod dpop;
pub mod frame;
pub mod key_package;

#[cfg(test)]
mod tests {
    use super::*;

    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct Vector {
        fixture_use: String,
        jkt: String,
        public_jwk: P256PublicJwk,
        private_jwk: PrivateJwk,
        signature: SignatureVector,
        dpop: DpopVector,
    }

    #[derive(Deserialize)]
    struct PrivateJwk {
        d: String,
    }

    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct SignatureVector {
        high_s_p1363_base64_url: String,
        message_utf8: String,
        p1363_base64_url: String,
    }

    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct DpopVector {
        access_token: String,
        ath: String,
        proof: String,
        signing_input: String,
    }

    #[test]
    fn verifies_shared_jwk_and_es256_vector() -> Result<(), Box<dyn std::error::Error>> {
        let vector = vector()?;
        assert!(vector.fixture_use.starts_with("TEST ONLY"));
        assert_eq!(vector.public_jwk.thumbprint()?, vector.jkt);
        let key = vector.public_jwk.verifying_key()?;
        let signature = URL_SAFE_NO_PAD.decode(vector.signature.p1363_base64_url)?;
        assert!(verify_p1363_low_s(
            &key,
            vector.signature.message_utf8.as_bytes(),
            &signature
        ));
        assert!(!verify_p1363_low_s(&key, b"tampered", &signature));
        assert!(!verify_p1363_low_s(&key, b"tampered", &signature[..63]));
        let high_s = URL_SAFE_NO_PAD.decode(vector.signature.high_s_p1363_base64_url)?;
        assert!(!verify_p1363_low_s(
            &key,
            vector.signature.message_utf8.as_bytes(),
            &high_s
        ));

        let scalar = URL_SAFE_NO_PAD.decode(vector.private_jwk.d)?;
        let signing_key = SigningKey::from_slice(&scalar)?;
        let generated = sign_p1363_low_s(&signing_key, vector.signature.message_utf8.as_bytes());
        assert!(verify_p1363_low_s(
            &key,
            vector.signature.message_utf8.as_bytes(),
            &generated
        ));
        Ok(())
    }

    #[test]
    fn verifies_shared_dpop_primitives() -> Result<(), Box<dyn std::error::Error>> {
        let vector = vector()?;
        assert_eq!(
            access_token_hash(&vector.dpop.access_token),
            vector.dpop.ath
        );
        let parts: Vec<&str> = vector.dpop.proof.split('.').collect();
        assert_eq!(parts.len(), 3);
        assert_eq!(parts[..2].join("."), vector.dpop.signing_input);
        let signature = URL_SAFE_NO_PAD.decode(parts[2])?;
        assert!(verify_p1363_low_s(
            &vector.public_jwk.verifying_key()?,
            vector.dpop.signing_input.as_bytes(),
            &signature
        ));
        Ok(())
    }

    fn vector() -> Result<Vector, serde_json::Error> {
        serde_json::from_str(include_str!(
            "../../../protocol/testdata/v1/suite-0001-jwk-es256-dpop.json"
        ))
    }
}
