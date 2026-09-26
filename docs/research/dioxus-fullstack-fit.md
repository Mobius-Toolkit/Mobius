# Dioxus fullstack fit

Research for issue #15. Date: 2026-09-26.

## Question

Does Dioxus fullstack (current stable version) support what the Mobius server needs?

- Streamed chat updates (WebSocket or server-sent events).
- Long-lived background tasks in the server process.
- A login cookie that survives server restarts and `dx serve` hot reload.
- A Cargo workspace of separate library crates.
- Later desktop and mobile builds from the same code.

Also: resource use and known limits.

## Answer

Yes. Dioxus 0.7.10 supports all five needs. Two needs require our own code: authentication (Dioxus has no session support) and client reconnect (Dioxus has no automatic reconnect). A minimal release server uses about 8-10 MB of resident memory. The minimal web client is about 390 KB of WebAssembly after gzip.

## Version

- The current stable version is **0.7.10** (2026-07-30). Source: [GitHub release v0.7.10](https://github.com/DioxusLabs/dioxus/releases/tag/v0.7.10), [crates.io `dioxus` versions](https://crates.io/crates/dioxus/versions).
- 0.8 is in alpha (`v0.8.0-alpha.1`, 2026-07-31). 0.8 moves to Rust edition 2024 and enables hot-patch by default. Source: [v0.8.0-alpha.0 release notes](https://github.com/DioxusLabs/dioxus/releases/tag/v0.8.0-alpha.0).
- `dioxuslabs.com/llms.txt` does not exist (HTTP 404 on 2026-09-26). This research uses the docs source in [DioxusLabs/docsite `docs-src/0.7`](https://github.com/DioxusLabs/docsite/tree/main/docs-src/0.7) and the 0.7.10 source code.

Abbreviations: SSE = server-sent events. SSR = server-side rendering. WASM = WebAssembly. RSS = resident set size.

## 1. Streamed chat updates

Supported. Dioxus has three typed transports. Each transport is a server function, so the client calls it as an `async fn`.

| Transport | Server return type | Direction | Client side |
|---|---|---|---|
| WebSocket | `Websocket<In, Out, Encoding>` | both | `use_websocket` hook, `.send()`, `.recv()` |
| SSE | `ServerEvents<T>` | server to client | `.recv()` in `use_future` |
| HTTP stream | `TextStream`, `ByteStream`, `Streaming<T, Encoding>` | one direction | implements `futures::Stream` |

- WebSocket: `#[get("/api/ws")] async fn ws(options: WebSocketOptions) -> Result<Websocket<ClientEvent, ServerEvent>>`. The server body runs in `options.on_upgrade(...)`. Encodings are JSON (default), CBOR, MsgPack, and Postcard. Source: [docs: Websockets](https://dioxuslabs.com/learn/0.7/essentials/fullstack/websockets/), [example `websocket.rs`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/examples/07-fullstack/websocket.rs).
- SSE: `ServerEvents::new(|mut tx| async move { tx.send(event).await })`. A failed `send` tells the server that the client disconnected. The server sends a keep-alive every 15 s by default. Source: [example `server_sent_events.rs`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/examples/07-fullstack/server_sent_events.rs), [`sse.rs` L304](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack/src/payloads/sse.rs#L304).
- HTTP stream: `TextStream::spawn(|tx| ...)` or `Streaming::new(rx)` from any `Stream`. The docs name LLM token output as a use case. Source: [docs: Streams and SSE](https://dioxuslabs.com/learn/0.7/essentials/fullstack/streams/), [example `streaming.rs`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/examples/07-fullstack/streaming.rs).

Sharp edges:

- `WebSocketOptions::with_automatic_reconnect()` sets a flag, but no code in 0.7.10 reads the flag. The client must reconnect itself. Source: [`websocket.rs` L517-L541](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack/src/payloads/websocket.rs#L517-L541) (search the package for `automatic_reconnect`: only the struct field, the constructor, and the setter use it).
- The SSE client reads the response body with `fetch`/`reqwest`, not with the browser `EventSource`. Thus the browser does not reconnect and does not send `Last-Event-ID`. Source: [`sse.rs` L110](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack/src/payloads/sse.rs#L110).
- In a debug build, each hot-patch closes all open connections, WebSocket and SSE included. Source: [`launch.rs` L234-L243](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack-server/src/launch.rs#L234-L243).
- The web client opens the WebSocket with the URL path only, so the socket goes to the page origin. Source: [`websocket.rs` L620-L625](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack/src/payloads/websocket.rs#L620-L625). Open issue: the WebSocket request ignores `base_path` ([#5584](https://github.com/DioxusLabs/dioxus/issues/5584)).

## 2. Long-lived background tasks in the server process

Supported. The server is a normal axum 0.8 server on a tokio multi-thread runtime. Source: [workspace `Cargo.toml` L242](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/Cargo.toml#L242).

- `dioxus::serve(|| async { ... Ok(router) })` gives full control of the axum `Router`. Use `dioxus::server::router(app)` to get the Dioxus routes, then add routes, layers, and state. Source: [docs: Axum Router](https://dioxuslabs.com/learn/0.7/essentials/fullstack/axum/), [example `custom_axum_serve.rs`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/examples/07-fullstack/custom_axum_serve.rs).
- Shared state has four options: a `LazyLock` static, a Dioxus `Lazy<T>` static (async init), an axum `Extension` layer, and `State<T>` with `FromRef<FullstackContext>`. Server functions read state through server-only extractors: `#[post("/api/x", ext: Extension<Hub>)]`. Source: [example `server_state.rs`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/examples/07-fullstack/server_state.rs), [docs: Axum Router, "Adding State with Extensions"](https://dioxuslabs.com/learn/0.7/essentials/fullstack/axum/).
- The `serve` closure runs inside the tokio runtime, so `tokio::spawn` works there. Source: [`launch.rs` L115-L126 and L253-L262](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack-server/src/launch.rs#L115-L126).
- Server functions run on a `tokio_util::task::LocalPoolHandle`, so their futures do not need to be `Send`. Source: [`serverfn.rs` L76](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack-server/src/serverfn.rs#L76).

Sharp edges:

- In a debug build, Dioxus calls the `serve` closure again after each hot-patch to rebuild the router. A task that the closure spawns will start again after each patch. Start background tasks one time (for example behind a `OnceLock`), not in the closure body. In a release build, Dioxus calls the closure one time. Source: [`launch.rs` L146-L158 and L234-L240](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack-server/src/launch.rs#L146-L158).
- `dioxus::serve` uses the current tokio runtime if one exists. Otherwise it builds a default multi-thread runtime. Source: [`launch.rs` L253-L262](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack-server/src/launch.rs#L253-L262).

## 3. Login cookie that survives restarts and hot reload

Supported with our own code. Dioxus has cookie primitives but no session or auth layer.

- The docs say: "Dioxus does *not* provide a built-in way of managing authentication". The docs point to axum middleware and to `axum_session_auth`. Source: [docs: Authentication](https://dioxuslabs.com/learn/0.7/essentials/fullstack/authentication/), [example `auth`](https://github.com/DioxusLabs/dioxus/tree/v0.7.10/examples/07-fullstack/auth).
- To set a cookie, a server function returns `SetHeader<SetCookie>`. To read it, use the server-only extractor `TypedHeader<Cookie>`. Source: [example `login_form.rs`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/examples/07-fullstack/login_form.rs).
- In that example, the session ID is a random value in a `static`. Thus all logins become invalid when the server restarts. A cookie survives a restart only when the server can validate it after the restart. Two ways:
  - A session row in a database (for example `axum_session` with SQLite). Source: [example `auth/Cargo.toml`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/examples/07-fullstack/auth/Cargo.toml).
  - A signed or private cookie (`axum-extra` `SignedCookieJar`) with a key that the server stores on disk. The axum-extra docs warn against a new key at each start. Source: [docs.rs `SignedCookieJar`](https://docs.rs/axum-extra/latest/axum_extra/extract/cookie/struct.SignedCookieJar.html).
- `dx serve` runs a devserver that forwards requests to the fullstack server on a different port. The browser origin is the devserver, so the browser keeps the cookie when the server process restarts. Source: [`cli/src/serve/server.rs` L479-L483](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/cli/src/serve/server.rs#L479-L483).
- `dx serve` restarts the server process after a full rebuild. Hot-patch (`--hotpatch`) patches only the "tip" crate. A change in a workspace library crate causes a full rebuild. Thus in a workspace, most server changes restart the process. Source: [docs: Hot-Reloading, "Experimental: Rust Hot-patching"](https://dioxuslabs.com/learn/0.7/essentials/ui/hotreload/), [issue #5245](https://github.com/DioxusLabs/dioxus/issues/5245).

## 4. Workspace of separate library crates

Supported. The official "Workspace" template uses this layout.

- Template layout: `packages/api` (server functions), `packages/ui` (shared components), and `packages/web`, `packages/desktop`, `packages/mobile` (one binary each). Source: [dioxus-template `v0.7/Workspace`](https://github.com/DioxusLabs/dioxus-template/tree/v0.7/Workspace).
- Feature rules:
  - `dx` builds a fullstack app two times: one client build (`web`, `desktop`, or `mobile` feature) and one server build (`server` feature). Source: [docs: Project Setup](https://dioxuslabs.com/learn/0.7/essentials/fullstack/project_setup/).
  - Each library crate defines its own `server` feature and forwards it down: `api` has `server = ["dioxus/server"]`, `ui` has `server = ["api/server"]`, `web` has `server = ["dioxus/server", "ui/server"]`. Source: [template `packages/*/Cargo.toml`](https://github.com/DioxusLabs/dioxus-template/tree/v0.7/Workspace/packages).
  - Server-only dependencies (`tokio`, `sqlx`, and similar) must be `optional = true` and enabled only by `server`. Otherwise the WASM build fails. Source: [docs: Project Setup, "Adding Server Only Dependencies"](https://dioxuslabs.com/learn/0.7/essentials/fullstack/project_setup/).
- Server functions register themselves at link time through `inventory`. A server function in a library crate becomes a route when the binary links that crate. Source: [`fullstack-server/src/lib.rs`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack-server/src/lib.rs) (`pub use inventory`), [docs: Axum Router, "Registering Server Functions"](https://dioxuslabs.com/learn/0.7/essentials/fullstack/axum/).
- A separate server binary is possible: `dx serve @client --package web @server --package server`. This fits a later split into server and runner nodes. Source: [docs: Project Setup, "Customizing the Builds"](https://dioxuslabs.com/learn/0.7/essentials/fullstack/project_setup/), [issue #3614 comment](https://github.com/DioxusLabs/dioxus/issues/3614).

Sharp edges:

- Open issue: the `workspace` template with fullstack does not run from `dx new` ([#4230](https://github.com/DioxusLabs/dioxus/issues/4230)).
- Hot-patch does not work across workspace crates (see section 3).

## 5. Later desktop and mobile builds

Supported. The same server functions work from desktop and mobile clients.

- Build with `dx serve --desktop`, `--ios`, or `--android`. Desktop and mobile use the system WebView (wry). Source: [docs: Native Clients](https://dioxuslabs.com/learn/0.7/essentials/fullstack/native/), [docs: Desktop](https://dioxuslabs.com/learn/0.7/guides/platforms/desktop/), [docs: Mobile](https://dioxuslabs.com/learn/0.7/guides/platforms/mobile/).
- To reach a remote server, call `dioxus::fullstack::set_server_url("https://...")` before the first request. Source: [docs: Native Clients](https://dioxuslabs.com/learn/0.7/essentials/fullstack/native/), [example `desktop`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/examples/07-fullstack/desktop/src/main.rs).
- Use explicit paths (`#[post("/api/x")]`), not `#[server]`. `#[server]` makes a hashed path that can change between builds. Source: [docs: Native Clients, "Prefer Known Endpoints"](https://dioxuslabs.com/learn/0.7/essentials/fullstack/native/).
- The native client uses `reqwest` with WebSocket support through `tungstenite`. Source: [`fullstack/Cargo.toml`](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack/Cargo.toml).

Sharp edges:

- `set_server_url` takes `&'static str` and writes a `OnceLock`. The app cannot change the server URL after the first call. A URL that the Owner types in at run time needs `Box::leak` and a restart to change it. Source: [`client.rs` L500-L509](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack/src/client.rs#L500-L509).
- The native cookie jar is in memory only. The login cookie is lost when the app closes. The app must store a token itself and send it with `set_request_headers` (for example an `Authorization` header). Source: [`client.rs` L115-L131 and L517](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack/src/client.rs#L115-L131).
- Native apps have no SSR and no hydration. Source: [docs: Native Clients, "Disabled Fullstack Features"](https://dioxuslabs.com/learn/0.7/essentials/fullstack/native/).

## Resource use

### Measurement

Setup: a one-file fullstack app with one SSE server function and one page. Dioxus 0.7.10, `dx` 0.7.10, Rust 1.98.1, macOS on Apple silicon (10 cores). Release profile: `opt-level = "z"`, `lto = true`, `codegen-units = 1`, `panic = "abort"`. Command: `dx build --release --web --fullstack`.

| Item | Size |
|---|---|
| Server binary (not stripped) | 3.6 MB |
| Server binary (after `strip`) | 1.7 MB |
| Client `.wasm` (after `dx` runs `wasm-bindgen` and `wasm-opt`) | 1.06 MB |
| Client `.wasm`, gzip -9 | 387 KB |
| Client `.wasm`, brotli -q 11 | 312 KB |
| Client JavaScript glue, gzip -9 | 12 KB |

| Server state | RSS | Threads |
|---|---|---|
| After start and 1 page request | 8.3 MB | 21 |
| After 50 SSR page requests | 9.0 MB | 21 |
| With 20 open SSE streams | 10.0 MB | 31 |

This is a lower limit. Mobius will add a database, a GitHub client, and child processes.

### Documented figures

- The Dioxus optimization guide reports a TodoMVC WASM file of 2.36 MB in release mode, 310 KB with the stable size profile, and 234 KB with nightly flags. Source: [docs: Optimizing](https://dioxuslabs.com/learn/0.7/guides/tips/optimizing/).
- 0.7 adds WASM code split per route (`wasm-split`). Source: [0.7 release notes](https://github.com/DioxusLabs/docsite/blob/main/docs-src/blog/src/release-070.md).
- Desktop apps are "typically under 5MB". Source: [docs: Desktop](https://dioxuslabs.com/learn/0.7/guides/platforms/desktop/).

### Thread count

The thread count scales with CPU count, not with load:

- tokio multi-thread runtime: one worker for each CPU.
- `FullstackState` makes a `LocalPoolHandle` with `available_parallelism()` threads for server functions and SSR. Source: [`server.rs` L229-L275](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack-server/src/server.rs#L229-L275).
- The first stream or WebSocket makes a second `LocalPoolHandle` of the same size. Source: [`spawn.rs` L13-L24](https://github.com/DioxusLabs/dioxus/blob/v0.7.10/packages/fullstack/src/spawn.rs#L13-L24).

The pool size is not configurable in 0.7.10. On a small host (1-2 CPUs) the count stays low. In the measurement above, 10 extra threads and 20 open streams together add about 1 MB of RSS.

## Other known limits

- A new server deploy can break open web clients, because a `#[server]` path changes and the browser caches the old WASM. Use explicit paths. Source: [issue #3550](https://github.com/DioxusLabs/dioxus/issues/3550).
- Open bug: middleware does not run when a component calls a server function through `use_server_future` ([#4117](https://github.com/DioxusLabs/dioxus/issues/4117)). Put auth checks in the server function (extractor), not only in a layer.
- Server functions have no built-in timeout ([#3110](https://github.com/DioxusLabs/dioxus/issues/3110)).
- A 0.7 to 0.8 upgrade will have breaking changes to internal APIs. Source: [v0.8.0-alpha.0 release notes](https://github.com/DioxusLabs/dioxus/releases/tag/v0.8.0-alpha.0).

## Implications for Mobius

1. Use Dioxus 0.7.10. Pin the exact version, and plan one upgrade to 0.8 when 0.8 is stable.
2. Use SSE (`ServerEvents<T>`) for chat updates from the server to the browser. Use normal server functions for Owner input. Use a WebSocket only if a later feature needs both directions on one connection.
3. Write a client reconnect loop with an event sequence number, so that the client can ask for missed events. Dioxus has no automatic reconnect for WebSocket or SSE, and a hot-patch closes all connections.
4. Start the agent runtime (Harness child processes, GitHub poll) one time in `main`, before `dioxus::serve`, or behind a `OnceLock`. Give server functions a handle through an axum `Extension`. Do not spawn tasks in the `serve` closure body.
5. Write a small auth layer: one Owner password, a session row in the Mobius database, and an `HttpOnly` cookie. Alternatively, use a signed cookie with a key that the Mobius server stores on disk. Both ways survive restarts.
6. Use the Workspace template layout. Each library crate has a `server` feature that it forwards. Server-only dependencies are optional. The agent runtime crate has no Dioxus dependency, so a later runner node can use it without the UI.
7. Use explicit server function paths (`#[get("/api/...")]`) everywhere. Desktop and mobile clients need stable paths.
8. For later native clients: store the auth token in the app, send it with `set_request_headers`, and accept that the server URL is fixed after start.
9. Expect a server of about 10 MB RSS before Mobius code, a stripped binary under 5 MB, and a first web load of about 400 KB compressed.
