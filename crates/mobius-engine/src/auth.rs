use std::error::Error;
use std::time::Duration;

use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use tokio::sync::Mutex;

use crate::Engine;

static LOGIN: Mutex<()> = Mutex::const_new(());

fn sha256(text: &str) -> [u8; 32] {
    Sha256::digest(text).into()
}

pub async fn login(
    engine: &Engine,
    password: &str,
    user_agent: &str,
) -> Result<Option<String>, Box<dyn Error + Send + Sync>> {
    let _login = LOGIN.lock().await;
    let fingerprint = sha256(&engine.config.access_password);
    if !bool::from(sha256(password).ct_eq(&fingerprint)) {
        // The sleep holds the lock, so all clients together get at most one wrong guess each second.
        tokio::time::sleep(Duration::from_secs(1)).await;
        return Ok(None);
    }
    let mut bytes = [0u8; 32];
    getrandom::fill(&mut bytes)?;
    let token: String = bytes.iter().map(|byte| format!("{byte:02x}")).collect();
    engine
        .store
        .device_logins()
        .add(&sha256(&token), &fingerprint, user_agent)
        .await?;
    Ok(Some(token))
}

pub async fn check(
    engine: &Engine,
    token: &str,
) -> Result<Option<i64>, Box<dyn Error + Send + Sync>> {
    engine.store.device_logins().find(&sha256(token)).await
}

pub async fn logout(engine: &Engine, device: i64) -> Result<(), Box<dyn Error + Send + Sync>> {
    engine.store.device_logins().delete(device).await
}

pub(crate) async fn start(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
    engine
        .store
        .device_logins()
        .delete_other_passwords(&sha256(&engine.config.access_password))
        .await
}
