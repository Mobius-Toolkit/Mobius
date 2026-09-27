pub mod fake_github;

use std::path::Path;

use mobius_engine::Engine;
use mobius_store::Store;

pub async fn start(data_dir: &Path, access_password: &str, github_url: &str) -> Engine {
    let config = mobius_engine::config::parse(&format!(
        r#"
access_password = "{access_password}"
trusted_users = ["owner"]
data_dir = "{}"

[roles]
lead        = {{ harness = "claude-code", model = "opus",    effort = "high" }}
triager     = {{ harness = "claude-code", model = "sonnet",  effort = "medium" }}
implementer = {{ harness = "devin",       model = "swe-1.5", effort = "high" }}
researcher  = {{ harness = "antigravity", model = "gemini-3-pro" }}
reviewer    = {{ harness = "claude-code", model = "opus",    effort = "high" }}
judge       = {{ harness = "claude-code", model = "haiku",   effort = "low" }}
"#,
        data_dir.display()
    ))
    .unwrap();
    let store = Store::open(&config.data_dir).await.unwrap();
    mobius_engine::start(config, store, github_url, github_url)
        .await
        .unwrap()
}
