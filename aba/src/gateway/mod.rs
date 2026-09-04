mod client;
mod control;
mod transcript;
mod trust;

pub use client::{GatewayClient, ReadyConnection};

use reqwest::StatusCode;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum GatewayError {
    #[error("ABA Gateway input is invalid")]
    InvalidInput,
    #[error("ABA Gateway HTTP request failed")]
    Http(#[from] reqwest::Error),
    #[error("ABA Gateway rejected a request with HTTP {0}")]
    Rejected(StatusCode),
    #[error("ABA Gateway response is invalid")]
    InvalidResponse,
    #[error("ABA Gateway trust verification failed")]
    Trust,
    #[error("ABA Gateway WebSocket failed")]
    WebSocket(#[from] tungstenite::Error),
    #[error("ABA Gateway AWP handshake failed")]
    Protocol,
    #[error(transparent)]
    Crypto(#[from] crate::crypto::CryptoError),
    #[error(transparent)]
    KeyPackage(#[from] crate::crypto::key_package::KeyPackageError),
    #[error(transparent)]
    Frame(#[from] crate::crypto::frame::FrameCryptoError),
    #[error(transparent)]
    KeyStore(#[from] crate::identity::KeyStoreError),
}
