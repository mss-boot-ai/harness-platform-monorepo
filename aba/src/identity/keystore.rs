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
use zeroize::{Zeroize, ZeroizeOnDrop, Zeroizing};

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

#[derive(Serialize, Deserialize, Zeroize, ZeroizeOnDrop)]
#[serde(deny_unknown_fields)]
struct StoredIdentity {
    version: u32,
    signing_d: String,
    kem_d: String,
    #[serde(default)]
    credentials: Option<EndpointCredentials>,
    #[serde(default)]
    trust: Option<TrustPin>,
}

#[derive(Serialize, Deserialize, Zeroize, ZeroizeOnDrop)]
pub struct EndpointCredentials {
    pub endpoint_id: String,
    pub credential_id: String,
    pub access_token: String,
    pub access_expires_at: String,
    pub refresh_token: String,
    pub refresh_expires_at: String,
}

#[derive(Serialize, Deserialize, Zeroize, ZeroizeOnDrop)]
struct TrustPin {
    root_jkt: String,
    revision: u64,
    #[serde(default)]
    expires_at_ms: i64,
    online_curve: String,
    online_key_type: String,
    online_x: String,
    online_y: String,
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
    #[error("ABA Gateway trust pin is invalid")]
    Trust,
    #[error("ABA endpoint credentials are unavailable")]
    CredentialsUnavailable,
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
            credentials: None,
            trust: None,
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
        let state = read_stored_identity(&self.path)?;
        let signing = decode_scalar(&state.signing_d)?;
        let kem = decode_scalar(&state.kem_d)?;
        Ok(EndpointIdentity {
            signing: SigningKey::from_slice(&signing).map_err(|_| KeyStoreError::Key)?,
            kem: SecretKey::from_slice(&kem).map_err(|_| KeyStoreError::Key)?,
        })
    }

    pub fn save_credentials(&self, credentials: EndpointCredentials) -> Result<(), KeyStoreError> {
        validate_private_file(&self.path)?;
        validate_credentials(&credentials)?;
        let mut state = read_stored_identity(&self.path)?;
        state.credentials = Some(credentials);
        write_stored_identity(&self.path, &state)
    }

    pub fn load_credentials(&self) -> Result<EndpointCredentials, KeyStoreError> {
        validate_private_file(&self.path)?;
        let mut state = read_stored_identity(&self.path)?;
        let credentials = state
            .credentials
            .take()
            .ok_or(KeyStoreError::CredentialsUnavailable)?;
        validate_credentials(&credentials)?;
        Ok(credentials)
    }

    pub fn pin_gateway_trust(
        &self,
        root_jkt: &str,
        revision: u64,
        expires_at_ms: i64,
        online: &P256PublicJwk,
    ) -> Result<(), KeyStoreError> {
        validate_private_file(&self.path)?;
        if decode_base64_fixed(root_jkt, 32).is_err()
            || revision == 0
            || expires_at_ms <= 0
            || online.verifying_key().is_err()
        {
            return Err(KeyStoreError::Trust);
        }
        let mut state = read_stored_identity(&self.path)?;
        if let Some(current) = state.trust.as_ref()
            && (current.root_jkt != root_jkt
                || revision < current.revision
                || (revision == current.revision
                    && (expires_at_ms < current.expires_at_ms
                        || current.online_curve != online.curve
                        || current.online_key_type != online.key_type
                        || current.online_x != online.x
                        || current.online_y != online.y)))
        {
            return Err(KeyStoreError::Trust);
        }
        state.trust = Some(TrustPin {
            root_jkt: root_jkt.to_owned(),
            revision,
            expires_at_ms,
            online_curve: online.curve.clone(),
            online_key_type: online.key_type.clone(),
            online_x: online.x.clone(),
            online_y: online.y.clone(),
        });
        write_stored_identity(&self.path, &state)
    }
}

fn read_stored_identity(path: &Path) -> Result<StoredIdentity, KeyStoreError> {
    let mut file = fs::File::open(path)?;
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
    Ok(state)
}

