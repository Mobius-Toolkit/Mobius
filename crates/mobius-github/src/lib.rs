use std::error::Error;

use http::StatusCode;
use http::header::{ACCEPT, ETAG, HeaderMap, HeaderValue, IF_NONE_MATCH};
use jsonwebtoken::EncodingKey;
use octocrab::Octocrab;
use octocrab::models::{AppId, InstallationId};
use serde::Deserialize;
use serde::de::DeserializeOwned;
use serde_json::json;
use time::OffsetDateTime;
use time::format_description::well_known::Rfc3339;

const PAGE_SIZE: usize = 100;

#[derive(Clone)]
pub struct GitHub {
    api: Octocrab,
    web: Octocrab,
    api_url: String,
    web_url: String,
}

#[derive(Clone)]
pub struct Repository {
    pub full_name: String,
    client: Octocrab,
}

#[derive(Deserialize)]
struct Installation {
    id: u64,
}

#[derive(Deserialize)]
struct InstallationRepositories {
    repositories: Vec<RepositoryName>,
}

#[derive(Deserialize)]
struct RepositoryName {
    full_name: String,
}

#[derive(Deserialize)]
pub struct Issue {
    pub number: i64,
    pub title: String,
    pub body: Option<String>,
    pub state: String,
    pub html_url: String,
    #[serde(with = "time::serde::rfc3339")]
    pub updated_at: OffsetDateTime,
    pub labels: Vec<Label>,
    pub pull_request: Option<serde_json::Value>,
}

impl Issue {
    pub fn has_label(&self, name: &str) -> bool {
        self.labels.iter().any(|label| label.name == name)
    }
}

#[derive(Deserialize)]
pub struct Label {
    pub name: String,
}

#[derive(Deserialize)]
pub struct IssueEvent {
    pub event: String,
    pub actor: Option<User>,
    pub label: Option<Label>,
    #[serde(with = "time::serde::rfc3339")]
    pub created_at: OffsetDateTime,
}

pub struct IssuePage {
    pub issues: Vec<Issue>,
    pub etag: Option<String>,
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
pub struct User {
    pub login: String,
}

pub fn manifest(origin: &str) -> String {
    json!({
        "name": "Möbius",
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
            api_url: api_url.to_string(),
            web_url: web_url.to_string(),
        })
    }

    pub async fn repositories(
        &self,
        app_id: i64,
        private_key: &str,
    ) -> Result<Vec<Repository>, Box<dyn Error + Send + Sync>> {
        let app = Octocrab::builder()
            .base_uri(self.api_url.as_str())?
            .app(
                AppId(u64::try_from(app_id)?),
                EncodingKey::from_rsa_pem(private_key.as_bytes())?,
            )
            .build()?;
        let installations =
            all_pages(&app, "/app/installations", |page: Vec<Installation>| page).await?;
        let mut repositories = Vec::new();
        for installation in installations {
            // This call gets the token before the clones, so each clone holds the token.
            let (client, _) = app
                .installation_and_token(InstallationId(installation.id))
                .await?;
            let names = all_pages(
                &client,
                "/installation/repositories",
                |page: InstallationRepositories| page.repositories,
            )
            .await?;
            repositories.extend(names.into_iter().map(|repository| Repository {
                full_name: repository.full_name,
                client: client.clone(),
            }));
        }
        Ok(repositories)
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

impl Repository {
    pub async fn open_issues_with_label(
        &self,
        label: &str,
    ) -> Result<Vec<Issue>, Box<dyn Error + Send + Sync>> {
        let issues = all_pages(
            &self.client,
            &format!("/repos/{}/issues?state=open&labels={label}", self.full_name),
            |page: Vec<Issue>| page,
        )
        .await?;
        Ok(issues
            .into_iter()
            .filter(|issue| issue.pull_request.is_none())
            .collect())
    }

    pub async fn issue(&self, number: i64) -> Result<Issue, Box<dyn Error + Send + Sync>> {
        Ok(self
            .client
            .get(
                format!("/repos/{}/issues/{number}", self.full_name),
                None::<&()>,
            )
            .await?)
    }

    pub async fn sub_issues(
        &self,
        number: i64,
    ) -> Result<Vec<Issue>, Box<dyn Error + Send + Sync>> {
        all_pages(
            &self.client,
            &format!("/repos/{}/issues/{number}/sub_issues", self.full_name),
            |page: Vec<Issue>| page,
        )
        .await
    }

    // Gives `None` when GitHub answers `304 Not Modified` to `etag`.
    pub async fn issues_since(
        &self,
        since: Option<OffsetDateTime>,
        etag: Option<&str>,
    ) -> Result<Option<IssuePage>, Box<dyn Error + Send + Sync>> {
        let since = match since {
            Some(since) => format!("&since={}", since.format(&Rfc3339)?),
            None => String::new(),
        };
        let mut issues = Vec::new();
        let mut first_etag = None;
        let mut pages = 0;
        for page in 1.. {
            pages = page;
            let mut headers = HeaderMap::new();
            if page == 1
                && let Some(etag) = etag
            {
                headers.insert(IF_NONE_MATCH, HeaderValue::from_str(etag)?);
            }
            let uri = format!(
                "/repos/{}/issues?state=all&sort=updated&direction=asc&per_page={PAGE_SIZE}&page={page}{since}",
                self.full_name
            );
            let response = self.client._get_with_headers(uri, Some(headers)).await?;
            if response.status() == StatusCode::NOT_MODIFIED {
                return Ok(None);
            }
            let response = octocrab::map_github_error(response).await?;
            if page == 1 {
                first_etag = response
                    .headers()
                    .get(ETAG)
                    .map(|value| value.to_str())
                    .transpose()?
                    .map(str::to_string);
            }
            let page: Vec<Issue> =
                serde_json::from_str(&self.client.body_to_string(response).await?)?;
            let last_page = page.len() < PAGE_SIZE;
            issues.extend(page);
            if last_page {
                break;
            }
        }
        // A `304` for page 1 says nothing about the pages after it.
        Ok(Some(IssuePage {
            issues,
            etag: first_etag.filter(|_| pages == 1),
        }))
    }

    pub async fn issue_events(
        &self,
        number: i64,
    ) -> Result<Vec<IssueEvent>, Box<dyn Error + Send + Sync>> {
        all_pages(
            &self.client,
            &format!("/repos/{}/issues/{number}/events", self.full_name),
            |page: Vec<IssueEvent>| page,
        )
        .await
    }
}

async fn all_pages<P: DeserializeOwned, T>(
    client: &Octocrab,
    route: &str,
    items_of: impl Fn(P) -> Vec<T>,
) -> Result<Vec<T>, Box<dyn Error + Send + Sync>> {
    let mut items = Vec::new();
    for page in 1.. {
        let page: P = client
            .get(route, Some(&[("per_page", PAGE_SIZE), ("page", page)]))
            .await?;
        let page = items_of(page);
        let last_page = page.len() < PAGE_SIZE;
        items.extend(page);
        if last_page {
            break;
        }
    }
    Ok(items)
}
