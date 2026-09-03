use thiserror::Error;

use super::{MAX_WIRE_PACKET_BYTES, WIRE_MAJOR, WIRE_MINOR};

pub const AAD_V1_LENGTH: usize = 148;
pub const ACP_BATCH_FLAG: u32 = 1;
pub const CRITICAL_FLAG_MASK: u32 = 0xffff_0000;

const MAGIC: [u8; 4] = *b"AWP1";
const ID_LENGTH: usize = 16;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u16)]
pub enum CryptoSuite {
    Suite0001 = 1,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u16)]
pub enum FrameType {
    AcpTransportFrame = 1,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
#[repr(u8)]
pub enum Direction {
    HcToAba = 1,
    AbaToHc = 2,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct FrameAadV1 {
    pub crypto_suite: CryptoSuite,
    pub frame_type: FrameType,
    pub flags: u32,
    pub message_id: [u8; ID_LENGTH],
    pub channel_id: [u8; ID_LENGTH],
    pub session_id: [u8; ID_LENGTH],
    pub sender_endpoint_id: [u8; ID_LENGTH],
    pub receiver_endpoint_id: [u8; ID_LENGTH],
    pub direction: Direction,
    pub sequence: u64,
    pub key_generation: u64,
    pub key_id: [u8; ID_LENGTH],
    pub created_at_ms: i64,
    pub ciphertext_length: u32,
}

#[derive(Debug, Error, Clone, PartialEq, Eq)]
pub enum WireValidationError {
    #[error("AWP v1 does not recognize one or more critical flag bits")]
    UnknownCriticalFlags,
    #[error("AWP v1 sequence must be greater than zero")]
    InvalidSequence,
    #[error("AWP v1 key generation must be greater than zero")]
    InvalidKeyGeneration,
    #[error("AWP v1 identifiers must not be all zero")]
    ZeroIdentifier,
    #[error("AWP v1 creation timestamp must not be negative")]
    InvalidTimestamp,
    #[error("AWP v1 ciphertext exceeds the maximum packet size")]
    CiphertextTooLarge,
}

impl FrameAadV1 {
    pub fn encode(&self) -> Result<[u8; AAD_V1_LENGTH], WireValidationError> {
        self.validate()?;

        let mut encoded = [0_u8; AAD_V1_LENGTH];
        encoded[0..4].copy_from_slice(&MAGIC);
        encoded[4..6].copy_from_slice(&WIRE_MAJOR.to_be_bytes());
        encoded[6..8].copy_from_slice(&WIRE_MINOR.to_be_bytes());
        encoded[8..10].copy_from_slice(&(self.crypto_suite as u16).to_be_bytes());
        encoded[10..12].copy_from_slice(&(self.frame_type as u16).to_be_bytes());
        encoded[12..16].copy_from_slice(&self.flags.to_be_bytes());
        encoded[16..32].copy_from_slice(&self.message_id);
        encoded[32..48].copy_from_slice(&self.channel_id);
        encoded[48..64].copy_from_slice(&self.session_id);
        encoded[64..80].copy_from_slice(&self.sender_endpoint_id);
        encoded[80..96].copy_from_slice(&self.receiver_endpoint_id);
        encoded[96] = self.direction as u8;
        // Bytes 97..104 are reserved and remain zero.
        encoded[104..112].copy_from_slice(&self.sequence.to_be_bytes());
        encoded[112..120].copy_from_slice(&self.key_generation.to_be_bytes());
        encoded[120..136].copy_from_slice(&self.key_id);
        encoded[136..144].copy_from_slice(&self.created_at_ms.to_be_bytes());
        encoded[144..148].copy_from_slice(&self.ciphertext_length.to_be_bytes());
        Ok(encoded)
    }

