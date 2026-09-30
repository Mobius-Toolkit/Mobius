use std::fs;
use std::io::Cursor;
use std::path::Path;
use std::time::Duration;

use chromiumoxide::cdp::browser_protocol::emulation::{
    SetDeviceMetricsOverrideParams, SetTouchEmulationEnabledParams,
};
use chromiumoxide::cdp::browser_protocol::page::CaptureScreenshotFormat;
use chromiumoxide::page::ScreenshotParams;
use chromiumoxide::{Browser, BrowserConfig, Page};
use dioxus::server::axum::Extension;
use dioxus::server::axum::http::header::CONTENT_TYPE;
use dioxus::server::axum::routing::get;
use futures_util::StreamExt;
use mobius_domain::Author;
use mobius_engine::{Engine, chat, github, inbox, workstreams};
use mobius_testkit::fake_github::FakeGitHub;
use mobius_testkit::{install_fake_harness, start, wait_for};
use tempfile::TempDir;
use time::macros::datetime;
use tokio::net::TcpListener;

const REPOSITORY: &str = "owner/shop";
// The organization `plants` has the second App. The switcher selects `owner` first, because `owner` comes first in the sorted list.
const GARDEN: &str = "plants/garden";
const FAKE_AGENT: &str = env!("CARGO_BIN_EXE_fake-agent");
const CLAUDE: &str = r##"
[options]
model = ["sonnet", "opus", "haiku"]
thought_level = ["low", "medium", "high"]
mode = ["default", "bypassPermissions"]

[[prompts]]
when = "dispatch of #41"
call = { tool = "start_implementer", arguments = { n = 41, instructions = "Store the price in cents." } }

[[prompts]]
when = "dispatch of #42"
call = { tool = "tell_owner", arguments = { text = "#42 needs a decision: one plan for each customer, or many?" } }

[[prompts]]
when = "Start a Workstream for gift cards."
reply = ["Title: Gift cards\n\nBrief: Sell gift cards in the shop."]

[[prompts]]
when = "Which roses sell best?"
reply = ["Red roses sell best."]

[[prompts]]
reply = ["The Implementer works on #41. #42 waits for your decision."]
"##;
const IMPLEMENTER: &str = r#"
[options]
model = ["swe-1.5"]
thought_level = ["high"]

[[prompts]]
hang = true
"#;
// The name, the width, the height, and the mobile flag.
type Viewport = (&'static str, u32, u32, bool);
const DESKTOP: Viewport = ("desktop", 1280, 800, false);
const PHONE: Viewport = ("phone", 390, 844, true);

#[derive(Clone, Copy)]
struct Shot<'a> {
    name: &'a str,
    path: &'a str,
    // The CSS selectors of the elements to click, in sequence.
    clicks: &'a [&'a str],
    expected: &'a str,
    // The page shows the Inbox count after the first load of its live data.
    inbox_count: bool,
}

// Outside a `dx` build, `asset!` gives the absolute source path of the file, and the page links that path.
async fn serve_ui(engine: &Engine) -> String {
    let css = fs::canonicalize(concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../mobius-ui/assets/main.css"
    ))
    .unwrap();
    let path = css.to_str().unwrap().to_string();
    let router = dioxus::server::router(mobius_ui::App)
        .route(
            &path,
            get(async move || ([(CONTENT_TYPE, "text/css")], fs::read(&css).unwrap())),
        )
        .layer(Extension(engine.clone()))
        .layer(Extension(engine.store.clone()));
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}", listener.local_addr().unwrap());
    tokio::spawn(async move { dioxus::server::axum::serve(listener, router).await.unwrap() });
    url
}

