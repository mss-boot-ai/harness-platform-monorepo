use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use hkdf::Hkdf;
use hpke::{
    Deserializable as _, Kem as KemTrait, OpModeS, Serializable as _, aead::AesGcm256,
    kdf::HkdfSha256, kem::DhP256HkdfSha256, single_shot_seal,
};
use sha2::{Digest as _, Sha256};
use thiserror::Error;
use zeroize::{Zeroize, ZeroizeOnDrop, Zeroizing};

use super::P256PublicJwk;

pub const SUITE_ID: u16 = 1;
pub const SUITE_NAME: &str = "MSS-AWP-SUITE-0001";
pub const KEY_PACKAGE_INFO_BYTES: usize = 84;
pub const KEY_PACKAGE_PLAINTEXT_BYTES: usize = 157;

type Kem = DhP256HkdfSha256;

#[derive(Zeroize, ZeroizeOnDrop)]
pub struct KeyPackageMaterial {
    pub session_id: [u8; 16],
    pub generation: u64,
    pub sender_aba_endpoint_id: [u8; 16],
    pub recipient_hc_endpoint_id: [u8; 16],
    pub policy_revision: u64,
    pub key_id: [u8; 16],
    pub srk: [u8; 32],
    pub session_nonce: [u8; 32],
    pub hc_to_aba_nonce_prefix: [u8; 4],
    pub aba_to_hc_nonce_prefix: [u8; 4],
    pub not_before_ms: i64,
    pub expires_at_ms: i64,
}

pub struct SealedKeyPackage {
    pub enc: Vec<u8>,
    pub ciphertext: Vec<u8>,
    pub context_hash: [u8; 32],
}

#[derive(Zeroize, ZeroizeOnDrop)]
pub struct SessionDirectionKeys {
    pub hc_to_aba: [u8; 32],
    pub aba_to_hc: [u8; 32],
}

pub struct KeyPackageEnvelope<'a> {
    pub key_package_id: &'a [u8],
    pub session_id: &'a [u8],
    pub generation: u64,
    pub issuer_aba_endpoint_id: &'a [u8],
    pub recipient_hc_endpoint_id: &'a [u8],
    pub issuer_credential_id: &'a [u8],
    pub policy_revision: u64,
    pub not_before_ms: i64,
    pub expires_at_ms: i64,
    pub hpke_enc: &'a [u8],
    pub hpke_ciphertext: &'a [u8],
}

#[derive(Debug, Error)]
pub enum KeyPackageError {
    #[error("key package input is invalid")]
    InvalidInput,
    #[error("key package recipient key is invalid")]
    RecipientKey,
    #[error("HPKE operation failed")]
    Hpke,
}

pub fn seal_key_package(
    recipient: &P256PublicJwk,
    material: &KeyPackageMaterial,
) -> Result<SealedKeyPackage, KeyPackageError> {
    validate_material(material)?;
    recipient
        .verifying_key()
        .map_err(|_| KeyPackageError::RecipientKey)?;
    let recipient_public = recipient_public_bytes(recipient)?;
    let public_key = <Kem as KemTrait>::PublicKey::from_bytes(&recipient_public)
        .map_err(|_| KeyPackageError::RecipientKey)?;
    let info = key_package_info(material)?;
    let plaintext = Zeroizing::new(key_package_plaintext(material)?);
    let (enc, ciphertext) = single_shot_seal::<AesGcm256, HkdfSha256, Kem>(
        &OpModeS::Base,
        &public_key,
        &info,
        &plaintext,
        &info,
    )
    .map_err(|_| KeyPackageError::Hpke)?;
    Ok(SealedKeyPackage {
        enc: enc.to_bytes().to_vec(),
        ciphertext,
        context_hash: Sha256::digest(&info).into(),
    })
}

pub fn p256_public_jwk_from_sec1(
    encoded: &[u8],
    expected_jkt: &str,
) -> Result<P256PublicJwk, KeyPackageError> {
    if encoded.len() != 65 || encoded[0] != 4 || expected_jkt.is_empty() {
        return Err(KeyPackageError::RecipientKey);
    }
    let jwk = P256PublicJwk {
        curve: "P-256".to_owned(),
        key_type: "EC".to_owned(),
        x: URL_SAFE_NO_PAD.encode(&encoded[1..33]),
        y: URL_SAFE_NO_PAD.encode(&encoded[33..65]),
    };
    if jwk
        .thumbprint()
        .map_err(|_| KeyPackageError::RecipientKey)?
        != expected_jkt
    {
        return Err(KeyPackageError::RecipientKey);
    }
    Ok(jwk)
}

