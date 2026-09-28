use mobius_github::Repository;

use crate::Engine;
use crate::config::Config;

pub(crate) fn trusted_authors<'a>(
    engine: &'a Engine,
    repository: &'a Repository,
) -> impl Fn(&str) -> bool + 'a {
    move |login: &str| trusted_author(&engine.config, &repository.app_slug, login)
}

pub(crate) fn app_login(app_slug: &str) -> String {
    format!("{app_slug}[bot]")
}

pub fn trusted_author(config: &Config, app_slug: &str, login: &str) -> bool {
    login.eq_ignore_ascii_case(&app_login(app_slug))
        || config
            .trusted_users
            .iter()
            .chain(&config.trusted_bots)
            .any(|author| author.eq_ignore_ascii_case(login))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn config() -> Config {
        crate::config::parse(
            r#"
access_password = "correct horse"
trusted_users = ["owner"]
trusted_bots = ["coderabbitai[bot]"]

[roles]
lead        = { harness = "claude-code", model = "opus",    effort = "high" }
triager     = { harness = "claude-code", model = "sonnet",  effort = "medium" }
implementer = { harness = "devin",       model = "swe-1.5", effort = "high" }
researcher  = { harness = "antigravity", model = "gemini-3-pro" }
reviewer    = { harness = "claude-code", model = "opus",    effort = "high" }
judge       = { harness = "claude-code", model = "haiku",   effort = "low" }
"#,
        )
        .unwrap()
    }

    #[test]
    fn trusts_a_trusted_user_in_each_letter_case() {
        assert!(trusted_author(&config(), "mobius-app", "owner"));
        assert!(trusted_author(&config(), "mobius-app", "Owner"));
    }

    #[test]
    fn trusts_a_trusted_bot() {
        assert!(trusted_author(&config(), "mobius-app", "coderabbitai[bot]"));
    }

    #[test]
    fn trusts_the_mobius_app() {
        assert!(trusted_author(&config(), "mobius-app", "mobius-app[bot]"));
        assert!(trusted_author(&config(), "mobius-app", "Mobius-App[bot]"));
    }

    #[test]
    fn refuses_other_authors() {
        assert!(!trusted_author(&config(), "mobius-app", "mallory"));
        assert!(!trusted_author(&config(), "mobius-app", "mobius-app"));
        assert!(!trusted_author(&config(), "mobius-app", "coderabbitai"));
    }
}
