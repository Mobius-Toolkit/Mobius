use std::error::Error;
use std::fs;
use std::path::{Path, PathBuf};
use std::process::Command;
use std::time::Duration;

use crate::Engine;

// The web UI gets the response of the upgrade call before the process ends.
const RESTART_DELAY: Duration = Duration::from_secs(1);

// Downloads the release archive for this host, puts the new program in place of
// the running one, and starts it again with the same arguments. A failure leaves
// the running version in place.
pub async fn run(engine: &Engine) -> Result<(), Box<dyn Error + Send + Sync>> {
    let Some(current) = mobius_domain::RELEASE_VERSION else {
        return Err("This Mobius build is not a release.".into());
    };
    let release = engine.github.latest_release().await?;
    if !mobius_domain::newer_release(current, &release.tag_name) {
        return Err(format!("{} is the newest release.", release.tag_name).into());
    }
    let asset = format!("mobius-{}.tar.gz", target()?);
    if !release.assets.iter().any(|known| known.name == asset) {
        return Err(format!("Release {} has no {asset}.", release.tag_name).into());
    }
    let url = engine.github.release_url(&release.tag_name, &asset);
    let exe = tokio::task::spawn_blocking(move || install(&url)).await??;
    restart(exe);
    Ok(())
}

// The release archive name of the target of this build.
fn target() -> Result<&'static str, Box<dyn Error + Send + Sync>> {
    let (arch, os) = (std::env::consts::ARCH, std::env::consts::OS);
    match (arch, os) {
        ("aarch64", "macos") => Ok("aarch64-apple-darwin"),
        ("x86_64", "linux") => Ok("x86_64-unknown-linux-gnu"),
        _ => Err(format!("Mobius has no release for {arch} {os}.").into()),
    }
}

// Gives the path of the program that the new release replaced.
fn install(url: &str) -> Result<PathBuf, Box<dyn Error + Send + Sync>> {
    let exe = std::env::current_exe()?.canonicalize()?;
    let dir = exe.parent().ok_or("The mobius program has no directory.")?;
    let staging = tempfile::tempdir_in(dir)?;
    let archive = staging.path().join("mobius.tar.gz");
    run_command(
        Command::new("curl")
            .args(["-fsSL", url, "-o"])
            .arg(&archive),
    )?;
    run_command(
        Command::new("tar")
            .arg("-xzf")
            .arg(&archive)
            .current_dir(staging.path()),
    )?;
    if !staging.path().join("mobius").is_file() {
        return Err(format!("The archive at {url} has no mobius program.").into());
    }
    // The old files move aside first, so a failed swap can move them back.
    let previous = staging.path().join("previous");
    fs::create_dir(&previous)?;
    if let Err(error) = swap(staging.path(), &previous, &exe) {
        let _ = fs::rename(previous.join("mobius"), &exe);
        let _ = fs::rename(previous.join("public"), dir.join("public"));
        return Err(error);
    }
    Ok(exe)
}

fn swap(staging: &Path, previous: &Path, exe: &Path) -> Result<(), Box<dyn Error + Send + Sync>> {
    let dir = exe.parent().ok_or("The mobius program has no directory.")?;
    let public = dir.join("public");
    fs::rename(exe, previous.join("mobius"))?;
    if public.exists() {
        fs::rename(&public, previous.join("public"))?;
    }
    fs::rename(staging.join("mobius"), exe)?;
    if staging.join("public").is_dir() {
        fs::rename(staging.join("public"), public)?;
    }
    Ok(())
}

fn run_command(command: &mut Command) -> Result<(), Box<dyn Error + Send + Sync>> {
    let program = command.get_program().to_string_lossy().into_owned();
    let output = command.output()?;
    if !output.status.success() {
        return Err(format!(
            "`{program}` failed: {}",
            String::from_utf8_lossy(&output.stderr).trim()
        )
        .into());
    }
    Ok(())
}

// `exec` keeps the environment, the working directory, and the terminal.
fn restart(exe: PathBuf) {
    use std::os::unix::process::CommandExt;
    tokio::spawn(async move {
        tokio::time::sleep(RESTART_DELAY).await;
        let error = Command::new(&exe).args(std::env::args_os().skip(1)).exec();
        eprintln!("mobius: the restart after the upgrade failed: {error}");
        std::process::exit(1);
    });
}