pub fn key_package_info(material: &KeyPackageMaterial) -> Result<Vec<u8>, KeyPackageError> {
    validate_material(material)?;
    let mut output = Vec::with_capacity(KEY_PACKAGE_INFO_BYTES);
    output.extend_from_slice(b"mss-key-package-v1");
    output.extend_from_slice(&material.session_id);
    output.extend_from_slice(&material.generation.to_be_bytes());
    output.extend_from_slice(&material.sender_aba_endpoint_id);
    output.extend_from_slice(&material.recipient_hc_endpoint_id);
    output.extend_from_slice(&SUITE_ID.to_be_bytes());
    output.extend_from_slice(&material.policy_revision.to_be_bytes());
    if output.len() != KEY_PACKAGE_INFO_BYTES {
        return Err(KeyPackageError::InvalidInput);
    }
    Ok(output)
}

pub fn key_package_plaintext(material: &KeyPackageMaterial) -> Result<Vec<u8>, KeyPackageError> {
    validate_material(material)?;
    let mut output = Vec::with_capacity(KEY_PACKAGE_PLAINTEXT_BYTES);
    output.extend_from_slice(b"mss-key-package-plaintext-v1");
    output.extend_from_slice(&material.session_id);
    output.extend_from_slice(&material.generation.to_be_bytes());
    output.extend_from_slice(&material.key_id);
    output.extend_from_slice(&material.srk);
    output.extend_from_slice(&material.session_nonce);
    output.extend_from_slice(&material.hc_to_aba_nonce_prefix);
    output.extend_from_slice(&material.aba_to_hc_nonce_prefix);
    output.extend_from_slice(&material.not_before_ms.to_be_bytes());
    output.extend_from_slice(&material.expires_at_ms.to_be_bytes());
    output.push(1);
    if output.len() != KEY_PACKAGE_PLAINTEXT_BYTES {
        return Err(KeyPackageError::InvalidInput);
    }
    Ok(output)
}

pub fn key_package_envelope_transcript(
    envelope: KeyPackageEnvelope<'_>,
) -> Result<Vec<u8>, KeyPackageError> {
    if envelope.key_package_id.len() != 16
        || envelope.session_id.len() != 16
        || envelope.generation == 0
        || envelope.issuer_aba_endpoint_id.len() != 16
        || envelope.recipient_hc_endpoint_id.len() != 16
        || envelope.issuer_credential_id.len() != 16
        || envelope.policy_revision == 0
        || envelope.not_before_ms <= 0
        || envelope.expires_at_ms <= envelope.not_before_ms
        || envelope.hpke_enc.len() != 65
        || envelope.hpke_ciphertext.is_empty()
        || envelope.hpke_ciphertext.len() > 16 * 1024
    {
        return Err(KeyPackageError::InvalidInput);
    }
    let enc_length =
        u32::try_from(envelope.hpke_enc.len()).map_err(|_| KeyPackageError::InvalidInput)?;
    let ciphertext_length =
        u32::try_from(envelope.hpke_ciphertext.len()).map_err(|_| KeyPackageError::InvalidInput)?;
    let mut output = b"mss-key-package-envelope-v1".to_vec();
    output.extend_from_slice(envelope.key_package_id);
    output.extend_from_slice(envelope.session_id);
    output.extend_from_slice(&envelope.generation.to_be_bytes());
    output.extend_from_slice(envelope.issuer_aba_endpoint_id);
    output.extend_from_slice(envelope.recipient_hc_endpoint_id);
    output.extend_from_slice(envelope.issuer_credential_id);
    output.extend_from_slice(&SUITE_ID.to_be_bytes());
    output.extend_from_slice(&envelope.policy_revision.to_be_bytes());
    output.extend_from_slice(&envelope.not_before_ms.to_be_bytes());
    output.extend_from_slice(&envelope.expires_at_ms.to_be_bytes());
    output.extend_from_slice(&enc_length.to_be_bytes());
    output.extend_from_slice(envelope.hpke_enc);
    output.extend_from_slice(&ciphertext_length.to_be_bytes());
    output.extend_from_slice(envelope.hpke_ciphertext);
    Ok(output)
}