async fn seed(engine: &Engine, github: &FakeGitHub) {
    github::convert_manifest(engine, "second-code")
        .await
        .unwrap();
    wait_for(async || (workstreams::list(engine).await.unwrap().len() == 2).then_some(())).await;

    github.add_issue(REPOSITORY, 41, "Add plan model");
    github.add_sub_issue(REPOSITORY, 12, 41);
    github.add_label(REPOSITORY, 41, "mobius:ready", "owner");
    wait_for(async || {
        let task = engine.store.tasks().live(REPOSITORY, 41).await.unwrap()?;
        (task.state == "working").then_some(())
    })
    .await;

    github.add_issue(REPOSITORY, 42, "Let customers change plans");
    github.add_sub_issue(REPOSITORY, 12, 42);
    github.add_label(REPOSITORY, 42, "mobius:ready", "owner");
    wait_for(async || (!inbox::list(engine).await.unwrap().is_empty()).then_some(())).await;

    // The wide code block, the wide table, and the long URL scroll or break inside the bubble.
    chat::send(
        engine,
        "owner",
        REPOSITORY,
        12,
        r#"What is the state of the plans? The full report is at https://example.com/reports/loyalty/plans/every-customer-segment-and-billing-period.

```text
summary = [{ plan: "standard", seats: 10, price_per_seat: 100, discount_code: "SPRING-SALE-EXTRA-LONG-2026", renewal: "monthly" }]
```

| Plan | Seats | Price per seat | Discount code | Region | Renewal |
| --- | --- | --- | --- | --- | --- |
| Standard | 10 | $100 | SPRING-SALE-EXTRA-LONG-2026 | Worldwide | Monthly |
| Extended | 40 | $80 | AUTUMN-SALE-EXTRA-LONG-2026 | Europe | Yearly |"#,
    )
    .await
    .unwrap();
    wait_for(async || {
        let messages = engine
            .store
            .chat_messages()
            .list("owner", REPOSITORY, 12)
            .await
            .unwrap();
        messages
            .iter()
            .any(|message| message.author == Author::Lead)
            .then_some(())
    })
    .await;

    // The switcher shows the unread reply in `plants`.
    chat::send(engine, "plants", GARDEN, 12, "Which roses sell best?")
        .await
        .unwrap();
    wait_for(async || {
        chat::view(engine, "plants", GARDEN, 12)
            .await
            .unwrap()
            .messages
            .into_iter()
            .find(|message| message.author == Author::Lead)
    })
    .await;

    chat::send(engine, "owner", "", 0, "Start a Workstream for gift cards.")
        .await
        .unwrap();
    wait_for(async || {
        chat::view(engine, "owner", "", 0)
            .await
            .unwrap()
            .messages
            .into_iter()
            .find(|message| message.author == Author::Triager)
    })
    .await;

    // The Chat page marks the messages as seen, so an unread count changes while a screenshot waits.
    for (organization, repository, workstream) in [("owner", REPOSITORY, 12), ("owner", "", 0)] {
        let messages = chat::view(engine, organization, repository, workstream)
            .await
            .unwrap()
            .messages;
        let last = messages.last().unwrap().id;
        chat::seen(engine, organization, repository, workstream, last)
            .await
            .unwrap();
    }

    // The ended sessions get their end time before `fix_times` changes it.
    wait_for(async || {
        let open: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM sessions WHERE ended_at IS NULL AND role != 'implementer'",
        )
        .fetch_one(&engine.store.pool)
        .await
        .unwrap();
        (open == 0).then_some(())
    })
    .await;
}

// The UI shows these times, so each run must give the same values.
async fn fix_times(engine: &Engine) {
    let time = datetime!(2026-09-28 09:30 UTC);
    for sql in [
        "UPDATE device_logins SET created_at = ?1",
        "UPDATE events SET time = ?1",
        "UPDATE sessions SET started_at = ?1, ended_at = iif(ended_at IS NULL, NULL, ?1)",
        "UPDATE transcript SET time = ?1",
        "UPDATE chat_messages SET time = ?1",
        "UPDATE inbox_items SET time = ?1",
    ] {
        sqlx::query(sql)
            .bind(time)
            .execute(&engine.store.pool)
            .await
            .unwrap();
    }
}

// A script fails while the page loads the next document.
async fn check(page: &Page, script: String) -> bool {
    match page.evaluate(script).await {
        Ok(result) => result.into_value().unwrap(),
        Err(_) => false,
    }
}

async fn wait_until_ready(page: &Page, expected: &str, inbox_count: bool) {
    let script = format!(
        "document.body.textContent.includes({expected:?}) && \
         (!{inbox_count} || !!document.querySelector('a[href=\"/inbox\"] .count'))"
    );
    wait_for(async || check(page, script.clone()).await.then_some(())).await;
}

// A new tab has no text, so the check in `wait_until_ready` cannot match the page before.
async fn open(browser: &Browser, url: &str, (_, width, height, mobile): Viewport) -> Page {
    let page = browser.new_page("about:blank").await.unwrap();
    page.set_user_agent("Mobius screenshots").await.unwrap();
    page.execute(SetDeviceMetricsOverrideParams::new(
        width, height, 1.0, mobile,
    ))
    .await
    .unwrap();
    // Headless Chrome claims a touch pointer (`pointer: coarse`) on every viewport.
    // The touch emulation sets the pointer type of each device class: on for the
    // phone shots, off for the desktop shots so they report `pointer: fine`.
    page.execute(SetTouchEmulationEnabledParams::new(mobile))
        .await
        .unwrap();
    page.evaluate(format!("location.href = {url:?}"))
        .await
        .unwrap();
    page
}

