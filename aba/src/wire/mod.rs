mod aad;

pub use aad::{
    AAD_V1_LENGTH, ACP_BATCH_FLAG, CRITICAL_FLAG_MASK, CryptoSuite, Direction, FrameAadV1,
    FrameType, WireValidationError,
};

pub const WIRE_MAJOR: u16 = 1;
pub const WIRE_MINOR: u16 = 0;
pub const MAX_WIRE_PACKET_BYTES: u32 = 1_048_576;
