use std::error::Error;

use http::header::ACCEPT;
use octocrab::Octocrab;
use serde::Deserialize;
use serde_json::json;

#[derive(Clone)]
pub struct GitHub {
    api: Octocrab,
    web: Octocrab,
    web_url: String,
}

#[derive(Deserialize)]
struct Account {
    #[serde(rename = "type")]
    account_type: String,
}

#[derive(Deserialize)]
pub struct NewApp {
    pub id: i64,
    pub slug: String,
    pub pem: String,
    pub client_id: String,
    pub client_secret: String,
}

#[derive(Deserialize)]
pub struct UserTokens {
    pub access_token: String,
    pub refresh_token: String,
}

#[derive(Deserialize)]
#[serde(untagged)]
enum CodeExchange {
    Tokens(UserTokens),
    Refused { error_description: String },
}

#[derive(Deserialize)]
struct User {
    login: String,
}

pub fn manifest(origin: &str) -> String {
    json!({
        "url": "https://github.com/Mobius-Toolkit/Mobius",
        "redirect_url": format!("{origin}/api/github/manifest-callback"),
        "callback_urls": [format!("{origin}/api/github/user-callback")],
        "request_oauth_on_install": true,
        "public": false,
        "default_permissions": {
            "issues": "write",
            "pull_requests": "write",
            "contents": "write",
            "checks": "write",
            "metadata": "read"
        }
    })
    .to_string()
}

impl GitHub {
    pub fn new(api_url: &str, web_url: &str) -> Result<GitHub, Box<dyn Error + Send + Sync>> {
        Ok(GitHub {
            api: Octocrab::builder().base_uri(api_url)?.build()?,
            web: Octocrab::builder()
                .base_uri(web_url)?
                .add_header(ACCEPT, "application/json".to_string())
                .build()?,
            web_url: web_url.to_string(),
        })
    }

    pub async fn manifest_url(
        &self,
        account: &str,
    ) -> Result<String, Box<dyn Error + Send + Sync>> {
        let found: Account = self
            .api
            .get(format!("/users/{account}"), None::<&()>)
            .await?;
        Ok(if found.account_type == "Organization" {
            format!("{}/organizations/{account}/settings/apps/new", self.web_url)
        } else {
            format!("{}/settings/apps/new", self.web_url)
        })
    }

    pub async fn convert_manifest(
        &self,
        code: &str,
    ) -> Result<NewApp, Box<dyn Error + Send + Sync>> {
        Ok(self
            .api
            .post(format!("/app-manifests/{code}/conversions"), None::<&()>)
            .await?)
    }

    pub async fn user_tokens(
        &self,
        client_id: &str,
        client_secret: &str,
        code: &str,
    ) -> Result<UserTokens, Box<dyn Error + Send + Sync>> {
        let exchange: CodeExchange = self
            .web
            .post(
                "/login/oauth/access_token",
                Some(&json!({
                    "client_id": client_id,
                    "client_secret": client_secret,
                    "code": code
                })),
            )
            .await?;
        match exchange {
            CodeExchange::Tokens(tokens) => Ok(tokens),
            CodeExchange::Refused { error_description } => Err(error_description.into()),
        }
    }

    pub async fn user_login(
        &self,
        user_token: &str,
    ) -> Result<String, Box<dyn Error + Send + Sync>> {
        let user: User = self
            .api
            .user_access_token(user_token.to_string())?
            .get("/user", None::<&()>)
            .await?;
        Ok(user.login)
    }
}
