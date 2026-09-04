use std::fs::{self, OpenOptions};
use std::io::{Read, Write};
#[cfg(unix)]
use std::os::unix::fs::{OpenOptionsExt, PermissionsExt};
use std::path::{Path, PathBuf};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use p256::SecretKey;
use p256::ecdsa::SigningKey;
use p256::elliptic_curve::sec1::ToEncodedPoint;
use rand_core::OsRng;
use serde::{Deserialize, Serialize};
use thiserror::Error;
use url::Url;
use zeroize::Zeroizing;

use crate::crypto::P256PublicJwk;

const STATE_VERSION: u32 = 1;
const MAX_STATE_BYTES: u64 = 4096;

pub struct EndpointIdentity {
    signing: SigningKey,
    kem: SecretKey,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct IdentitySummary {
    pub signing_jkt: String,
    pub kem_jkt: String,
    pub assurance: &'static str,
}

#[derive(Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct StoredIdentity {
    version: u32,
    signing_d: String,
    kem_d: String,
}

#[derive(Debug, Error)]
pub enum KeyStoreError {
    #[error("development file KeyStore requires --insecure-dev-keystore")]
    ExplicitOptInRequired,
    #[error("development file KeyStore is allowed only for a loopback Platform URL")]
    LoopbackRequired,
    #[error("ABA KeyStore path is invalid or unsafe")]
    UnsafePath,
    #[error("ABA KeyStore already exists")]
    AlreadyExists,
    #[error("ABA KeyStore is unavailable")]
    Io(#[from] std::io::Error),
    #[error("ABA KeyStore content is invalid")]
    Invalid,
    #[error("ABA KeyStore permissions are too broad")]
    Permissions,
    #[error("ABA identity key is invalid")]
    Key,
}

pub struct DevFileKeyStore {
    path: PathBuf,
}

impl DevFileKeyStore {
    pub fn new(
        path: impl AsRef<Path>,
        platform: &Url,
        insecure_dev_keystore: bool,
    ) -> Result<Self, KeyStoreError> {
        if !insecure_dev_keystore {
            return Err(KeyStoreError::ExplicitOptInRequired);
        }
        if !is_loopback_platform(platform) {
            return Err(KeyStoreError::LoopbackRequired);
        }
        let path = path.as_ref();
        if path.as_os_str().is_empty() || path.file_name().is_none() {
            return Err(KeyStoreError::UnsafePath);
        }
        Ok(Self {
            path: path.to_path_buf(),
        })
    }

    pub fn initialize(&self) -> Result<IdentitySummary, KeyStoreError> {
        let parent = self.path.parent().ok_or(KeyStoreError::UnsafePath)?;
        create_private_directory(parent)?;
        if fs::symlink_metadata(&self.path).is_ok() {
            return Err(KeyStoreError::AlreadyExists);
        }
        let identity = EndpointIdentity {
            signing: SigningKey::random(&mut OsRng),
            kem: SecretKey::random(&mut OsRng),
        };
        let signing_d = Zeroizing::new(URL_SAFE_NO_PAD.encode(identity.signing.to_bytes()));
        let kem_d = Zeroizing::new(URL_SAFE_NO_PAD.encode(identity.kem.to_bytes()));
        let state = StoredIdentity {
            version: STATE_VERSION,
            signing_d: signing_d.to_string(),
            kem_d: kem_d.to_string(),
        };
        let encoded =
            Zeroizing::new(serde_json::to_vec(&state).map_err(|_| KeyStoreError::Invalid)?);
        let mut options = OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        options.mode(0o600);
        let mut file = options.open(&self.path).map_err(|error| {
            if error.kind() == std::io::ErrorKind::AlreadyExists {
                KeyStoreError::AlreadyExists
            } else {
                KeyStoreError::Io(error)
            }
        })?;
        file.write_all(&encoded)?;
        file.sync_all()?;
        restrict_file(&self.path)?;
        identity.summary()
    }

    pub fn load(&self) -> Result<EndpointIdentity, KeyStoreError> {
        validate_private_file(&self.path)?;
        let mut file = fs::File::open(&self.path)?;
        let mut encoded = Zeroizing::new(Vec::new());
        Read::by_ref(&mut file)
            .take(MAX_STATE_BYTES + 1)
            .read_to_end(&mut encoded)?;
        if encoded.is_empty() || encoded.len() as u64 > MAX_STATE_BYTES {
            return Err(KeyStoreError::Invalid);
        }
        let state: StoredIdentity =
            serde_json::from_slice(&encoded).map_err(|_| KeyStoreError::Invalid)?;
        if state.version != STATE_VERSION || state.signing_d == state.kem_d {
            return Err(KeyStoreError::Invalid);
        }
        let signing = decode_scalar(&state.signing_d)?;
        let kem = decode_scalar(&state.kem_d)?;
        Ok(EndpointIdentity {
            signing: SigningKey::from_slice(&signing).map_err(|_| KeyStoreError::Key)?,
            kem: SecretKey::from_slice(&kem).map_err(|_| KeyStoreError::Key)?,
        })
    }
}

impl EndpointIdentity {
    pub fn signing_key(&self) -> &SigningKey {
        &self.signing
    }

    pub fn kem_key(&self) -> &SecretKey {
        &self.kem
    }

    pub fn signing_public_jwk(&self) -> Result<P256PublicJwk, KeyStoreError> {
        point_to_jwk(self.signing.verifying_key().to_encoded_point(false))
    }

    pub fn kem_public_jwk(&self) -> Result<P256PublicJwk, KeyStoreError> {
        point_to_jwk(self.kem.public_key().to_encoded_point(false))
    }

