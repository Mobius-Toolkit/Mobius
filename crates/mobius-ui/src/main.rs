fn main() {
    // dioxus-web installs its panic hook only in devtools/debug builds; in
    // release a wasm panic surfaces as a bare `RuntimeError: unreachable`.
    // Log the panic message to console.error instead.
    std::panic::set_hook(Box::new(|info| {
        web_sys::console::error_1(&format!("mobius-ui panicked: {info}").into());
    }));
    dioxus::launch(mobius_ui::App);
}
