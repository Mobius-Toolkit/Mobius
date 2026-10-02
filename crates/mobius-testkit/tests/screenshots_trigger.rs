use std::io::Write;
use std::process::{Command, Stdio};

fn ui_changed(files: &str) -> String {
    let script = concat!(
        env!("CARGO_MANIFEST_DIR"),
        "/../../.github/scripts/ui-changed.sh"
    );
    let mut child = Command::new(script)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    child
        .stdin
        .take()
        .unwrap()
        .write_all(files.as_bytes())
        .unwrap();
    let output = child.wait_with_output().unwrap();
    assert!(output.status.success());
    String::from_utf8(output.stdout).unwrap().trim().to_string()
}

#[test]
fn server_code_does_not_run_the_screenshots() {
    assert_eq!(ui_changed("crates/mobius-engine/src/checks.rs\n"), "false");
}

#[test]
fn ui_file_runs_the_screenshots() {
    assert_eq!(
        ui_changed("crates/mobius-engine/src/checks.rs\ncrates/mobius-ui/src/lib.rs\n"),
        "true"
    );
}