async fn capture(page: &Page) -> Vec<u8> {
    let params = ScreenshotParams::builder()
        .format(CaptureScreenshotFormat::Png)
        .build();
    page.screenshot(params).await.unwrap()
}

fn decode(png: &[u8]) -> (png::OutputInfo, Vec<u8>) {
    let mut reader = png::Decoder::new(Cursor::new(png)).read_info().unwrap();
    let mut pixels = vec![0; reader.output_buffer_size().unwrap()];
    let info = reader.next_frame(&mut pixels).unwrap();
    (info, pixels)
}

fn looks_same(old: &[u8], new: &[u8]) -> bool {
    let (old_info, old) = decode(old);
    let (new_info, new) = decode(new);
    (old_info.width, old_info.height) == (new_info.width, new_info.height)
        && old
            .iter()
            .zip(&new)
            .all(|(old, new)| old.abs_diff(*new) <= 16)
}

async fn screenshot(browser: &Browser, url: &str, shot: Shot<'_>, viewport: Viewport) {
    let page = open(browser, &format!("{url}{}", shot.path), viewport).await;
    for selector in shot.clicks {
        let script = format!(
            "(() => {{ const element = document.querySelector({selector:?}); element?.click(); return !!element; }})()"
        );
        wait_for(async || check(&page, script.clone()).await.then_some(())).await;
    }
    wait_until_ready(&page, shot.expected, shot.inbox_count).await;
    let mut last = capture(&page).await;
    let png = wait_for(async || {
        tokio::time::sleep(Duration::from_millis(200)).await;
        let next = capture(&page).await;
        let same = next == last;
        last = next;
        same.then(|| last.clone())
    })
    .await;
    let directory = Path::new(env!("CARGO_MANIFEST_DIR")).join("../mobius-ui/screenshots");
    fs::create_dir_all(&directory).unwrap();
    let path = directory.join(format!("{}-{}.png", shot.name, viewport.0));
    // Chrome can draw the edge pixels of text and of round corners a little differently in each run.
    if !fs::read(&path).is_ok_and(|old| looks_same(&old, &png)) {
        fs::write(path, png).unwrap();
    }
    page.close().await.unwrap();
}

async fn log_in(browser: &Browser, url: &str) {
    let page = open(browser, url, DESKTOP).await;
    wait_until_ready(&page, "Access password", false).await;
    page.find_element("#password")
        .await
        .unwrap()
        .click()
        .await
        .unwrap()
        .type_str("correct horse")
        .await
        .unwrap();
    page.find_element("button[type=submit]")
        .await
        .unwrap()
        .click()
        .await
        .unwrap();
    wait_until_ready(&page, "App name", false).await;
    page.close().await.unwrap();
}