pub fn derive_session_direction_keys(
    material: &KeyPackageMaterial,
) -> Result<SessionDirectionKeys, KeyPackageError> {
    validate_material(material)?;
    let hkdf = Hkdf::<Sha256>::new(Some(&material.session_nonce), &material.srk);
    let prefix = format!(
        "mss-awp/v1/session/{}/generation/{}/endpoint/{}",
        lower_hex(&material.session_id),
        material.generation,
        lower_hex(&material.recipient_hc_endpoint_id),
    );
    let mut keys = SessionDirectionKeys {
        hc_to_aba: [0_u8; 32],
        aba_to_hc: [0_u8; 32],
    };
    hkdf.expand(
        format!("{prefix}/hc-to-aba").as_bytes(),
        &mut keys.hc_to_aba,
    )
    .map_err(|_| KeyPackageError::InvalidInput)?;
    hkdf.expand(
        format!("{prefix}/aba-to-hc").as_bytes(),
        &mut keys.aba_to_hc,
    )
    .map_err(|_| KeyPackageError::InvalidInput)?;
    Ok(keys)
}

fn validate_material(material: &KeyPackageMaterial) -> Result<(), KeyPackageError> {
    if material.session_id.iter().all(|value| *value == 0)
        || material.generation == 0
        || material
            .sender_aba_endpoint_id
            .iter()
            .all(|value| *value == 0)
        || material
            .recipient_hc_endpoint_id
            .iter()
            .all(|value| *value == 0)
        || material.sender_aba_endpoint_id == material.recipient_hc_endpoint_id
        || material.policy_revision == 0
        || material.key_id.iter().all(|value| *value == 0)
        || material.srk.iter().all(|value| *value == 0)
        || material.session_nonce.iter().all(|value| *value == 0)
        || material.hc_to_aba_nonce_prefix == material.aba_to_hc_nonce_prefix
        || material.not_before_ms <= 0
        || material.expires_at_ms <= material.not_before_ms
    {
        return Err(KeyPackageError::InvalidInput);
    }
    Ok(())
}

fn recipient_public_bytes(recipient: &P256PublicJwk) -> Result<[u8; 65], KeyPackageError> {
    let x = URL_SAFE_NO_PAD
        .decode(&recipient.x)
        .map_err(|_| KeyPackageError::RecipientKey)?;
    let y = URL_SAFE_NO_PAD
        .decode(&recipient.y)
        .map_err(|_| KeyPackageError::RecipientKey)?;
    if x.len() != 32 || y.len() != 32 {
        return Err(KeyPackageError::RecipientKey);
    }
    let mut output = [0_u8; 65];
    output[0] = 4;
    output[1..33].copy_from_slice(&x);
    output[33..].copy_from_slice(&y);
    Ok(output)
}

fn lower_hex(value: &[u8]) -> String {
    let mut output = String::with_capacity(value.len() * 2);
    for byte in value {
        use std::fmt::Write as _;
        let _ = write!(output, "{byte:02x}");
    }
    output
}

#[cfg(test)]
mod tests {
    use super::*;
    use hpke::{OpModeR, setup_receiver};
    use serde::Deserialize;
    use url::Url;

    use crate::identity::DevFileKeyStore;

