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
        let mut packets = Vec::new();
        let finished: Vec<_> = self
            .closing
            .iter()
            .filter_map(|(id, pending)| match pending.result.try_recv() {
                Ok(result) => Some((
                    *id,
                    pending.receiver_endpoint,
                    result.is_ok(),
                    pending.receipt_requested,
                )),
                Err(TryRecvError::Disconnected) => Some((
                    *id,
                    pending.receiver_endpoint,
                    false,
                    pending.receipt_requested,
                )),
                Err(TryRecvError::Empty) => None,
            })
            .take(4)
            .collect();
        for (id, receiver, confirmed, receipt_requested) in finished {
            self.closing.remove(&id);
            if !confirmed {
                continue;
            } // Keep Platform DRAINING; exact close retry can reconcile again.
            self.journal.close_session(id)?;
            if !receipt_requested {
                continue;
            }
            let payload = CloseTunnelResult {
                session_id: id.to_vec(),
                status: if receiver == [0; 16] {
                    CloseTunnelStatus::AlreadyClosed
                } else {
                    CloseTunnelStatus::Accepted
                } as i32,
                stable_error_code: String::new(),
            }
            .encode_to_vec();
            packets.push(self.signed_control(
                endpoint_id,
                &receiver,
                ControlType::CloseTunnelResult,
                payload,
                identity,
                now_ms,
            )?);
        }
        let mut ready = Vec::new();
        for (id, pending) in &self.starting {
            let result = match pending.receiver.try_recv() {
                Ok(value) => value,
                Err(TryRecvError::Empty) => continue,
                Err(TryRecvError::Disconnected) => Err(ProcessError::Spawn),
            };
            ready.push((*id, result));
            if ready.len() >= 4 {
                break;
            }
        }
        for (id, result) in ready {
            let pending = self.starting.remove(&id).ok_or(GatewayError::Protocol)?;
            if pending.cancelled {
                let receiver = pending
                    .request
                    .hc_endpoint_id
                    .as_slice()
                    .try_into()
                    .map_err(|_| GatewayError::Protocol)?;
                // Includes cancellation caused by reconnect. Never drop a process on the network thread.
                self.start_close(id, receiver, result.ok(), pending.close_requested)?;
                continue;
            }
            let (decision, agent) = if now_ms >= pending.request.expires_at_ms {
                let receiver = pending
                    .request
                    .hc_endpoint_id
                    .as_slice()
                    .try_into()
                    .map_err(|_| GatewayError::Protocol)?;
                self.start_close(id, receiver, result.ok(), false)?;
                (rejected("AGENT_START_EXPIRED"), None)
            } else {
                match result {
                    Ok(agent) => (pending.decision, Some(agent)),
                    Err(ProcessError::WorkspaceBusy) => (rejected("WORKSPACE_BUSY"), None),
                    Err(_) => (rejected("AGENT_START_FAILED"), None),
                }
            };
            packets.extend(self.finish_open(
                pending.request,
                decision,
                agent,
                OpenContext {
                    endpoint_id,
                    identity,
                    credential_id: &pending.credential_id,
                    now_ms,
                },
            )?);
        }
        let journal = self.journal.clone();
        let mut ids: Vec<[u8; 16]> = self.sessions.keys().copied().collect();
        ids.sort_unstable();
        if ids.is_empty() {
            return Ok(packets);
        }
        let offset = self.poll_offset % ids.len();
        ids.rotate_left(offset);
        self.poll_offset = (offset + 1) % ids.len();
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
