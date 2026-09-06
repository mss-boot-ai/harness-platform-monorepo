use crate::protocol::awpv1::{AckFrame, Direction, SequenceRange};

use super::{sign_p1363_low_s, verify_p1363_low_s};
use p256::ecdsa::{SigningKey, VerifyingKey};
use thiserror::Error;

const MAX_ACK_RANGES: usize = 32;

#[derive(Debug, Error)]
pub enum AckError {
    #[error("ACK input is invalid")]
    Invalid,
    #[error("ACK signature is invalid")]
    Signature,
}

pub fn sign_ack(frame: &mut AckFrame, key: &SigningKey) -> Result<(), AckError> {
    frame.signature.clear();
    frame.signature = sign_p1363_low_s(key, &ack_transcript(frame)?).to_vec();
    Ok(())
}

pub fn verify_ack(frame: &AckFrame, key: &VerifyingKey) -> Result<(), AckError> {
    if frame.signature.len() != 64
        || !verify_p1363_low_s(key, &ack_transcript(frame)?, &frame.signature)
    {
        return Err(AckError::Signature);
    }
    Ok(())
}

pub fn ack_transcript(frame: &AckFrame) -> Result<Vec<u8>, AckError> {
    if !id(&frame.ack_id)
        || !id(&frame.channel_id)
        || !id(&frame.session_id)
        || !id(&frame.endpoint_id)
        || !matches!(
            Direction::try_from(frame.acknowledged_direction),
            Ok(Direction::HcToAba | Direction::AbaToHc)
        )
        || frame.key_generation == 0
        || frame.created_at_ms <= 0
        || (frame.highest_contiguous_sequence == 0 && frame.received_ranges.is_empty())
        || !valid_ranges(frame.highest_contiguous_sequence, &frame.received_ranges)
    {
        return Err(AckError::Invalid);
    }
    let mut output = Vec::with_capacity(114 + frame.received_ranges.len() * 16);
    output.extend_from_slice(b"mss-awp-ack-v1");
    output.extend_from_slice(&frame.ack_id);
    output.extend_from_slice(&frame.channel_id);
    output.extend_from_slice(&frame.session_id);
    output.extend_from_slice(&frame.endpoint_id);
    output.push(u8::try_from(frame.acknowledged_direction).map_err(|_| AckError::Invalid)?);
    output.extend_from_slice(&[0; 7]);
    output.extend_from_slice(&frame.highest_contiguous_sequence.to_be_bytes());
    output.extend_from_slice(&frame.key_generation.to_be_bytes());
    output.extend_from_slice(&frame.created_at_ms.to_be_bytes());
    output.extend_from_slice(
        &u32::try_from(frame.received_ranges.len())
            .map_err(|_| AckError::Invalid)?
            .to_be_bytes(),
    );
    for value in &frame.received_ranges {
        output.extend_from_slice(&value.start.to_be_bytes());
        output.extend_from_slice(&value.end.to_be_bytes());
    }
    Ok(output)
}

fn valid_ranges(highest: u64, ranges: &[SequenceRange]) -> bool {
    if ranges.len() > MAX_ACK_RANGES {
        return false;
    }
    let mut previous = highest;
    for value in ranges {
        if value.start == 0 || value.start > value.end || value.start <= previous {
            return false;
        }
        previous = value.end;
    }
    true
}

fn id(value: &[u8]) -> bool {
    value.len() == 16 && value.iter().any(|byte| *byte != 0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn signs_exact_ack_transcript_and_rejects_overlap() -> Result<(), Box<dyn std::error::Error>> {
        let key = SigningKey::from_slice(&[7; 32])?;
        let mut frame = AckFrame {
            ack_id: vec![1; 16],
            channel_id: vec![2; 16],
            session_id: vec![3; 16],
            endpoint_id: vec![4; 16],
            acknowledged_direction: Direction::HcToAba as i32,
            highest_contiguous_sequence: 5,
            received_ranges: vec![
                SequenceRange { start: 7, end: 9 },
                SequenceRange { start: 11, end: 12 },
            ],
            key_generation: 2,
            created_at_ms: 1_800_000_000_000,
            signature: Vec::new(),
        };
        assert_eq!(ack_transcript(&frame)?.len(), 146);
        sign_ack(&mut frame, &key)?;
        verify_ack(&frame, key.verifying_key())?;
        frame.received_ranges[1].start = 9;
        assert!(matches!(ack_transcript(&frame), Err(AckError::Invalid)));
        Ok(())
    }
}
