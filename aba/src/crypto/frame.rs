use aes_gcm::{
    Aes256Gcm,
    aead::{Aead as _, KeyInit as _, Payload, array::Array},
};
use p256::ecdsa::{SigningKey, VerifyingKey};
use sha2::{Digest as _, Sha256};
use thiserror::Error;
use zeroize::Zeroizing;

use super::{sign_p1363_low_s, verify_p1363_low_s};
use crate::wire::{FrameAadV1, WireValidationError};

pub struct ProtectedFrame {
    pub aad: [u8; 148],
    pub ciphertext: Vec<u8>,
    pub signature: [u8; 64],
}

#[derive(Debug, Error)]
pub enum FrameCryptoError {
    #[error("frame input is invalid")]
    InvalidInput,
    #[error("frame AAD is invalid")]
    Aad(#[from] WireValidationError),
    #[error("frame signature is invalid")]
    Signature,
    #[error("frame AEAD operation failed")]
    Aead,
}

pub fn seal_frame(
    key: &[u8; 32],
    nonce_prefix: &[u8; 4],
    signing_key: &SigningKey,
    mut aad: FrameAadV1,
    plaintext: &[u8],
) -> Result<ProtectedFrame, FrameCryptoError> {
    if plaintext.is_empty() || plaintext.len() > (1 << 20) - 16 {
        return Err(FrameCryptoError::InvalidInput);
    }
    aad.ciphertext_length =
        u32::try_from(plaintext.len() + 16).map_err(|_| FrameCryptoError::InvalidInput)?;
    let encoded_aad = aad.encode()?;
    let nonce = frame_nonce(nonce_prefix, aad.sequence)?;
    let cipher = Aes256Gcm::new(&Array(*key));
    let ciphertext = cipher
        .encrypt(
            &Array(nonce),
            Payload {
                msg: plaintext,
                aad: &encoded_aad,
            },
        )
        .map_err(|_| FrameCryptoError::Aead)?;
    let signature = sign_p1363_low_s(
        signing_key,
        &frame_signature_input(&encoded_aad, &ciphertext),
    );
    Ok(ProtectedFrame {
        aad: encoded_aad,
        ciphertext,
        signature,
    })
}

pub fn open_frame(
    key: &[u8; 32],
    nonce_prefix: &[u8; 4],
    verifying_key: &VerifyingKey,
    aad: &FrameAadV1,
    ciphertext: &[u8],
    signature: &[u8],
) -> Result<Zeroizing<Vec<u8>>, FrameCryptoError> {
    let encoded_aad = aad.encode()?;
    if usize::try_from(aad.ciphertext_length).ok() != Some(ciphertext.len())
        || ciphertext.len() < 16
    {
        return Err(FrameCryptoError::InvalidInput);
    }
    if !verify_p1363_low_s(
        verifying_key,
        &frame_signature_input(&encoded_aad, ciphertext),
        signature,
    ) {
        return Err(FrameCryptoError::Signature);
    }
    let nonce = frame_nonce(nonce_prefix, aad.sequence)?;
    let cipher = Aes256Gcm::new(&Array(*key));
    let plaintext = cipher
        .decrypt(
            &Array(nonce),
            Payload {
                msg: ciphertext,
                aad: &encoded_aad,
            },
        )
        .map_err(|_| FrameCryptoError::Aead)?;
    Ok(Zeroizing::new(plaintext))
}

pub fn frame_nonce(prefix: &[u8; 4], sequence: u64) -> Result<[u8; 12], FrameCryptoError> {
    if sequence == 0 {
        return Err(FrameCryptoError::InvalidInput);
    }
    let mut nonce = [0_u8; 12];
    nonce[..4].copy_from_slice(prefix);
    nonce[4..].copy_from_slice(&sequence.to_be_bytes());
    Ok(nonce)
}

pub fn session_channel_id(
    session_id: &[u8; 16],
    aba_endpoint_id: &[u8; 16],
    hc_endpoint_id: &[u8; 16],
) -> Result<[u8; 16], FrameCryptoError> {
    if session_id.iter().all(|value| *value == 0)
        || aba_endpoint_id.iter().all(|value| *value == 0)
        || hc_endpoint_id.iter().all(|value| *value == 0)
        || aba_endpoint_id == hc_endpoint_id
    {
        return Err(FrameCryptoError::InvalidInput);
    }
    let mut digest = Sha256::new();
    digest.update(b"mss-awp-channel-v1");
    digest.update(session_id);
    digest.update(aba_endpoint_id);
    digest.update(hc_endpoint_id);
    let result = digest.finalize();
    let mut id = [0_u8; 16];
    id.copy_from_slice(&result[..16]);
    Ok(id)
}

pub fn frame_content_hash(aad: &[u8; 148], ciphertext: &[u8], signature: &[u8]) -> [u8; 32] {
    let mut digest = Sha256::new();
    digest.update(aad);
    digest.update(ciphertext);
    digest.update(signature);
    digest.finalize().into()
}

fn frame_signature_input(aad: &[u8; 148], ciphertext: &[u8]) -> Vec<u8> {
    let mut input = Vec::with_capacity(26 + aad.len() + ciphertext.len());
    input.extend_from_slice(b"mss-awp-frame-signature-v1");
    input.extend_from_slice(aad);
    input.extend_from_slice(ciphertext);
    input
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::wire::{CryptoSuite, Direction, FrameType};

    #[test]
    fn seals_verifies_and_opens_frame() -> Result<(), Box<dyn std::error::Error>> {
        let signing = SigningKey::from_slice(&[9_u8; 32])?;
        let aad = FrameAadV1 {
            crypto_suite: CryptoSuite::Suite0001,
            frame_type: FrameType::AcpTransportFrame,
            flags: 0,
            message_id: [1_u8; 16],
            channel_id: session_channel_id(&[2_u8; 16], &[3_u8; 16], &[4_u8; 16])?,
            session_id: [2_u8; 16],
            sender_endpoint_id: [4_u8; 16],
            receiver_endpoint_id: [3_u8; 16],
            direction: Direction::HcToAba,
            sequence: 1,
            key_generation: 1,
            key_id: [5_u8; 16],
            created_at_ms: 1_800_000_000_000,
            ciphertext_length: 0,
        };
        let plaintext =
            br#"{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1}}"#;
        let protected = seal_frame(&[6_u8; 32], &[7_u8; 4], &signing, aad.clone(), plaintext)?;
        let mut opened_aad = aad;
        opened_aad.ciphertext_length = u32::try_from(protected.ciphertext.len())?;
        assert_eq!(
            open_frame(
                &[6_u8; 32],
                &[7_u8; 4],
                signing.verifying_key(),
                &opened_aad,
                &protected.ciphertext,
                &protected.signature,
            )?
            .as_slice(),
            plaintext
        );
        let mut tampered = protected.ciphertext.clone();
        tampered[0] ^= 1;
        assert!(matches!(
            open_frame(
                &[6_u8; 32],
                &[7_u8; 4],
                signing.verifying_key(),
                &opened_aad,
                &tampered,
                &protected.signature,
            ),
            Err(FrameCryptoError::Signature)
        ));
        Ok(())
    }
}
