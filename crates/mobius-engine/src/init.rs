use std::error::Error;
use std::ffi::OsStr;
use std::fs;
use std::io::{BufRead, Write};
use std::path::Path;
use std::time::Duration;

use mobius_domain::Harness;
use mobius_runner::Choices;

use crate::config::{self, EFFORT_LEVEL_HARNESSES};

const ROLES: [(&str, &str); 6] = [
    ("lead", "Lead"),
    ("triager", "Triager"),
    ("implementer", "Implementer"),
    ("researcher", "Researcher"),
    ("reviewer", "Reviewer"),
    ("judge", "Judge"),
];
const START_TIMEOUT: Duration = Duration::from_secs(60);
// The Harness gets no Mobius tools here, so the MCP URL has no server.
const NO_MCP_URL: &str = "http://127.0.0.1:1/mcp/init";

struct Offer {
    harness: Harness,
    models: Choices,
    efforts: Option<Choices>,
}

struct Binding {
    harness: Harness,
    model: String,
    effort: Option<String>,
}

pub async fn run(
    input: &mut impl BufRead,
    output: &mut impl Write,
    config_path: &Path,
    path: &OsStr,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    if config_path.exists() {
        return Err(format!(
            "{} exists. `mobius init` writes only a new file.",
            config_path.display()
        )
        .into());
    }
    let password = loop {
        let first = ask(input, output, "Access password (8 characters or more): ")?;
        if first.chars().count() < 8 {
            writeln!(output, "The password has less than 8 characters.")?;
            continue;
        }
        if ask(input, output, "Access password again: ")? == first {
            break first;
        }
        writeln!(output, "The two passwords are not the same.")?;
    };
    let users = loop {
        let line = ask(
            input,
            output,
            "GitHub logins of the trusted users, with spaces between them: ",
        )?;
        let users: Vec<String> = line.split_whitespace().map(str::to_string).collect();
        if !users.is_empty() {
            break users;
        }
        writeln!(output, "Give one login or more.")?;
    };
    let offers = offers(output, path).await?;
    if offers
        .iter()
        .map(|offer| offer.models.values.len())
        .sum::<usize>()
        < 2
    {
        return Err("The Reviewer needs another Harness or another model than the Implementer, but the Harnesses gave less than 2 models.".into());
    }
    let mut bindings: Vec<(&str, Binding)> = Vec::new();
    for (key, name) in ROLES {
        let binding = loop {
            let binding = choose(input, output, &offers, name)?;
            let implementer = bindings.iter().find(|(role, _)| *role == "implementer");
            if key == "reviewer"
                && let Some((_, implementer)) = implementer
                && implementer.harness == binding.harness
                && implementer.model == binding.model
            {
                writeln!(
                    output,
                    "The Reviewer needs another Harness or another model than the Implementer."
                )?;
                continue;
            }
            break binding;
        };
        bindings.push((key, binding));
    }
    let text = file_text(&password, &users, &bindings);
    config::parse(&text)?;
    if let Some(dir) = config_path.parent() {
        fs::create_dir_all(dir)?;
    }
    fs::write(config_path, text)?;
    writeln!(output, "Mobius wrote {}.", config_path.display())?;
    writeln!(output, "Start Mobius with `mobius`.")?;
    Ok(())
}

fn title(harness: Harness) -> &'static str {
    match harness {
        Harness::ClaudeCode => "Claude Code",
        Harness::Antigravity => "Antigravity",
        Harness::Devin => "Devin",
    }
}

