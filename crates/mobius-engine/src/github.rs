use std::error::Error;

use mobius_domain::ManifestForm;

use crate::Engine;

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
    engine
        .store
        .github_app()
        .set_user_tokens(&tokens.access_token, &tokens.refresh_token)
        .await?;
    Ok(true)
}
