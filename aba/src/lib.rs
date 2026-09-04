#![forbid(unsafe_code)]

pub mod config;
pub mod crypto;
pub mod gateway;
pub mod identity;
pub mod protocol {
    pub mod awpv1 {
        include!(concat!(env!("OUT_DIR"), "/mss.awp.v1.rs"));
    }
}
pub mod version;
pub mod wire;

#[cfg(test)]
mod protocol_tests {
    use base64::{Engine as _, engine::general_purpose::STANDARD};
    use prost::Message as _;
    use serde::Deserialize;

    use crate::protocol::awpv1::{WirePacket, wire_packet};

    #[derive(Deserialize)]
    #[serde(rename_all = "camelCase")]
    struct ChallengeVector {
        fixture_use: String,
        wire_packet_base64: String,
        wire_major: u32,
        connection_generation: u64,
        trust_manifest_revision: u64,
        credential_status_revision: u64,
    }

    #[test]
    fn decodes_shared_server_challenge_vector() -> Result<(), Box<dyn std::error::Error>> {
        let vector: ChallengeVector = serde_json::from_str(include_str!(
            "../../protocol/testdata/v1/wire-server-challenge.json"
        ))?;
        assert!(vector.fixture_use.starts_with("TEST ONLY"));
        let packet = WirePacket::decode(STANDARD.decode(vector.wire_packet_base64)?.as_slice())?;
        assert_eq!(packet.wire_major, vector.wire_major);
        let challenge = match packet.body {
            Some(wire_packet::Body::ServerChallenge(value)) => value,
            _ => return Err("shared fixture is not a ServerChallenge".into()),
        };
        assert_eq!(
            challenge.connection_generation,
            vector.connection_generation
        );
        assert_eq!(
            challenge.trust_manifest_revision,
            vector.trust_manifest_revision
        );
        assert_eq!(
            challenge.credential_status_revision,
            vector.credential_status_revision
        );
        Ok(())
    }
}