async fn offers(
    output: &mut impl Write,
    path: &OsStr,
) -> Result<Vec<Offer>, Box<dyn Error + Send + Sync>> {
    let dir = std::env::temp_dir().join(format!("mobius-init-{}", std::process::id()));
    fs::create_dir_all(&dir)?;
    let mut offers = Vec::new();
    for harness in Harness::ALL {
        let program = mobius_runner::program(harness);
        if mobius_runner::find(program, path).is_none() {
            writeln!(output, "{}: `{program}` is not on PATH", title(harness))?;
            continue;
        }
        let started = tokio::time::timeout(
            START_TIMEOUT,
            mobius_runner::start(harness, &dir, &dir, path, NO_MCP_URL, None),
        )
        .await;
        let (session, _updates) = match started {
            Ok(Ok(started)) => started,
            Ok(Err(error)) => {
                writeln!(output, "{}: {error}", title(harness))?;
                continue;
            }
            Err(_) => {
                writeln!(
                    output,
                    "{}: the Harness gave no answer to session/new",
                    title(harness)
                )?;
                continue;
            }
        };
        let has_values = |choices: &Choices| !choices.values.is_empty();
        let models = session.models().filter(has_values);
        let efforts = session.efforts().filter(has_values);
        session.close().await;
        let Some(models) = models else {
            writeln!(
                output,
                "{}: the Harness gave no model option",
                title(harness)
            )?;
            continue;
        };
        if EFFORT_LEVEL_HARNESSES.contains(&harness) && efforts.is_none() {
            writeln!(
                output,
                "{}: the Harness gave no thought_level option",
                title(harness)
            )?;
            continue;
        }
        offers.push(Offer {
            harness,
            models,
            efforts,
        });
    }
    fs::remove_dir_all(&dir)?;
    Ok(offers)
}

fn choose(
    input: &mut impl BufRead,
    output: &mut impl Write,
    offers: &[Offer],
    role: &str,
) -> Result<Binding, Box<dyn Error + Send + Sync>> {
    writeln!(output, "\n{role}")?;
    let names: Vec<String> = offers
        .iter()
        .map(|offer| offer.harness.name().to_string())
        .collect();
    let offer = &offers[pick(input, output, "Harness", &names, 0)?];
    let model = pick_current(input, output, "Model", &offer.models)?;
    let effort = match (
        &offer.efforts,
        EFFORT_LEVEL_HARNESSES.contains(&offer.harness),
    ) {
        (Some(efforts), true) => Some(pick_current(input, output, "Effort", efforts)?),
        _ => None,
    };
    Ok(Binding {
        harness: offer.harness,
        model,
        effort,
    })
}

fn pick_current(
    input: &mut impl BufRead,
    output: &mut impl Write,
    label: &str,
    choices: &Choices,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    let current = choices
        .values
        .iter()
        .position(|value| *value == choices.current)
        .unwrap_or_default();
    Ok(choices.values[pick(input, output, label, &choices.values, current)?].clone())
}

fn pick(
    input: &mut impl BufRead,
    output: &mut impl Write,
    label: &str,
    values: &[String],
    default: usize,
) -> Result<usize, Box<dyn Error + Send + Sync>> {
    for (index, value) in values.iter().enumerate() {
        writeln!(output, "  {}) {value}", index + 1)?;
    }
    loop {
        let line = ask(input, output, &format!("{label} [{}]: ", default + 1))?;
        if line.trim().is_empty() {
            return Ok(default);
        }
        match line.trim().parse::<usize>() {
            Ok(number) if (1..=values.len()).contains(&number) => return Ok(number - 1),
            _ => writeln!(output, "Give a number from 1 to {}.", values.len())?,
        }
    }
}

fn ask(
    input: &mut impl BufRead,
    output: &mut impl Write,
    question: &str,
) -> Result<String, Box<dyn Error + Send + Sync>> {
    write!(output, "{question}")?;
    output.flush()?;
    let mut line = String::new();
    if input.read_line(&mut line)? == 0 {
        return Err("The input ended before the last answer.".into());
    }
    // The password can start or end with a space.
    Ok(line.trim_end_matches(['\n', '\r']).to_string())
}

fn quoted(text: &str) -> String {
    toml::Value::String(text.to_string()).to_string()
}

fn file_text(password: &str, users: &[String], bindings: &[(&str, Binding)]) -> String {
    let users: Vec<String> = users.iter().map(|user| quoted(user)).collect();
    let mut text = format!(
        "access_password = {}\ntrusted_users = [{}]\n\n[roles]\n",
        quoted(password),
        users.join(", ")
    );
    for (role, binding) in bindings {
        let effort = binding
            .effort
            .as_ref()
            .map(|effort| format!(", effort = {}", quoted(effort)))
            .unwrap_or_default();
        text.push_str(&format!(
            "{role} = {{ harness = {}, model = {}{effort} }}\n",
            quoted(binding.harness.name()),
            quoted(&binding.model)
        ));
    }
    text
}
