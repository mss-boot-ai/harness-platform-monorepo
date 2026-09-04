use std::collections::BTreeSet;
use std::fs::{self, File, OpenOptions};
use std::io::Write as _;
#[cfg(unix)]
use std::os::unix::fs::{OpenOptionsExt as _, PermissionsExt as _};
use std::path::{Component, Path, PathBuf};
use std::sync::{Arc, Mutex};

use serde::{Deserialize, Serialize};
use thiserror::Error;

const JOURNAL_SCHEMA_VERSION: u32 = 1;
const MAX_PACKET_BYTES: usize = 1 << 20;

#[derive(Debug, Error)]
pub enum JournalError {
    #[error("ABA journal path or permissions are unsafe")]
    UnsafePath,
    #[error("ABA journal I/O failed")]
    Io(#[source] std::io::Error),
    #[error("ABA journal is corrupt")]
    Corrupt,
    #[error("ABA journal reached its configured capacity")]
    Capacity,
    #[error("ABA journal detected a frame identity conflict")]
    Conflict,
    #[error("ABA journal state transition is invalid")]
    InvalidState,
}

#[derive(Debug, Clone, Copy, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "SCREAMING_SNAKE_CASE")]
pub enum InboundState {
    Received,
    DispatchStarted,
    Responded,
    Uncertain,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum IntakeOutcome {
    New,
    Duplicate(InboundState),
}

#[derive(Debug, Clone)]
pub struct InboundFrame {
    pub message_id: [u8; 16],
    pub session_id: [u8; 16],
    pub channel_id: [u8; 16],
    pub direction: u8,
    pub sequence: u64,
    pub key_generation: u64,
    pub content_hash: [u8; 32],
    pub updated_at_ms: i64,
}

#[derive(Debug, Clone)]
pub struct OutboundFrame {
    pub message_id: [u8; 16],
    pub session_id: [u8; 16],
    pub channel_id: [u8; 16],
    pub direction: u8,
    pub sequence: u64,
    pub key_generation: u64,
    pub packet: Vec<u8>,
    pub created_at_ms: i64,
}

#[derive(Debug, Clone)]
pub struct ChannelKey {
    pub session_id: [u8; 16],
    pub channel_id: [u8; 16],
    pub direction: u8,
    pub key_generation: u64,
}

#[derive(Clone)]
pub struct Journal {
    inner: Arc<JournalInner>,
}

struct JournalInner {
    state: Mutex<JournalState>,
    path: Option<PathBuf>,
    max_bytes: u64,
    _lease: Option<File>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct JournalState {
    schema_version: u32,
    channels: Vec<ChannelRecord>,
    inbound: Vec<InboundRecord>,
    outbound: Vec<OutboundRecord>,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct ChannelRecord {
    session_id: [u8; 16],
    channel_id: [u8; 16],
    direction: u8,
    key_generation: u64,
    last_reserved_sequence: u64,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct InboundRecord {
    message_id: [u8; 16],
    session_id: [u8; 16],
    channel_id: [u8; 16],
    direction: u8,
    sequence: u64,
    key_generation: u64,
    content_hash: [u8; 32],
    state: InboundState,
    updated_at_ms: i64,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct OutboundRecord {
    message_id: [u8; 16],
    session_id: [u8; 16],
    channel_id: [u8; 16],
    direction: u8,
    sequence: u64,
    key_generation: u64,
    packet: Vec<u8>,
    created_at_ms: i64,
}

impl Journal {
    pub fn open(path: impl AsRef<Path>, max_bytes: u64) -> Result<Self, JournalError> {
        let path = safe_journal_path(path.as_ref())?;
        let lease = acquire_lease(&path)?;
        let mut state = if path.exists() {
            let metadata = fs::symlink_metadata(&path).map_err(JournalError::Io)?;
            if !metadata.is_file()
                || metadata.file_type().is_symlink()
                || metadata.len() > max_bytes
            {
                return Err(JournalError::UnsafePath);
            }
            #[cfg(unix)]
            if metadata.permissions().mode() & 0o077 != 0 {
                return Err(JournalError::UnsafePath);
            }
            let encoded = fs::read(&path).map_err(JournalError::Io)?;
            serde_json::from_slice(&encoded).map_err(|_| JournalError::Corrupt)?
        } else {
            JournalState::empty()
        };
        validate_state(&state)?;
        let recovered = state.recover_uncertain();
        let journal = Self {
            inner: Arc::new(JournalInner {
                state: Mutex::new(state),
                path: Some(path),
                max_bytes,
                _lease: Some(lease),
            }),
        };
        if recovered || !journal.path_exists() {
            let state = journal.lock_state()?.clone();
            journal.persist(&state)?;
        }
        Ok(journal)
    }

    #[cfg(test)]
    fn memory(max_bytes: u64) -> Self {
        Self {
            inner: Arc::new(JournalInner {
                state: Mutex::new(JournalState::empty()),
                path: None,
                max_bytes,
                _lease: None,
            }),
        }
    }

    pub fn record_inbound(&self, value: InboundFrame) -> Result<IntakeOutcome, JournalError> {
        validate_inbound(&value)?;
        self.update(|state| {
            if let Some(existing) = state.inbound.iter().find(|existing| {
                existing.message_id == value.message_id
                    || (existing.session_id == value.session_id
                        && existing.channel_id == value.channel_id
                        && existing.direction == value.direction
                        && existing.key_generation == value.key_generation
                        && existing.sequence == value.sequence)
            }) {
                if existing.message_id == value.message_id
                    && existing.session_id == value.session_id
                    && existing.channel_id == value.channel_id
                    && existing.direction == value.direction
                    && existing.key_generation == value.key_generation
                    && existing.sequence == value.sequence
                    && existing.content_hash == value.content_hash
                {
                    return Ok(IntakeOutcome::Duplicate(existing.state));
                }
                return Err(JournalError::Conflict);
            }
            state.inbound.push(InboundRecord {
                message_id: value.message_id,
                session_id: value.session_id,
                channel_id: value.channel_id,
                direction: value.direction,
                sequence: value.sequence,
                key_generation: value.key_generation,
                content_hash: value.content_hash,
                state: InboundState::Received,
                updated_at_ms: value.updated_at_ms,
            });
            Ok(IntakeOutcome::New)
        })
    }

    pub fn start_dispatch(&self, message_id: [u8; 16], now_ms: i64) -> Result<(), JournalError> {
        self.transition(
            message_id,
            InboundState::Received,
            InboundState::DispatchStarted,
            now_ms,
        )
    }

    pub fn mark_responded(&self, message_id: [u8; 16], now_ms: i64) -> Result<(), JournalError> {
        self.transition(
            message_id,
            InboundState::DispatchStarted,
            InboundState::Responded,
            now_ms,
        )
    }

    pub fn mark_uncertain(&self, message_id: [u8; 16], now_ms: i64) -> Result<(), JournalError> {
        self.transition(
            message_id,
            InboundState::DispatchStarted,
            InboundState::Uncertain,
            now_ms,
        )
    }

    pub fn reserve_outbound_sequence(&self, key: ChannelKey) -> Result<u64, JournalError> {
        validate_channel(&key)?;
        self.update(|state| {
            if let Some(channel) = state
                .channels
                .iter_mut()
                .find(|channel| same_channel(channel, &key))
            {
                channel.last_reserved_sequence = channel
                    .last_reserved_sequence
                    .checked_add(1)
                    .ok_or(JournalError::Capacity)?;
                return Ok(channel.last_reserved_sequence);
            }
            state.channels.push(ChannelRecord {
                session_id: key.session_id,
                channel_id: key.channel_id,
                direction: key.direction,
                key_generation: key.key_generation,
                last_reserved_sequence: 1,
            });
            Ok(1)
        })
    }

    pub fn record_outbound(&self, value: OutboundFrame) -> Result<(), JournalError> {
        validate_outbound(&value)?;
        self.update(|state| {
            let reserved = state.channels.iter().any(|channel| {
                same_channel(
                    channel,
                    &ChannelKey {
                        session_id: value.session_id,
                        channel_id: value.channel_id,
                        direction: value.direction,
                        key_generation: value.key_generation,
                    },
                ) && channel.last_reserved_sequence >= value.sequence
            });
            if !reserved {
                return Err(JournalError::InvalidState);
            }
            if let Some(existing) = state.outbound.iter().find(|existing| {
                existing.message_id == value.message_id
                    || (existing.session_id == value.session_id
                        && existing.channel_id == value.channel_id
                        && existing.direction == value.direction
                        && existing.key_generation == value.key_generation
                        && existing.sequence == value.sequence)
            }) {
                if existing.message_id == value.message_id
                    && existing.session_id == value.session_id
                    && existing.channel_id == value.channel_id
                    && existing.direction == value.direction
                    && existing.key_generation == value.key_generation
                    && existing.sequence == value.sequence
                    && existing.packet == value.packet
                {
                    return Ok(());
                }
                return Err(JournalError::Conflict);
            }
            state.outbound.push(OutboundRecord {
                message_id: value.message_id,
                session_id: value.session_id,
                channel_id: value.channel_id,
                direction: value.direction,
                sequence: value.sequence,
                key_generation: value.key_generation,
                packet: value.packet,
                created_at_ms: value.created_at_ms,
            });
            Ok(())
        })
    }

    pub fn unacknowledged(
        &self,
        session_id: [u8; 16],
        direction: u8,
    ) -> Result<Vec<Vec<u8>>, JournalError> {
        if zero(&session_id) || !valid_direction(direction) {
            return Err(JournalError::InvalidState);
        }
        let state = self.lock_state()?;
        let mut values = state
            .outbound
            .iter()
            .filter(|frame| frame.session_id == session_id && frame.direction == direction)
            .collect::<Vec<_>>();
        values.sort_by_key(|frame| (frame.key_generation, frame.sequence));
        Ok(values.iter().map(|frame| frame.packet.clone()).collect())
    }

    pub fn acknowledge_outbound(
        &self,
        session_id: [u8; 16],
        direction: u8,
        key_generation: u64,
        highest: u64,
        ranges: &[(u64, u64)],
    ) -> Result<usize, JournalError> {
        if zero(&session_id)
            || !valid_direction(direction)
            || key_generation == 0
            || (highest == 0 && ranges.is_empty())
            || !valid_ranges(highest, ranges)
        {
            return Err(JournalError::InvalidState);
        }
        self.update(|state| {
            let before = state.outbound.len();
            state.outbound.retain(|frame| {
                if frame.session_id != session_id
                    || frame.direction != direction
                    || frame.key_generation != key_generation
                {
                    return true;
                }
                let acknowledged = frame.sequence <= highest
                    || ranges
                        .iter()
                        .any(|(start, end)| frame.sequence >= *start && frame.sequence <= *end);
                !acknowledged
            });
            Ok(before - state.outbound.len())
        })
    }

    pub fn uncertain_count(&self) -> Result<usize, JournalError> {
        Ok(self
            .lock_state()?
            .inbound
            .iter()
            .filter(|record| record.state == InboundState::Uncertain)
            .count())
    }

    fn transition(
        &self,
        message_id: [u8; 16],
        expected: InboundState,
        next: InboundState,
        now_ms: i64,
    ) -> Result<(), JournalError> {
        if zero(&message_id) || now_ms <= 0 {
            return Err(JournalError::InvalidState);
        }
        self.update(|state| {
            let record = state
                .inbound
                .iter_mut()
                .find(|record| record.message_id == message_id)
                .ok_or(JournalError::InvalidState)?;
            if record.state != expected {
                return Err(JournalError::InvalidState);
            }
            record.state = next;
            record.updated_at_ms = now_ms;
            Ok(())
        })
    }

    fn update<T>(
        &self,
        operation: impl FnOnce(&mut JournalState) -> Result<T, JournalError>,
    ) -> Result<T, JournalError> {
        let mut current = self.lock_state()?;
        let mut next = current.clone();
        let result = operation(&mut next)?;
        validate_state(&next)?;
        self.persist(&next)?;
        *current = next;
        Ok(result)
    }

    fn lock_state(&self) -> Result<std::sync::MutexGuard<'_, JournalState>, JournalError> {
        self.inner
            .state
            .lock()
            .map_err(|_| JournalError::InvalidState)
    }

    fn path_exists(&self) -> bool {
        self.inner.path.as_ref().is_some_and(|path| path.exists())
    }

    fn persist(&self, state: &JournalState) -> Result<(), JournalError> {
        let encoded = serde_json::to_vec(state).map_err(|_| JournalError::Corrupt)?;
        if u64::try_from(encoded.len()).map_err(|_| JournalError::Capacity)? > self.inner.max_bytes
        {
            return Err(JournalError::Capacity);
        }
        let Some(path) = &self.inner.path else {
            return Ok(());
        };
        let parent = path.parent().ok_or(JournalError::UnsafePath)?;
        let mut temporary = None;
        for _ in 0..16 {
            let name = format!(
                ".journal-{:016x}.tmp",
                rand_core::RngCore::next_u64(&mut rand_core::OsRng)
            );
            let candidate = parent.join(name);
            let mut options = OpenOptions::new();
            options.write(true).create_new(true);
            #[cfg(unix)]
            options.mode(0o600);
            match options.open(&candidate) {
                Ok(file) => {
                    temporary = Some((candidate, file));
                    break;
                }
                Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => continue,
                Err(error) => return Err(JournalError::Io(error)),
            }
        }
        let (temporary_path, mut file) = temporary.ok_or(JournalError::Capacity)?;
        let write_result = file.write_all(&encoded).and_then(|()| file.sync_all());
        if let Err(error) = write_result {
            let _ = fs::remove_file(&temporary_path);
            return Err(JournalError::Io(error));
        }
        drop(file);
        if let Err(error) = fs::rename(&temporary_path, path) {
            let _ = fs::remove_file(&temporary_path);
            return Err(JournalError::Io(error));
        }
        File::open(parent)
            .and_then(|directory| directory.sync_all())
            .map_err(JournalError::Io)
    }
}

impl JournalState {
    fn empty() -> Self {
        Self {
            schema_version: JOURNAL_SCHEMA_VERSION,
            channels: Vec::new(),
            inbound: Vec::new(),
            outbound: Vec::new(),
        }
    }

    fn recover_uncertain(&mut self) -> bool {
        let mut changed = false;
        for record in &mut self.inbound {
            if record.state == InboundState::DispatchStarted {
                record.state = InboundState::Uncertain;
                changed = true;
            }
        }
        changed
    }
}

fn safe_journal_path(input: &Path) -> Result<PathBuf, JournalError> {
    let absolute = if input.is_absolute() {
        input.to_path_buf()
    } else {
        std::env::current_dir()
            .map_err(JournalError::Io)?
            .join(input)
    };
    let file_name = absolute.file_name().ok_or(JournalError::UnsafePath)?;
    if absolute
        .components()
        .any(|component| matches!(component, Component::ParentDir))
    {
        return Err(JournalError::UnsafePath);
    }
    let parent = absolute.parent().ok_or(JournalError::UnsafePath)?;
    if !parent.exists() {
        fs::create_dir_all(parent).map_err(JournalError::Io)?;
        #[cfg(unix)]
        fs::set_permissions(parent, fs::Permissions::from_mode(0o700)).map_err(JournalError::Io)?;
    }
    let metadata = fs::symlink_metadata(parent).map_err(JournalError::Io)?;
    if !metadata.is_dir() || metadata.file_type().is_symlink() {
        return Err(JournalError::UnsafePath);
    }
    #[cfg(unix)]
    if metadata.permissions().mode() & 0o077 != 0 {
        return Err(JournalError::UnsafePath);
    }
    let canonical_parent = fs::canonicalize(parent).map_err(JournalError::Io)?;
    if canonical_parent != parent {
        return Err(JournalError::UnsafePath);
    }
    Ok(canonical_parent.join(file_name))
}

fn acquire_lease(path: &Path) -> Result<File, JournalError> {
    let file_name = path.file_name().ok_or(JournalError::UnsafePath)?;
    let lease_path = path.with_file_name(format!("{}.lock", file_name.to_string_lossy()));
    if lease_path.exists() {
        let metadata = fs::symlink_metadata(&lease_path).map_err(JournalError::Io)?;
        if !metadata.is_file() || metadata.file_type().is_symlink() {
            return Err(JournalError::UnsafePath);
        }
        #[cfg(unix)]
        if metadata.permissions().mode() & 0o077 != 0 {
            return Err(JournalError::UnsafePath);
        }
    }
    let mut options = OpenOptions::new();
    options.read(true).write(true).create(true);
    #[cfg(unix)]
    options.mode(0o600);
    let file = options.open(lease_path).map_err(JournalError::Io)?;
    #[cfg(unix)]
    rustix::fs::flock(&file, rustix::fs::FlockOperation::NonBlockingLockExclusive)
        .map_err(|_| JournalError::InvalidState)?;
    Ok(file)
}

fn validate_state(state: &JournalState) -> Result<(), JournalError> {
    if state.schema_version != JOURNAL_SCHEMA_VERSION {
        return Err(JournalError::Corrupt);
    }
    let mut channels = BTreeSet::new();
    for value in &state.channels {
        if zero(&value.session_id)
            || zero(&value.channel_id)
            || !valid_direction(value.direction)
            || value.key_generation == 0
            || value.last_reserved_sequence == 0
            || !channels.insert((
                value.session_id,
                value.channel_id,
                value.direction,
                value.key_generation,
            ))
        {
            return Err(JournalError::Corrupt);
        }
    }
    let mut inbound_ids = BTreeSet::new();
    let mut inbound_sequences = BTreeSet::new();
    for value in &state.inbound {
        if zero(&value.message_id)
            || zero(&value.session_id)
            || zero(&value.channel_id)
            || zero(&value.content_hash)
            || !valid_direction(value.direction)
            || value.sequence == 0
            || value.key_generation == 0
            || value.updated_at_ms <= 0
            || !inbound_ids.insert(value.message_id)
            || !inbound_sequences.insert((
                value.session_id,
                value.channel_id,
                value.direction,
                value.key_generation,
                value.sequence,
            ))
        {
            return Err(JournalError::Corrupt);
        }
    }
    let mut outbound_ids = BTreeSet::new();
    let mut outbound_sequences = BTreeSet::new();
    for value in &state.outbound {
        if zero(&value.message_id)
            || zero(&value.session_id)
            || zero(&value.channel_id)
            || !valid_direction(value.direction)
            || value.sequence == 0
            || value.key_generation == 0
            || value.packet.is_empty()
            || value.packet.len() > MAX_PACKET_BYTES
            || value.created_at_ms <= 0
            || !outbound_ids.insert(value.message_id)
            || !outbound_sequences.insert((
                value.session_id,
                value.channel_id,
                value.direction,
                value.key_generation,
                value.sequence,
            ))
        {
            return Err(JournalError::Corrupt);
        }
    }
    Ok(())
}

fn validate_inbound(value: &InboundFrame) -> Result<(), JournalError> {
    if zero(&value.message_id)
        || zero(&value.session_id)
        || zero(&value.channel_id)
        || zero(&value.content_hash)
        || !valid_direction(value.direction)
        || value.sequence == 0
        || value.key_generation == 0
        || value.updated_at_ms <= 0
    {
        return Err(JournalError::InvalidState);
    }
    Ok(())
}

fn validate_outbound(value: &OutboundFrame) -> Result<(), JournalError> {
    if zero(&value.message_id)
        || zero(&value.session_id)
        || zero(&value.channel_id)
        || !valid_direction(value.direction)
        || value.sequence == 0
        || value.key_generation == 0
        || value.packet.is_empty()
        || value.packet.len() > MAX_PACKET_BYTES
        || value.created_at_ms <= 0
    {
        return Err(JournalError::InvalidState);
    }
    Ok(())
}

fn validate_channel(value: &ChannelKey) -> Result<(), JournalError> {
    if zero(&value.session_id)
        || zero(&value.channel_id)
        || !valid_direction(value.direction)
        || value.key_generation == 0
    {
        return Err(JournalError::InvalidState);
    }
    Ok(())
}

fn same_channel(record: &ChannelRecord, key: &ChannelKey) -> bool {
    record.session_id == key.session_id
        && record.channel_id == key.channel_id
        && record.direction == key.direction
        && record.key_generation == key.key_generation
}

fn valid_ranges(highest: u64, ranges: &[(u64, u64)]) -> bool {
    if ranges.len() > 32 {
        return false;
    }
    let mut previous = highest;
    for (start, end) in ranges {
        if *start == 0 || *start > *end || *start <= previous {
            return false;
        }
        previous = *end;
    }
    true
}

fn valid_direction(value: u8) -> bool {
    matches!(value, 1 | 2)
}

fn zero<const N: usize>(value: &[u8; N]) -> bool {
    value.iter().all(|byte| *byte == 0)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn persists_dedupe_sequences_and_uncertain_recovery() -> Result<(), Box<dyn std::error::Error>>
    {
        let directory = tempfile::tempdir()?;
        #[cfg(unix)]
        fs::set_permissions(directory.path(), fs::Permissions::from_mode(0o700))?;
        let path = directory.path().join("journal-v1.json");
        let journal = Journal::open(&path, 1 << 20)?;
        assert!(matches!(
            Journal::open(&path, 1 << 20),
            Err(JournalError::InvalidState)
        ));
        let inbound = InboundFrame {
            message_id: [1; 16],
            session_id: [2; 16],
            channel_id: [3; 16],
            direction: 1,
            sequence: 1,
            key_generation: 1,
            content_hash: [4; 32],
            updated_at_ms: 1_800_000_000_000,
        };
        assert_eq!(journal.record_inbound(inbound.clone())?, IntakeOutcome::New);
        assert_eq!(
            journal.record_inbound(inbound.clone())?,
            IntakeOutcome::Duplicate(InboundState::Received)
        );
        let mut conflict = inbound.clone();
        conflict.content_hash[0] ^= 1;
        assert!(matches!(
            journal.record_inbound(conflict),
            Err(JournalError::Conflict)
        ));
        journal.start_dispatch(inbound.message_id, 1_800_000_000_001)?;

        let channel = ChannelKey {
            session_id: inbound.session_id,
            channel_id: inbound.channel_id,
            direction: 2,
            key_generation: 1,
        };
        assert_eq!(journal.reserve_outbound_sequence(channel.clone())?, 1);
        assert_eq!(journal.reserve_outbound_sequence(channel.clone())?, 2);
        let packet = vec![9; 64];
        journal.record_outbound(OutboundFrame {
            message_id: [5; 16],
            session_id: channel.session_id,
            channel_id: channel.channel_id,
            direction: channel.direction,
            sequence: 2,
            key_generation: channel.key_generation,
            packet: packet.clone(),
            created_at_ms: 1_800_000_000_002,
        })?;
        assert_eq!(journal.unacknowledged(channel.session_id, 2)?, vec![packet]);
        drop(journal);

        let reopened = Journal::open(&path, 1 << 20)?;
        assert_eq!(reopened.uncertain_count()?, 1);
        assert_eq!(reopened.reserve_outbound_sequence(channel.clone())?, 3);
        assert_eq!(
            reopened.acknowledge_outbound(channel.session_id, 2, 1, 2, &[])?,
            1
        );
        assert!(reopened.unacknowledged(channel.session_id, 2)?.is_empty());
        Ok(())
    }

    #[test]
    fn enforces_capacity_without_mutating_memory_state() -> Result<(), Box<dyn std::error::Error>> {
        let journal = Journal::memory(64);
        let channel = ChannelKey {
            session_id: [1; 16],
            channel_id: [2; 16],
            direction: 2,
            key_generation: 1,
        };
        assert!(matches!(
            journal.reserve_outbound_sequence(channel),
            Err(JournalError::Capacity)
        ));
        assert_eq!(journal.uncertain_count()?, 0);
        Ok(())
    }
}