fn write_stored_identity(path: &Path, state: &StoredIdentity) -> Result<(), KeyStoreError> {
    let encoded = Zeroizing::new(serde_json::to_vec(state).map_err(|_| KeyStoreError::Invalid)?);
    let parent = path.parent().ok_or(KeyStoreError::UnsafePath)?;
    let mut temporary = tempfile::NamedTempFile::new_in(parent)?;
    #[cfg(unix)]
    temporary
        .as_file()
        .set_permissions(fs::Permissions::from_mode(0o600))?;
    temporary.write_all(&encoded)?;
    temporary.as_file().sync_all()?;
    temporary
        .persist(path)
        .map_err(|error| KeyStoreError::Io(error.error))?;
    restrict_file(path)
}

fn validate_credentials(credentials: &EndpointCredentials) -> Result<(), KeyStoreError> {
    if !is_hex_id(&credentials.endpoint_id)
        || !is_hex_id(&credentials.credential_id)
        || decode_base64_fixed(&credentials.access_token, 32).is_err()
        || decode_base64_fixed(&credentials.refresh_token, 32).is_err()
        || credentials.access_expires_at.is_empty()
        || credentials.refresh_expires_at.is_empty()
    {
        return Err(KeyStoreError::Invalid);
    }
    Ok(())
}

fn is_hex_id(value: &str) -> bool {
    value.len() == 32
        && value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || matches!(byte, b'a'..=b'f'))
}

fn decode_base64_fixed(value: &str, length: usize) -> Result<Vec<u8>, KeyStoreError> {
    let decoded = URL_SAFE_NO_PAD
        .decode(value)
        .map_err(|_| KeyStoreError::Invalid)?;
    if decoded.len() != length {
        return Err(KeyStoreError::Invalid);
    }
    Ok(decoded)
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

    #[test]
    fn persists_rotated_credentials_and_rejects_trust_rollback()
    -> Result<(), Box<dyn std::error::Error>> {
        let directory = tempfile::tempdir()?;
        let path = directory.path().join("private").join("identity.json");
        let platform = Url::parse("http://localhost:8082")?;
        let store = DevFileKeyStore::new(&path, &platform, true)?;
        store.initialize()?;
        assert!(matches!(
            store.load_credentials(),
            Err(KeyStoreError::CredentialsUnavailable)
        ));
        store.save_credentials(EndpointCredentials {
            endpoint_id: "01010101010101010101010101010101".to_owned(),
            credential_id: "02020202020202020202020202020202".to_owned(),
            access_token: URL_SAFE_NO_PAD.encode([3_u8; 32]),
            access_expires_at: "2030-01-01T00:00:00Z".to_owned(),
            refresh_token: URL_SAFE_NO_PAD.encode([4_u8; 32]),
            refresh_expires_at: "2030-01-02T00:00:00Z".to_owned(),
        })?;
        let loaded = store.load_credentials()?;
        assert_eq!(loaded.endpoint_id, "01010101010101010101010101010101");
        assert_eq!(loaded.credential_id, "02020202020202020202020202020202");

        let online = store.load()?.signing_public_jwk()?;
        let root = URL_SAFE_NO_PAD.encode([9_u8; 32]);
        store.pin_gateway_trust(&root, 3, 1_800_000_000_000, &online)?;
        store.pin_gateway_trust(&root, 3, 1_800_000_001_000, &online)?;
        assert!(matches!(
            store.pin_gateway_trust(&root, 3, 1_800_000_000_999, &online),
            Err(KeyStoreError::Trust)
        ));
        store.pin_gateway_trust(&root, 4, 1_800_000_002_000, &online)?;
        assert!(matches!(
            store.pin_gateway_trust(&root, 3, 1_800_000_003_000, &online),
            Err(KeyStoreError::Trust)
        ));
        assert!(matches!(
            store.pin_gateway_trust(
                &URL_SAFE_NO_PAD.encode([8_u8; 32]),
                5,
                1_800_000_004_000,
                &online
            ),
            Err(KeyStoreError::Trust)
        ));
        Ok(())
    }
}
