use std::error::Error;

use mobius_domain::{Author, Harness, InboxKind, Live, PAUSED};
use mobius_runner::PromptError;
use mobius_store::Pause;
use time::{Duration, OffsetDateTime, Time};

use crate::lead::Recorder;
use crate::{Engine, TIME_FORMAT, agents, inbox};

const NO_TIME_WAIT: Duration = Duration::minutes(30);

// `hint` is the last `_claude/rateLimit.resetsAt` of the session.
fn detect(
    harness: Harness,
    error: &PromptError,
    hint: Option<OffsetDateTime>,
    now: OffsetDateTime,
) -> Option<OffsetDateTime> {
    let text = error.to_string();
    let reset = match harness {
        Harness::ClaudeCode => {
            if !error
                .data
                .as_ref()
                .is_some_and(|data| data["errorKind"] == "rate_limit")
            {
                return None;
            }
            hint.or_else(|| resets_at(&text, now))
        }
        Harness::Antigravity => reset_in(&text[text.find("Usage Limit Reached")?..], now),
        Harness::Devin => {
            if error.code != -32011 {
                return None;
            }
            error
                .data
                .as_ref()
                .and_then(|data| data["retryAfterSeconds"].as_i64())
                .map(|seconds| now + Duration::seconds(seconds))
        }
    };
    Some(reset.unwrap_or(now + NO_TIME_WAIT))
}

// "resets 3pm" or "resets 17:30" gives the next such time, in UTC.
fn resets_at(text: &str, now: OffsetDateTime) -> Option<OffsetDateTime> {
    let rest = &text[text.find("resets ")? + "resets ".len()..];
    let token: String = rest
        .chars()
        .take_while(|character| character.is_ascii_alphanumeric() || *character == ':')
        .collect::<String>()
        .to_lowercase();
    let (clock, afternoon) = match (token.strip_suffix("pm"), token.strip_suffix("am")) {
        (Some(clock), _) => (clock, Some(true)),
        (_, Some(clock)) => (clock, Some(false)),
        _ => (token.as_str(), None),
    };
    let (hour, minute) = match clock.split_once(':') {
        Some((hour, minute)) => (hour.parse::<u8>().ok()?, minute.parse::<u8>().ok()?),
        None => (clock.parse::<u8>().ok()?, 0),
    };
    let hour = match afternoon {
        Some(true) if hour < 12 => hour + 12,
        Some(false) if hour == 12 => 0,
        _ => hour,
    };
    let today = now.replace_time(Time::from_hms(hour, minute, 0).ok()?);
    Some(if today > now {
        today
    } else {
        today + Duration::days(1)
    })
}

// "reset in 2 days, 3 hours" gives now plus that time.
fn reset_in(text: &str, now: OffsetDateTime) -> Option<OffsetDateTime> {
    let rest = &text[text.find("reset in ")? + "reset in ".len()..];
    let words: Vec<&str> = rest
        .split(|character: char| character.is_whitespace() || character == ',')
        .filter(|word| !word.is_empty())
        .collect();
    let mut wait = Duration::ZERO;
    for pair in words.windows(2) {
        let Ok(count) = pair[0].parse::<i64>() else {
            continue;
        };
        wait += match pair[1].trim_end_matches(|character: char| !character.is_alphabetic()) {
            "day" | "days" => Duration::days(count),
            "hour" | "hours" => Duration::hours(count),
            "minute" | "minutes" => Duration::minutes(count),
            _ => continue,
        };
    }
    (wait > Duration::ZERO).then_some(now + wait)
}

// After `true`, the pause has ended, and the caller sends the same prompt again.
pub(crate) async fn wait_out(
    recorder: &Recorder,
    harness: Harness,
    error: &PromptError,
) -> Result<bool, Box<dyn Error + Send + Sync>> {
    let now = OffsetDateTime::now_utc();
    // A reset time that is not later than now is an old hint.
    let hint = recorder.reset_hint().filter(|hint| *hint > now);
    let Some(until) = detect(harness, error, hint, now) else {
        return Ok(false);
    };
    let engine = recorder.engine();
    pause(engine, recorder, harness, until).await?;
    wait(engine, harness, Some(recorder.session())).await?;
    Ok(true)
}

// A second session at the same pause adds no second Inbox item. The chat of the Workstream of the session shows the text.
async fn pause(
    engine: &Engine,
    recorder: &Recorder,
    harness: Harness,
    until: OffsetDateTime,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let _pausing = engine.pausing.lock().await;
    let pauses = engine.store.harness_pauses();
    if pauses.get(harness).await?.is_some() {
        return Ok(());
    }
    let (repository, workstream) = recorder.chat_key();
    let text = format!(
        "{} reached a usage limit. Mobius sends the prompt again at {}.",
        harness.name(),
        until.format(TIME_FORMAT)?
    );
    let item = engine
        .store
        .inbox_items()
        .add(
            InboxKind::UsageLimit,
            repository,
            workstream,
            workstream,
            &text,
            "",
        )
        .await?;
    engine.broadcast(Live::Inbox(item.clone()));
    pauses
        .set(&Pause {
            harness,
            until,
            inbox_item: item.id,
        })
        .await?;
    let message = engine
        .store
        .chat_messages()
        .add(repository, workstream, Author::Mobius, &text)
        .await?;
    engine.broadcast(Live::Message(message));
    timer(engine, harness, until);
    Ok(())
}

pub(crate) async fn start(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
    for pause in engine.store.harness_pauses().list().await? {
        timer(engine, pause.harness, pause.until);
    }
    Ok(())
}

