//! Fair, bounded output polling. A slow/offline HC is handled by the durable relay,
//! not by blocking the Agent turn or the Gateway's inbound control handling.
use super::*;

const MAX_POLL_PACKETS: usize = 32;
const MAX_EVENTS_PER_SESSION: usize = 4;

impl ControlState {
    pub fn poll(
        &mut self,
        endpoint_id: &[u8; 16],
        identity: &EndpointIdentity,
        now: SystemTime,
    ) -> Result<Vec<Vec<u8>>, GatewayError> {
        let now_ms = unix_millis(now)?;
        let journal = self.journal.clone();
        let mut ids: Vec<[u8; 16]> = self.sessions.keys().copied().collect();
        ids.sort_unstable();
        if ids.is_empty() {
            return Ok(Vec::new());
        }
        let offset = self.poll_offset % ids.len();
        ids.rotate_left(offset);
        self.poll_offset = (offset + 1) % ids.len();
        let mut packets = Vec::new();
        for id in ids {
            if packets.len() >= MAX_POLL_PACKETS {
                break;
            }
            let session = self.sessions.get_mut(&id).ok_or(GatewayError::Protocol)?;
            if !session.active || session.uncertain {
                continue;
            }
            let channel_id =
                session_channel_id(&id, endpoint_id, &session.material.recipient_hc_endpoint_id)?;
            for _ in 0..MAX_EVENTS_PER_SESSION {
                if packets.len() >= MAX_POLL_PACKETS {
                    break;
                }
                match session.agent.poll() {
                    Ok(Some(event)) => {
                        if !event.message.is_empty() {
                            packets.push(seal_aba_frame(
                                session,
                                endpoint_id,
                                identity,
                                channel_id,
                                &event.message,
                                now_ms,
                                &journal,
                            )?);
                        }
                        if let Some(message_id) = event.completed_dispatch {
                            if !session.pending_dispatches.remove(&message_id) {
                                return Err(GatewayError::ProtocolStage(
                                    "unknown dispatch receipt",
                                ));
                            }
                            journal.mark_responded(message_id, now_ms)?;
                        }
                    }
                    Ok(None) => break,
                    Err(_) => {
                        session.uncertain = true;
                        for message_id in std::mem::take(&mut session.pending_dispatches) {
                            journal.mark_uncertain(message_id, now_ms)?;
                            session.uncertain_message_id = Some(message_id);
                        }
                        if let Some(message_id) = session.uncertain_message_id {
                            packets.push(signed_uncertain_error(message_id, identity)?);
                        }
                        break;
                    }
                }
            }
        }
        Ok(packets)
    }
}