#[tokio::test]
#[ignore = "starts Chrome and serves the web bundle in DIOXUS_PUBLIC_PATH"]
async fn screenshots() {
    let data_dir = TempDir::new().unwrap();
    let github = FakeGitHub::start().await;
    github.add_manifest_code("manifest-code");
    github.add_manifest_code("second-code");
    github.install_second_app("plants");
    install_fake_harness(data_dir.path(), FAKE_AGENT, "claude-agent-acp", CLAUDE);
    install_fake_harness(data_dir.path(), FAKE_AGENT, "devin", IMPLEMENTER);
    let engine = start(data_dir.path(), "correct horse", &github.url).await;
    let url = serve_ui(&engine).await;
    let (mut browser, mut handler) = Browser::launch(
        BrowserConfig::builder()
            .no_sandbox()
            .arg("--hide-scrollbars")
            .build()
            .unwrap(),
    )
    .await
    .unwrap();
    tokio::spawn(async move { while handler.next().await.is_some() {} });

    let login = Shot {
        name: "login",
        path: "/github",
        clicks: &[],
        expected: "Access password",
        inbox_count: false,
    };
    let connect = Shot {
        name: "github-connect",
        path: "/github",
        clicks: &[],
        expected: "App name",
        inbox_count: false,
    };
    for viewport in [DESKTOP, PHONE] {
        screenshot(&browser, &url, login, viewport).await;
    }
    log_in(&browser, &format!("{url}/github")).await;
    for viewport in [DESKTOP, PHONE] {
        screenshot(&browser, &url, connect, viewport).await;
    }

    // The App has no repository yet, so Mobius knows no organization.
    github::convert_manifest(&engine, "manifest-code")
        .await
        .unwrap();
    let no_organization = Shot {
        name: "new-workstream-no-organization",
        path: "/workstreams/new",
        clicks: &[],
        expected: "Mobius reads the repositories from GitHub.",
        inbox_count: false,
    };
    for viewport in [DESKTOP, PHONE] {
        screenshot(&browser, &url, no_organization, viewport).await;
    }

    for (repository, title) in [
        (REPOSITORY, "Integrate loyalty plans"),
        (GARDEN, "Plant roses"),
    ] {
        github.add_repository(repository);
        github.add_issue(repository, 12, title);
        github.add_label(repository, 12, "mobius:workstream", "owner");
    }
    seed(&engine, &github).await;
    fix_times(&engine).await;
    for viewport in [DESKTOP, PHONE] {
        let (tasks_clicks, switch_clicks): (&[&str], &[&str]) = if viewport == PHONE {
            (
                &[".btn.phone", ".sheet .sidetabs button:nth-child(2)"],
                &[".head .switch"],
            )
        } else {
            (&[".side .sidetabs button:nth-child(2)"], &[".rail .switch"])
        };
        let shots = [
            Shot {
                name: "workstreams",
                path: "/workstreams",
                clicks: &[],
                expected: "Integrate loyalty plans",
                inbox_count: true,
            },
            Shot {
                name: "organizations",
                path: "/workstreams",
                clicks: switch_clicks,
                expected: "Organizations",
                inbox_count: true,
            },
            Shot {
                name: "activity",
                path: "/activity",
                clicks: &[],
                expected: "Dispatched",
                inbox_count: true,
            },
            Shot {
                name: "chat",
                path: "/workstreams/owner/shop/12",
                clicks: &[],
                expected: "#42 waits for your decision.",
                inbox_count: true,
            },
            Shot {
                name: "chat-tasks",
                path: "/workstreams/owner/shop/12",
                clicks: tasks_clicks,
                expected: "#41 Add plan model",
                inbox_count: true,
            },
            Shot {
                name: "new-workstream",
                path: "/workstreams/new",
                clicks: &[],
                expected: "Sell gift cards in the shop.",
                inbox_count: true,
            },
            Shot {
                name: "settings",
                path: "/settings",
                clicks: &[],
                expected: "Devices",
                inbox_count: true,
            },
            Shot {
                name: "server-agents",
                path: "/server-agents",
                clicks: &[],
                expected: "chat session",
                inbox_count: true,
            },
            Shot {
                name: "inbox",
                path: "/inbox",
                clicks: &[],
                expected: "one plan for each customer",
                inbox_count: true,
            },
            Shot {
                name: "devices",
                path: "/devices",
                clicks: &[],
                expected: "this device",
                inbox_count: true,
            },
            Shot {
                name: "github",
                path: "/github",
                clicks: &[],
                expected: "Install mobius-second",
                inbox_count: true,
            },
        ];
        for shot in shots {
            screenshot(&browser, &url, shot, viewport).await;
        }
        if viewport == PHONE {
            // A wide message scrolls inside the bubble; the page itself never scrolls sideways.
            let page = open(
                &browser,
                &format!("{url}/workstreams/owner/shop/12"),
                ("check", 375, 667, true),
            )
            .await;
            wait_until_ready(&page, "#42 waits for your decision.", false).await;
            // `overflow-x: auto` keeps the wide block inside the bubble, so no ancestor overflows.
            let script = String::from(
                "(() => {
                    const msgs = document.querySelector('.msgs');
                    const bubble = document.querySelector('.msg.owner');
                    const message = bubble?.querySelector('.md');
                    const pre = message?.querySelector('pre');
                    const table = message?.querySelector('table');
                    const overflows = (element) => element.scrollWidth > element.clientWidth;
                    const scrolls = (element) => getComputedStyle(element).overflowX === 'auto';
                    return !!pre && !!table
                        && overflows(pre) && scrolls(pre)
                        && overflows(table) && scrolls(table)
                        && !overflows(message) && !overflows(bubble) && !overflows(msgs)
                        && document.documentElement.scrollWidth <= window.innerWidth;
                })()",
            );
            wait_for(async || check(&page, script.clone()).await.then_some(())).await;
            page.close().await.unwrap();
        }
    }
    browser.close().await.unwrap();
}
