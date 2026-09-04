use p256::ecdsa::{SigningKey, VerifyingKey};
use thiserror::Error;

use crate::protocol::awpv1::ErrorFrame;

use super::{sign_p1363_low_s, verify_p1363_low_s};

const MAX_SAFE_MESSAGE_BYTES: usize = 256;

#[derive(Debug, Error)]
pub enum ErrorFrameError {
    #[error("ErrorFrame input is invalid")]
    Invalid,
    #[error("ErrorFrame signature is invalid")]
    Signature,
}

pub fn sign_error_frame(frame: &mut ErrorFrame, key: &SigningKey) -> Result<(), ErrorFrameError> {
    frame.signature.clear();
    frame.signature = sign_p1363_low_s(key, &error_frame_transcript(frame)?).to_vec();
    Ok(())
}

pub fn verify_error_frame(frame: &ErrorFrame, key: &VerifyingKey) -> Result<(), ErrorFrameError> {
    if frame.signature.len() != 64
        || !verify_p1363_low_s(key, &error_frame_transcript(frame)?, &frame.signature)
    {
        return Err(ErrorFrameError::Signature);
    }
    Ok(())
}

pub fn error_frame_transcript(frame: &ErrorFrame) -> Result<Vec<u8>, ErrorFrameError> {
    if !id(&frame.error_id)
        || frame.related_message_id.len() != 16
        || frame.code <= 0
        || frame.safe_message.is_empty()
        || frame.safe_message.len() > MAX_SAFE_MESSAGE_BYTES
        || frame.safe_message.chars().any(char::is_control)
        || (!frame.retryable && frame.retry_after_ms != 0)
    {
        return Err(ErrorFrameError::Invalid);
    }
    let mut output = Vec::with_capacity(64 + frame.safe_message.len());
    output.extend_from_slice(b"mss-awp-error-v1");
    output.extend_from_slice(&frame.error_id);
    output.extend_from_slice(&frame.related_message_id);
    output.extend_from_slice(
        &u32::try_from(frame.code)
            .map_err(|_| ErrorFrameError::Invalid)?
            .to_be_bytes(),
    );
    output.push(u8::from(frame.retryable));
    output.extend_from_slice(&[0; 3]);
    output.extend_from_slice(&frame.retry_after_ms.to_be_bytes());
    output.extend_from_slice(
        &u32::try_from(frame.safe_message.len())
            .map_err(|_| ErrorFrameError::Invalid)?
            .to_be_bytes(),
    );
    output.extend_from_slice(frame.safe_message.as_bytes());
    Ok(output)
}

fn id(value: &[u8]) -> bool {
    value.len() == 16 && value.iter().any(|byte| *byte != 0)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::protocol::awpv1::ErrorCode;

    #[test]
    fn signs_uncertain_error_without_payload_content() -> Result<(), Box<dyn std::error::Error>> {
        let key = SigningKey::from_slice(&[8; 32])?;
        let mut frame = ErrorFrame {
            error_id: vec![1; 16],
            related_message_id: vec![2; 16],
            code: ErrorCode::LocalDispatchUncertain as i32,
            retryable: false,
            retry_after_ms: 0,
            safe_message: "Local agent dispatch result is uncertain".to_owned(),
            signature: Vec::new(),
        };
        sign_error_frame(&mut frame, &key)?;
        verify_error_frame(&frame, key.verifying_key())?;
        frame.safe_message.push('!');
        assert!(matches!(
            verify_error_frame(&frame, key.verifying_key()),
            Err(ErrorFrameError::Signature)
        ));
        Ok(())
    }
}