    pub fn summary(&self) -> Result<IdentitySummary, KeyStoreError> {
        let signing_jkt = self
            .signing_public_jwk()?
            .thumbprint()
            .map_err(|_| KeyStoreError::Key)?;
        let kem_jkt = self
            .kem_public_jwk()?
            .thumbprint()
            .map_err(|_| KeyStoreError::Key)?;
        if signing_jkt == kem_jkt {
            return Err(KeyStoreError::Key);
        }
        Ok(IdentitySummary {
            signing_jkt,
            kem_jkt,
            assurance: "insecure-dev-file",
        })
    }
}

fn point_to_jwk(point: p256::EncodedPoint) -> Result<P256PublicJwk, KeyStoreError> {
    let x = point.x().ok_or(KeyStoreError::Key)?;
    let y = point.y().ok_or(KeyStoreError::Key)?;
    Ok(P256PublicJwk {
        curve: "P-256".to_owned(),
        key_type: "EC".to_owned(),
        x: URL_SAFE_NO_PAD.encode(x),
        y: URL_SAFE_NO_PAD.encode(y),
    })
}

fn decode_scalar(value: &str) -> Result<Zeroizing<Vec<u8>>, KeyStoreError> {
    let decoded = Zeroizing::new(
        URL_SAFE_NO_PAD
            .decode(value)
            .map_err(|_| KeyStoreError::Invalid)?,
    );
    if decoded.len() != 32 {
        return Err(KeyStoreError::Invalid);
    }
    Ok(decoded)
}

fn is_loopback_platform(platform: &Url) -> bool {
    matches!(platform.scheme(), "http" | "https")
        && platform.host_str().is_some_and(|host| {
            host.eq_ignore_ascii_case("localhost")
                || host
                    .parse::<std::net::IpAddr>()
                    .is_ok_and(|address| address.is_loopback())
        })
        && platform.username().is_empty()
        && platform.password().is_none()
        && platform.query().is_none()
        && platform.fragment().is_none()
}

fn create_private_directory(path: &Path) -> Result<(), KeyStoreError> {
    fs::create_dir_all(path)?;
    #[cfg(unix)]
    fs::set_permissions(path, fs::Permissions::from_mode(0o700))?;
    let metadata = fs::symlink_metadata(path)?;
    if !metadata.is_dir() || metadata.file_type().is_symlink() {
        return Err(KeyStoreError::UnsafePath);
    }
    #[cfg(unix)]
    if metadata.permissions().mode() & 0o077 != 0 {
        return Err(KeyStoreError::Permissions);
    }
    Ok(())
}

fn restrict_file(path: &Path) -> Result<(), KeyStoreError> {
    #[cfg(unix)]
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
    validate_private_file(path)
}

fn validate_private_file(path: &Path) -> Result<(), KeyStoreError> {
    let metadata = fs::symlink_metadata(path)?;
    if !metadata.is_file() || metadata.file_type().is_symlink() {
        return Err(KeyStoreError::UnsafePath);
    }
    #[cfg(unix)]
    if metadata.permissions().mode() & 0o077 != 0 {
        return Err(KeyStoreError::Permissions);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn requires_explicit_loopback_development_opt_in() -> Result<(), Box<dyn std::error::Error>> {
        let directory = tempfile::tempdir()?;
        let path = directory.path().join("identity.json");
        let loopback = Url::parse("http://127.0.0.1:8082")?;
        assert!(matches!(
            DevFileKeyStore::new(&path, &loopback, false),
            Err(KeyStoreError::ExplicitOptInRequired)
        ));
        let remote = Url::parse("https://platform.example")?;
        assert!(matches!(
            DevFileKeyStore::new(&path, &remote, true),
            Err(KeyStoreError::LoopbackRequired)
        ));
        Ok(())
    }

    #[test]
    fn persists_distinct_keys_without_exposing_private_material()
    -> Result<(), Box<dyn std::error::Error>> {
        let directory = tempfile::tempdir()?;
        let path = directory.path().join("private").join("identity.json");
        let platform = Url::parse("http://localhost:8082")?;
        let store = DevFileKeyStore::new(&path, &platform, true)?;
        let created = store.initialize()?;
        let loaded = store.load()?;
        let inspected = loaded.summary()?;
        assert_eq!(created, inspected);
        assert_ne!(created.signing_jkt, created.kem_jkt);
        assert_eq!(created.assurance, "insecure-dev-file");
        assert!(matches!(
            store.initialize(),
            Err(KeyStoreError::AlreadyExists)
        ));
        #[cfg(unix)]
        {
            let metadata = fs::metadata(&path)?;
            assert_eq!(metadata.permissions().mode() & 0o777, 0o600);
            assert_eq!(
                fs::metadata(path.parent().ok_or(KeyStoreError::UnsafePath)?)?
                    .permissions()
                    .mode()
                    & 0o777,
                0o700
            );
        }
        Ok(())
    }

    #[cfg(unix)]
    #[test]
    fn rejects_broad_permissions_and_symlinks() -> Result<(), Box<dyn std::error::Error>> {
        let directory = tempfile::tempdir()?;
        let path = directory.path().join("private").join("identity.json");
        let platform = Url::parse("http://localhost:8082")?;
        let store = DevFileKeyStore::new(&path, &platform, true)?;
        store.initialize()?;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o644))?;
        assert!(matches!(store.load(), Err(KeyStoreError::Permissions)));
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600))?;
        let link = directory.path().join("identity-link.json");
        std::os::unix::fs::symlink(&path, &link)?;
        let linked = DevFileKeyStore::new(&link, &platform, true)?;
        assert!(matches!(linked.load(), Err(KeyStoreError::UnsafePath)));
        Ok(())
    }
}