    fn validate(&self) -> Result<(), WireValidationError> {
        if self.flags & CRITICAL_FLAG_MASK != 0 {
            return Err(WireValidationError::UnknownCriticalFlags);
        }
        if self.sequence == 0 {
            return Err(WireValidationError::InvalidSequence);
        }
        if self.key_generation == 0 {
            return Err(WireValidationError::InvalidKeyGeneration);
        }
        if [
            &self.message_id,
            &self.channel_id,
            &self.session_id,
            &self.sender_endpoint_id,
            &self.receiver_endpoint_id,
            &self.key_id,
        ]
        .into_iter()
        .any(|id| id.iter().all(|byte| *byte == 0))
        {
            return Err(WireValidationError::ZeroIdentifier);
        }
        if self.created_at_ms < 0 {
            return Err(WireValidationError::InvalidTimestamp);
        }
        if self.ciphertext_length > MAX_WIRE_PACKET_BYTES {
            return Err(WireValidationError::CiphertextTooLarge);
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::{
        ACP_BATCH_FLAG, AAD_V1_LENGTH, CRITICAL_FLAG_MASK, CryptoSuite, Direction, FrameAadV1,
        FrameType, WireValidationError,
    };

    fn valid_aad() -> FrameAadV1 {
        FrameAadV1 {
            crypto_suite: CryptoSuite::Suite0001,
            frame_type: FrameType::AcpTransportFrame,
            flags: ACP_BATCH_FLAG,
            message_id: [0x11; 16],
            channel_id: [0x22; 16],
            session_id: [0x33; 16],
            sender_endpoint_id: [0x44; 16],
            receiver_endpoint_id: [0x55; 16],
            direction: Direction::HcToAba,
            sequence: 0x0102_0304_0506_0708,
            key_generation: 0x1112_1314_1516_1718,
            key_id: [0x66; 16],
            created_at_ms: 0x2122_2324_2526_2728,
            ciphertext_length: 0x0001_0203,
        }
    }

    #[test]
    fn encodes_every_field_at_the_documented_offset() {
        let encoded = match valid_aad().encode() {
            Ok(value) => value,
            Err(error) => {
                assert!(false, "valid AAD did not encode: {error}");
                return;
            }
        };

        assert_eq!(encoded.len(), AAD_V1_LENGTH);
        assert_eq!(&encoded[0..4], b"AWP1");
        assert_eq!(&encoded[4..6], &[0, 1]);
        assert_eq!(&encoded[6..8], &[0, 0]);
        assert_eq!(&encoded[8..10], &[0, 1]);
        assert_eq!(&encoded[10..12], &[0, 1]);
        assert_eq!(&encoded[12..16], &ACP_BATCH_FLAG.to_be_bytes());
        assert_eq!(&encoded[16..32], &[0x11; 16]);
        assert_eq!(&encoded[32..48], &[0x22; 16]);
        assert_eq!(&encoded[48..64], &[0x33; 16]);
        assert_eq!(&encoded[64..80], &[0x44; 16]);
        assert_eq!(&encoded[80..96], &[0x55; 16]);
        assert_eq!(encoded[96], Direction::HcToAba as u8);
        assert_eq!(&encoded[97..104], &[0; 7]);
        assert_eq!(&encoded[104..112], &0x0102_0304_0506_0708_u64.to_be_bytes());
        assert_eq!(&encoded[112..120], &0x1112_1314_1516_1718_u64.to_be_bytes());
        assert_eq!(&encoded[120..136], &[0x66; 16]);
        assert_eq!(&encoded[136..144], &0x2122_2324_2526_2728_i64.to_be_bytes());
        assert_eq!(&encoded[144..148], &0x0001_0203_u32.to_be_bytes());
    }

    #[test]
    fn rejects_unknown_critical_flags() {
        let mut aad = valid_aad();
        aad.flags |= CRITICAL_FLAG_MASK;
        assert_eq!(
            aad.encode(),
            Err(WireValidationError::UnknownCriticalFlags)
        );
    }

    #[test]
    fn rejects_zero_sequence_and_generation() {
        let mut aad = valid_aad();
        aad.sequence = 0;
        assert_eq!(aad.encode(), Err(WireValidationError::InvalidSequence));

        let mut aad = valid_aad();
        aad.key_generation = 0;
        assert_eq!(
            aad.encode(),
            Err(WireValidationError::InvalidKeyGeneration)
        );
    }

    #[test]
    fn rejects_zero_identifiers() {
        let mut aad = valid_aad();
        aad.key_id = [0; 16];
        assert_eq!(aad.encode(), Err(WireValidationError::ZeroIdentifier));
    }

    #[test]
    fn supports_both_protocol_directions() {
        let mut aad = valid_aad();
        aad.direction = Direction::AbaToHc;
        let encoded = aad.encode();
        assert!(matches!(encoded, Ok(value) if value[96] == Direction::AbaToHc as u8));
    }
}