    #[test]
    fn seals_and_opens_bound_suite_0001_material() -> Result<(), Box<dyn std::error::Error>> {
        let directory = tempfile::tempdir()?;
        let platform = Url::parse("http://127.0.0.1:8082")?;
        let store = DevFileKeyStore::new(directory.path().join("identity.json"), &platform, true)?;
        store.initialize()?;
        let recipient = store.load()?;
        let material = fixture_material();
        let info = key_package_info(&material)?;
        let expected = key_package_plaintext(&material)?;
        let sealed = seal_key_package(&recipient.kem_public_jwk()?, &material)?;
        assert_eq!(sealed.enc.len(), 65);
        assert_eq!(sealed.ciphertext.len(), KEY_PACKAGE_PLAINTEXT_BYTES + 16);
        let expected_hash: [u8; 32] = Sha256::digest(&info).into();
        assert_eq!(sealed.context_hash, expected_hash);

        let private_bytes = recipient.kem_key().to_bytes();
        let private_key = <Kem as KemTrait>::PrivateKey::from_bytes(private_bytes.as_ref())?;
        let enc = <Kem as KemTrait>::EncappedKey::from_bytes(&sealed.enc)?;
        let mut receiver = setup_receiver::<AesGcm256, HkdfSha256, Kem>(
            &OpModeR::Base,
            &private_key,
            &enc,
            &info,
        )?;
        assert_eq!(receiver.open(&sealed.ciphertext, &info)?, expected);
        let mut tampered = info.clone();
        tampered[0] ^= 1;
        let mut receiver = setup_receiver::<AesGcm256, HkdfSha256, Kem>(
            &OpModeR::Base,
            &private_key,
            &enc,
            &info,
        )?;
        assert!(receiver.open(&sealed.ciphertext, &tampered).is_err());
        let keys = derive_session_direction_keys(&material)?;
        assert_ne!(keys.hc_to_aba, keys.aba_to_hc);
        Ok(())
    }

    fn fixture_material() -> KeyPackageMaterial {
        KeyPackageMaterial {
            session_id: [1_u8; 16],
            generation: 1,
            sender_aba_endpoint_id: [2_u8; 16],
            recipient_hc_endpoint_id: [3_u8; 16],
            policy_revision: 1,
            key_id: [8_u8; 16],
            srk: [4_u8; 32],
            session_nonce: [5_u8; 32],
            hc_to_aba_nonce_prefix: [6_u8; 4],
            aba_to_hc_nonce_prefix: [7_u8; 4],
            not_before_ms: 1_800_000_000_000,
            expires_at_ms: 1_800_003_600_000,
        }
    }

    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct Vector {
        fixture_use: String,
        suite_id: u16,
        recipient: VectorRecipient,
        info_base64_url: String,
        plaintext_base64_url: String,
        enc_base64_url: String,
        ciphertext_base64_url: String,
        context_hash_base64_url: String,
        direction_keys: VectorDirectionKeys,
    }

    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct VectorRecipient {
        private_d: String,
    }

    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct VectorDirectionKeys {
        hc_to_aba_base64_url: String,
        aba_to_hc_base64_url: String,
    }

    #[test]
    fn opens_shared_suite_0001_vector() -> Result<(), Box<dyn std::error::Error>> {
        let vector: Vector = serde_json::from_str(include_str!(
            "../../../protocol/testdata/v1/suite-0001-hpke-session.json"
        ))?;
        assert!(vector.fixture_use.starts_with("TEST ONLY"));
        assert_eq!(vector.suite_id, SUITE_ID);
        let material = fixture_material();
        let info = key_package_info(&material)?;
        assert_eq!(URL_SAFE_NO_PAD.encode(&info), vector.info_base64_url);
        assert_eq!(
            URL_SAFE_NO_PAD.encode(key_package_plaintext(&material)?),
            vector.plaintext_base64_url
        );
        assert_eq!(
            URL_SAFE_NO_PAD.encode(Sha256::digest(&info)),
            vector.context_hash_base64_url
        );
        let private = URL_SAFE_NO_PAD.decode(vector.recipient.private_d)?;
        let private_key = <Kem as KemTrait>::PrivateKey::from_bytes(&private)?;
        let enc_bytes = URL_SAFE_NO_PAD.decode(vector.enc_base64_url)?;
        let enc = <Kem as KemTrait>::EncappedKey::from_bytes(&enc_bytes)?;
        let ciphertext = URL_SAFE_NO_PAD.decode(vector.ciphertext_base64_url)?;
        let mut receiver = setup_receiver::<AesGcm256, HkdfSha256, Kem>(
            &OpModeR::Base,
            &private_key,
            &enc,
            &info,
        )?;
        assert_eq!(
            receiver.open(&ciphertext, &info)?,
            key_package_plaintext(&material)?
        );
        let keys = derive_session_direction_keys(&material)?;
        assert_eq!(
            URL_SAFE_NO_PAD.encode(keys.hc_to_aba),
            vector.direction_keys.hc_to_aba_base64_url
        );
        assert_eq!(
            URL_SAFE_NO_PAD.encode(keys.aba_to_hc),
            vector.direction_keys.aba_to_hc_base64_url
        );
        Ok(())
    }
}
