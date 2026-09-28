use std::error::Error;

use mobius_domain::ManifestForm;
use mobius_github::UserTokens;
use time::{Duration, OffsetDateTime};
use tokio::sync::Mutex;

use crate::Engine;

const REFRESH_MARGIN: Duration = Duration::minutes(5);

// Each refresh stops the refresh token that it uses.
static REFRESH: Mutex<()> = Mutex::const_new(());

// GitHub App names are unique on all of GitHub, and GitHub allows a maximum of 34 characters.
const MAX_APP_NAME: usize = 34;

pub async fn manifest_form(
    engine: &Engine,
    account: &str,
    name: &str,
    origin: &str,
) -> Result<ManifestForm, Box<dyn Error + Send + Sync>> {
    let name = app_name(name)?;
    Ok(ManifestForm {
        url: engine.github.manifest_url(account).await?,
        manifest: mobius_github::manifest(origin, name),
    })
}

fn app_name(name: &str) -> Result<&str, String> {
    let name = name.trim();
    if name.is_empty() || name.chars().count() > MAX_APP_NAME {
        return Err(format!(
            "The App name must have 1 to {MAX_APP_NAME} characters."
        ));
    }
    Ok(name)
}

pub async fn convert_manifest(
    engine: &Engine,
    code: &str,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let app = engine.github.convert_manifest(code).await?;
    engine
        .store
        .github_apps()
        .add(
            app.id,
            &app.slug,
            &app.pem,
            &app.client_id,
            &app.client_secret,
        )
        .await
}

// GitHub sends the Owner to the same callback URL for each App, so the callback tries the code with each App.
pub async fn authorize_user(
    engine: &Engine,
    code: &str,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    let mut refused = "The Mobius App does not exist.".to_string();
    for app in engine.store.github_apps().list().await? {
        let tokens = match engine
            .github
            .user_tokens(&app.client_id, &app.client_secret, code)
            .await
        {
            Ok(tokens) => tokens,
            Err(error) => {
                refused = error.to_string();
                continue;
            }
        };
        let login = engine.github.user_login(&tokens.access_token).await?;
        if !engine
            .config
            .trusted_users
            .iter()
            .any(|user| user.eq_ignore_ascii_case(&login))
        {
            return Ok(false);
        }
        store_user_tokens(engine, app.app_id, &tokens).await?;
        return Ok(true);
    }
    Err(refused.into())
}

async fn store_user_tokens(
    engine: &Engine,
    app_id: i64,
    tokens: &UserTokens,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    engine
        .store
        .github_apps()
        .set_user_tokens(
            app_id,
            &tokens.access_token,
            &tokens.refresh_token,
            OffsetDateTime::now_utc() + Duration::seconds(tokens.expires_in),
        )
        .await
}

pub(crate) async fn user_token(
    engine: &Engine,
    app_id: i64,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let _refresh = REFRESH.lock().await;
    let app = engine
        .store
        .github_apps()
        .get(app_id)
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
    store_user_tokens(engine, app_id, &tokens).await?;
    Ok(tokens.access_token)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn an_app_name_has_1_to_34_characters_with_no_outer_spaces() {
        assert_eq!(app_name("  Mobius owner "), Ok("Mobius owner"));
        assert_eq!(app_name(&"m".repeat(34)), Ok("m".repeat(34).as_str()));
    }

    #[test]
    fn an_empty_or_long_app_name_is_refused() {
        let error = Err("The App name must have 1 to 34 characters.".to_string());

        assert_eq!(app_name("   ").map(str::to_string), error);
        assert_eq!(app_name(&"m".repeat(35)).map(str::to_string), error);
    }
}