fn timer(engine: &Engine, harness: Harness, until: OffsetDateTime) {
    let engine = engine.clone();
    tokio::spawn(async move {
        let left =
            std::time::Duration::try_from(until - OffsetDateTime::now_utc()).unwrap_or_default();
        tokio::time::sleep(left).await;
        let result = async {
            // A later pause of the same Harness has its own timer.
            match engine.store.harness_pauses().get(harness).await? {
                Some(pause) if pause.until == until => end(&engine, &pause).await,
                _ => Ok(()),
            }
        };
        if let Err(error) = result.await {
            eprintln!("mobius: end of the pause of {}: {error}", harness.name());
        }
    });
}

pub async fn resume(engine: &Engine, inbox_item: i64) -> Result<(), Box<dyn Error + Send + Sync>> {
    match engine
        .store
        .harness_pauses()
        .list()
        .await?
        .into_iter()
        .find(|pause| pause.inbox_item == inbox_item)
    {
        Some(pause) => end(engine, &pause).await,
        None => Ok(()),
    }
}

async fn end(engine: &Engine, pause: &Pause) -> Result<(), Box<dyn Error + Send + Sync>> {
    engine.store.harness_pauses().remove(pause.harness).await?;
    inbox::dismiss(engine, pause.inbox_item).await?;
    engine.pauses_changed.notify_waiters();
    engine.workers.changed.notify_waiters();
    Ok(())
}

pub(crate) fn reason(pause: &Pause) -> Result<String, time::error::Format> {
    Ok(format!("{PAUSED}{}", pause.until.format(TIME_FORMAT)?))
}

// A session that waits shows the pause in its queue reason.
pub(crate) async fn wait(
    engine: &Engine,
    harness: Harness,
    session: Option<i64>,
) -> Result<(), Box<dyn Error + Send + Sync>> {
    let mut shown = false;
    loop {
        let changed = engine.pauses_changed.notified();
        tokio::pin!(changed);
        changed.as_mut().enable();
        let Some(pause) = engine.store.harness_pauses().get(harness).await? else {
            if shown && let Some(session) = session {
                let cleared = engine.store.sessions().clear_queue_reason(session).await?;
                engine.broadcast(Live::Agent(agents::node(cleared)));
            }
            return Ok(());
        };
        if !shown && let Some(session) = session {
            let queued = engine
                .store
                .sessions()
                .set_queue_reason(session, &reason(&pause)?)
                .await?;
            engine.broadcast(Live::Agent(agents::node(queued)));
            shown = true;
        }
        changed.await;
    }
}

#[cfg(test)]
mod tests {
    use serde_json::json;
    use time::macros::datetime;

    use super::*;

    const NOW: OffsetDateTime = datetime!(2026-09-28 10:00 UTC);

    fn error(code: i32, message: &str, data: Option<serde_json::Value>) -> PromptError {
        PromptError {
            code,
            message: message.to_string(),
            data,
        }
    }

    #[test]
    fn claude_code_takes_the_reset_time_of_the_usage_update() {
        let limit = error(
            -32603,
            "Internal error",
            Some(json!({ "errorKind": "rate_limit" })),
        );
        let hint = datetime!(2026-09-28 13:00 UTC);

        assert_eq!(
            detect(Harness::ClaudeCode, &limit, Some(hint), NOW),
            Some(hint)
        );
    }

    #[test]
    fn claude_code_reads_the_reset_time_in_the_text() {
        let limit = error(
            -32603,
            "Claude usage limit reached. Your limit resets 3pm.",
            Some(json!({ "errorKind": "rate_limit" })),
        );
        let early = error(
            -32603,
            "Your limit resets 9:30am",
            Some(json!({ "errorKind": "rate_limit" })),
        );

        assert_eq!(
            detect(Harness::ClaudeCode, &limit, None, NOW),
            Some(datetime!(2026-09-28 15:00 UTC))
        );
        assert_eq!(
            detect(Harness::ClaudeCode, &early, None, NOW),
            Some(datetime!(2026-09-29 9:30 UTC))
        );
    }

    #[test]
    fn claude_code_with_no_time_waits_30_minutes_and_another_error_is_no_limit() {
        let limit = error(
            -32603,
            "Rate limit",
            Some(json!({ "errorKind": "rate_limit" })),
        );
        let other = error(
            -32603,
            "Internal error",
            Some(json!({ "errorKind": "overloaded" })),
        );

        assert_eq!(
            detect(Harness::ClaudeCode, &limit, None, NOW),
            Some(datetime!(2026-09-28 10:30 UTC))
        );
        assert_eq!(detect(Harness::ClaudeCode, &other, None, NOW), None);
    }

    #[test]
    fn antigravity_reads_the_days_and_hours() {
        let limit = error(
            -32603,
            "Usage Limit Reached. Your quota will reset in 2 days, 3 hours.",
            None,
        );
        let other = error(-32603, "Model is busy", None);

        assert_eq!(
            detect(Harness::Antigravity, &limit, None, NOW),
            Some(datetime!(2026-09-30 13:00 UTC))
        );
        assert_eq!(detect(Harness::Antigravity, &other, None, NOW), None);
    }

    #[test]
    fn devin_takes_the_retry_time_and_waits_30_minutes_with_no_time() {
        let limit = error(
            -32011,
            "Rate limited",
            Some(json!({ "retryAfterSeconds": 600 })),
        );
        let no_time = error(-32011, "Rate limited", None);
        let other = error(
            -32603,
            "Internal error",
            Some(json!({ "retryAfterSeconds": 600 })),
        );

        assert_eq!(
            detect(Harness::Devin, &limit, None, NOW),
            Some(datetime!(2026-09-28 10:10 UTC))
        );
        assert_eq!(
            detect(Harness::Devin, &no_time, None, NOW),
            Some(datetime!(2026-09-28 10:30 UTC))
        );
        assert_eq!(detect(Harness::Devin, &other, None, NOW), None);
    }
}
