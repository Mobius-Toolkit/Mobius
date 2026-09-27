use dioxus::prelude::*;

const MAIN_CSS: Asset = asset!("/assets/main.css");

fn main() {
    #[cfg(not(feature = "server"))]
    dioxus::launch(App);

    #[cfg(feature = "server")]
    serve();
}

#[cfg(feature = "server")]
fn serve() {
    use std::env;
    use std::net::{IpAddr, Ipv4Addr};
    use std::path::PathBuf;

    use dioxus::server::axum::Extension;

    let config_path = env::var_os("MOBIUS_CONFIG")
        .map(PathBuf::from)
        .unwrap_or_else(|| {
            env::home_dir()
                .unwrap_or_default()
                .join(".mobius/config.toml")
        });
    let config = mobius_engine::config::load(&config_path).unwrap_or_else(|error| fail(error));

    let missing =
        mobius_engine::missing_commands(&config, &env::var_os("PATH").unwrap_or_default());
    for program in &missing {
        eprintln!("mobius: `{program}` is not on PATH");
    }
    if !missing.is_empty() {
        std::process::exit(1);
    }

    if let Some(ip) = dioxus::cli_config::server_ip()
        && ip != IpAddr::V4(Ipv4Addr::LOCALHOST)
        && ip != IpAddr::V4(Ipv4Addr::UNSPECIFIED)
    {
        fail(format!("IP: must be 127.0.0.1 or 0.0.0.0, not {ip}"));
    }

    let runtime = tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
        .unwrap_or_else(|error| fail(error));
    let _runtime_guard = runtime.enter();
    let store = runtime
        .block_on(mobius_store::Store::open(&config.data_dir))
        .unwrap_or_else(|error| {
            fail(format!(
                "{}: {error}",
                config.data_dir.join("mobius.db").display()
            ))
        });
    let engine = mobius_engine::start(config, store.clone());

    dioxus::serve(move || {
        let engine = engine.clone();
        let store = store.clone();
        async move {
            Ok(dioxus::server::router(App)
                .layer(Extension(engine))
                .layer(Extension(store)))
        }
    });
}

#[cfg(feature = "server")]
fn fail(message: impl std::fmt::Display) -> ! {
    eprintln!("mobius: {message}");
    std::process::exit(1)
}

#[component]
fn App() -> Element {
    rsx! {
        document::Stylesheet { href: MAIN_CSS }
        mobius_ui::Shell {}
    }
}
