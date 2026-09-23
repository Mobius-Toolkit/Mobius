#!/usr/bin/env bash
# Mobius dev entrypoint: scripts/mobius.sh [serve|ui|seed] [--build-ui] [--no-open]
set -euo pipefail
cd "$(dirname "$0")/.."

CONFIG="${MOBIUS_CONFIG:-mobius.toml}"
CMD="serve"
BUILD_UI=0
OPEN=1
for arg in "$@"; do
    case "$arg" in
        serve|ui|seed) CMD="$arg" ;;
        --build-ui) BUILD_UI=1 ;;
        --no-open) OPEN=0 ;;
        *) echo "usage: $0 [serve|ui|seed] [--build-ui] [--no-open]" >&2; exit 2 ;;
    esac
done

# Wait for /api/v1/health, then open the UI. Runs in the background so the
# server keeps the foreground (and Ctrl+C). MOBIUS_BROWSER overrides the
# opener command (e.g. MOBIUS_BROWSER=echo for headless runs).
open_when_ready() {
    local bind url opener
    bind="$(sed -n 's/^bind *= *"\([^"]*\)".*/\1/p' "$CONFIG" | head -1)"
    url="http://${bind:-127.0.0.1:8787}"
    opener="${MOBIUS_BROWSER:-}"
    if [ -z "$opener" ]; then
        if command -v open >/dev/null 2>&1; then opener=open
        elif command -v xdg-open >/dev/null 2>&1; then opener=xdg-open
        else echo "no browser opener found; open $url manually" >&2; return; fi
    fi
    for _ in $(seq 1 600); do
        if curl -fsS "$url/api/v1/health" >/dev/null 2>&1; then
            "$opener" "$url"
            return
        fi
        sleep 0.5
    done
    echo "server did not become healthy at $url; not opening browser" >&2
}

build_ui() {
    DX="$(command -v dx || true)"
    if [ -z "$DX" ] && [ -x "$HOME/.cargo/bin/dx" ]; then
        DX="$HOME/.cargo/bin/dx"
    fi
    if [ -z "$DX" ]; then
        echo "dx not found; install with: cargo install dioxus-cli --version 0.7.10" >&2
        exit 1
    fi
    # --debug-symbols false: rust 1.98 DWARF crashes dx's bundled wasm-opt.
    (cd crates/mobius-ui && "$DX" build --release --debug-symbols false)
}

case "$CMD" in
    ui)
        build_ui
        ;;
    seed)
        cargo run -p mobius-server -- --config "$CONFIG" seed-dev --repo "$(pwd)"
        ;;
    serve)
        if [ ! -f "$CONFIG" ]; then
            cp mobius.example.toml "$CONFIG"
            echo "created $CONFIG from mobius.example.toml"
        fi
        # Server + CLI from the same target dir: SessionManager prepends the
        # directory containing mobius-server to harness PATH, so `mobius`
        # must sit next to it.
        cargo build --release -p mobius-server -p mobius-cli
        if [ "$BUILD_UI" = 1 ] || \
           [ ! -f target/dx/mobius-ui/release/web/public/index.html ]; then
            build_ui
        fi
        if [ "$OPEN" = 1 ]; then
            open_when_ready &
        fi
        exec ./target/release/mobius-server --config "$CONFIG" serve
        ;;
esac
