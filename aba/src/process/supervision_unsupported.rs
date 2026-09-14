use super::ProcessError;
use crate::config::{IsolationConfig, RuntimeProfile, WorkspaceProfile};
#[derive(Clone)]
pub struct Supervisor;
pub(super) struct Scope;
impl Supervisor {
    pub fn has_process_command(&self, _: [u8; 16], _: &[&str]) -> Result<bool, ProcessError> {
        Err(ProcessError::UnsafeProfile)
    }
    pub fn open(_: &IsolationConfig) -> Result<Self, ProcessError> {
        Err(ProcessError::UnsafeProfile)
    }
    pub fn close_run(&self, _: [u8; 16]) -> Result<(), ProcessError> {
        Err(ProcessError::CleanupUnconfirmed)
    }
    pub(super) fn prepare(
        &self,
        _: &RuntimeProfile,
        _: &WorkspaceProfile,
        _: [u8; 16],
    ) -> Result<Scope, ProcessError> {
        Err(ProcessError::UnsafeProfile)
    }
}
impl Scope {
    pub(super) fn configure(
        &self,
        _: &mut std::process::Command,
        _: &RuntimeProfile,
    ) -> Result<(), ProcessError> {
        Err(ProcessError::UnsafeProfile)
    }
    pub(super) fn close(&self) -> Result<(), ProcessError> {
        Err(ProcessError::CleanupUnconfirmed)
    }
}
