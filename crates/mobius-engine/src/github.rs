use std::error::Error;

use mobius_domain::ManifestForm;
use mobius_github::UserTokens;
use time::{Duration, OffsetDateTime};
use tokio::sync::Mutex;

use crate::Engine;

const REFRESH_MARGIN: Duration = Duration::minutes(5);

// Each refresh stops the refresh token that it uses.
static REFRESH: Mutex<()> = Mutex::const_new(());

pub async fn manifest_form(
    engine: &Engine,
    account: &str,
    origin: &str,
) -> Result<ManifestForm, Box<dyn Error + Send + Sync>> {
    Ok(ManifestForm {
        url: engine.github.manifest_url(account).await?,
        manifest: mobius_github::manifest(origin),
    })
}

pub async fn convert_manifest(
    engine: &Engine,
    code: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    // A conversion creates the App on GitHub and gives the only copy of its private key.
    if engine.store.github_app().get().await?.is_some() {
        return Err("The Mobius App already exists.".into());
    }
    let app = engine.github.convert_manifest(code).await?;
    engine
        .store
        .github_app()
        .add(
            app.id,
            &app.slug,
            &app.pem,
            &app.client_id,
            &app.client_secret,
        )
        .await
}

pub async fn authorize_user(
    engine: &Engine,
    code: &str,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    let app = engine
        .store
        .github_app()
        .get()
        .await?
        .ok_or("The Mobius App does not exist.")?;
    let tokens = engine
        .github
        .user_tokens(&app.client_id, &app.client_secret, code)
        .await?;
    let login = engine.github.user_login(&tokens.access_token).await?;
    if !engine
        .config
        .trusted_users
        .iter()
        .any(|user| user.eq_ignore_ascii_case(&login))
    {
        return Ok(false);
    }
    store_user_tokens(engine, &tokens).await?;
    Ok(true)
}

async fn store_user_tokens(
    engine: &Engine,
    tokens: &UserTokens,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    engine
        .store
        .github_app()
        .set_user_tokens(
            &tokens.access_token,
            &tokens.refresh_token,
            OffsetDateTime::now_utc() + Duration::seconds(tokens.expires_in),
        )
        .await
}

pub(crate) async fn user_token(engine: &Engine) -> Result<String, Box<dyn Error + Send + Sync>> {
    let _refresh = REFRESH.lock().await;
    let app = engine
        .store
        .github_app()
        .get()
        .await?
        .ok_or("The Mobius App does not exist.")?;
    if let (Some(token), Some(expires_at)) = (app.user_token, app.user_token_expires_at)
        && expires_at > OffsetDateTime::now_utc() + REFRESH_MARGIN
    {
        return Ok(token);
    }
    let refreshed = match &app.refresh_token {
        Some(refresh_token) => {
            engine
                .github
                .refresh_user_tokens(&app.client_id, &app.client_secret, refresh_token)
                .await
        }
        None => Err("The Owner did not authorize the Mobius App.".into()),
    };
    let tokens = refreshed.map_err(|error| {
        format!(
            "{error} Tell the Owner to open {} and authorize the Mobius App.",
            engine.github.authorize_url(&app.client_id)
        )
    })?;
    store_user_tokens(engine, &tokens).await?;
    Ok(tokens.access_token)
}
