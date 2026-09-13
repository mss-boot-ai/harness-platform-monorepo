//! Bounded independent stdin/stdout pumps. A blocked agent never owns the Gateway loop.
use std::io::{BufRead, BufReader, Write as _};
use std::process::{ChildStdin, ChildStdout};
use std::sync::mpsc::{Receiver, SyncSender, sync_channel};
use std::thread::{self, JoinHandle};

use super::{MAX_ACP_MESSAGE_BYTES, ProcessError};

pub(super) const QUEUE_DEPTH: usize = 64;
pub(super) struct WriteCommand {
    pub bytes: Vec<u8>,
    pub receipt: Option<[u8; 16]>,
}

pub(super) enum TransportEvent {
    Message(Vec<u8>),
    Written([u8; 16]),
    Failure(ProcessError),
}

pub(super) struct Pumps {
    pub writer: SyncSender<WriteCommand>,
    pub events: Receiver<TransportEvent>,
    pub threads: Vec<JoinHandle<()>>,
}

pub(super) fn spawn(stdin: ChildStdin, stdout: ChildStdout) -> Result<Pumps, ProcessError> {
    let (writer, commands) = sync_channel::<WriteCommand>(QUEUE_DEPTH);
    let (output, events) = sync_channel(QUEUE_DEPTH);
    let read_output = output.clone();
    let reader = thread::Builder::new()
        .name("aba-agent-stdout".to_owned())
        .spawn(move || {
            let mut reader = BufReader::new(stdout);
            loop {
                match read_line(&mut reader) {
                    Ok(Some(bytes)) => {
                        if read_output.send(TransportEvent::Message(bytes)).is_err() {
                            return;
                        }
                    }
                    Ok(None) => {
                        let _ = read_output.send(TransportEvent::Failure(ProcessError::Transport));
                        return;
                    }
                    Err(error) => {
                        let _ = read_output.send(TransportEvent::Failure(error));
                        return;
                    }
                }
            }
        })
        .map_err(|_| ProcessError::Spawn)?;
    let write_thread = thread::Builder::new()
        .name("aba-agent-stdin".to_owned())
        .spawn(move || {
            let mut stdin = stdin;
            while let Ok(command) = commands.recv() {
                if stdin
                    .write_all(&command.bytes)
                    .and_then(|()| stdin.write_all(b"\n"))
                    .and_then(|()| stdin.flush())
                    .is_err()
                {
                    let _ = output.send(TransportEvent::Failure(ProcessError::Transport));
                    return;
                }
                if let Some(receipt) = command.receipt {
                    if output.send(TransportEvent::Written(receipt)).is_err() {
                        return;
                    }
                }
            }
        })
        .map_err(|_| ProcessError::Spawn)?;
    Ok(Pumps {
        writer,
        events,
        threads: vec![reader, write_thread],
    })
}

fn read_line(reader: &mut impl BufRead) -> Result<Option<Vec<u8>>, ProcessError> {
    let mut line = Vec::new();
    loop {
        let available = reader.fill_buf().map_err(|_| ProcessError::Transport)?;
        if available.is_empty() {
            return if line.is_empty() {
                Ok(None)
            } else {
                Err(ProcessError::Transport)
            };
        }
        let newline = available.iter().position(|byte| *byte == b'\n');
        let consumed = newline.map_or(available.len(), |position| position + 1);
        let length = newline.unwrap_or(available.len());
        if line.len().saturating_add(length) > MAX_ACP_MESSAGE_BYTES {
            return Err(ProcessError::Limit);
        }
        line.extend_from_slice(&available[..length]);
        reader.consume(consumed);
        if newline.is_some() {
            if line.last() == Some(&b'\r') {
                line.pop();
            }
            return Ok(Some(line));
        }
    }
}

#[cfg(test)]
mod tests {
    use std::io::Cursor;
    use super::*;

    #[test]
    fn lines_are_bounded_and_require_a_delimiter() -> Result<(), ProcessError> {
        assert_eq!(read_line(&mut Cursor::new(b"{}\r\n"))?, Some(b"{}".to_vec()));
        assert_eq!(read_line(&mut Cursor::new(b""))?, None);
        assert_eq!(read_line(&mut Cursor::new(b"{}")), Err(ProcessError::Transport));
        assert_eq!(
            read_line(&mut Cursor::new(vec![b'x'; MAX_ACP_MESSAGE_BYTES + 1])),
            Err(ProcessError::Limit)
        );
        Ok(())
    }
}
