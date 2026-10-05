# Measurements of the Go port and the Rust version

This document gives the numbers of issue #4, with the scope of its last comment. It compares two versions on the same machine:

- Go: main of this repository, commit `ab56481c28316a069950a5e95091c2e199089794`. This is the full port, with milestones 1 to 5 of #12.
- Rust: main of Mobius-Toolkit/Mobius-rust, commit `bdcc140f02ce7046bfb7efc653070f67c0120665`.

The measurements ran on 2026-10-05 between 04:11 and 05:52 local time.

## Results

Each cell gives the wall time and the peak memory of one run. A cold run starts with new empty cache folders and a clean clone. A warm run uses the caches of the run before it. The section [Commands](#commands) gives the exact commands.

| Measurement | Rust run 1 | Rust run 2 | Go run 1 | Go run 2 |
| --- | --- | --- | --- | --- |
| Cold build (debug) | 64.7 s, 2004 MiB | 64.1 s, 2000 MiB | 16.9 s, 753 MiB | 16.3 s, 789 MiB |
| Warm build, no change | 0.77 s, 147 MiB (run 3: 0.52 s) | 0.53 s, 147 MiB | 0.27 s, 47 MiB | 0.26 s, 48 MiB |
| Warm build after one edit in the engine | 4.0 s, 859 MiB | 4.0 s, 858 MiB | 1.4 s, 480 MiB | 1.4 s, 496 MiB |
| `.mobius/check`, cold | 420.5 s, 2688 MiB | 416.9 s, 2688 MiB | 315.6 s, 1515 MiB | 315.9 s, 1543 MiB |
| `.mobius/check`, warm | 267.8 s, 1058 MiB | 271.7 s, 1058 MiB | 18.3 s, 495 MiB | 17.8 s, 487 MiB |
| CI test command, cold | 333.5 s, 2688 MiB | 327.8 s, 2687 MiB | 249.1 s, 750 MiB | 247.7 s, 751 MiB |
| CI test command, warm | 257.3 s, 1056 MiB | 257.3 s, 1058 MiB | 5.7 s, 92 MiB (cached results) | 5.7 s, 92 MiB (cached results) |
| CI test command, warm, all tests run again | same as the row above | same as the row above | 226.1 s, 419 MiB | 225.1 s, 409 MiB |
| UI tests, cold build cache | 153.5 s, 2688 MiB | 147.1 s, 2688 MiB | 38.8 s, 717 MiB | 38.4 s, 762 MiB |
| UI tests, warm | 91.2 s, 1056 MiB | 91.6 s, 1059 MiB | 22.5 s, 433 MiB | 21.5 s, 427 MiB |
| `pnpm install`, cold store | not applicable | not applicable | 5.3 s, 338 MiB | 5.8 s, 310 MiB |
| Vite build | not applicable | not applicable | 1.9 s, 361 MiB | 1.8 s, 361 MiB |
| `pnpm build` (`tsc -b` and the Vite build) | not applicable | not applicable | 2.1 s, 439 MiB | 2.1 s, 433 MiB |
| Release build, cold | 149.0 s, 2196 MiB | 145.0 s, 2204 MiB | 24.3 s, 708 MiB | 23.8 s, 752 MiB |
| Release binary, `aarch64-apple-darwin` | 38.7 MiB, and 3.6 MiB in `public/` | 38.7 MiB, and 3.6 MiB in `public/` | 36.7 MiB, the UI is in the binary | 36.7 MiB, the UI is in the binary |
| Release archive (`tar.gz`), `aarch64-apple-darwin` | 13.4 MiB | 13.4 MiB | 17.9 MiB | 17.9 MiB |
| Release binary, `x86_64-unknown-linux-gnu` | not measured | not measured | 37.8 MiB | 37.8 MiB |
| Disk use after `.mobius/check`: build cache | `target`: 11.59 GiB | `target`: 11.58 GiB | `GOCACHE`: 889 MiB, golangci-lint cache: 12.6 MiB | `GOCACHE`: 889 MiB, golangci-lint cache: 12.6 MiB |
| Disk use after `.mobius/check`: dependencies | `CARGO_HOME`: 537 MiB | `CARGO_HOME`: 537 MiB | `GOMODCACHE`: 1231 MiB, pnpm store: 271 MiB | `GOMODCACHE`: 1231 MiB, pnpm store: 271 MiB |
| Disk use after `.mobius/check`: worktree without `.git` | 3.7 MiB | 3.7 MiB | 285.6 MiB, of which `node_modules` is 282.4 MiB | 285.6 MiB, of which `node_modules` is 282.4 MiB |
| Disk use after `.mobius/check`: total | 12.12 GiB | 12.11 GiB | 2.62 GiB | 2.62 GiB |
| Disk use of `target` after the release build | 2.04 GiB | 2.04 GiB | not applicable | not applicable |

The two runs of each number differ by less than 20%, except the warm Rust build with no change (0.77 s and 0.53 s). A third run of it gave 0.52 s.

The peak memory is the "maximum resident set size" of `/usr/bin/time -l`. On macOS, this value is the peak of the largest single process in the command tree. It is not the sum of the processes that run at the same time.

## What the numbers show

- The Go port compiles faster. A cold build takes 16 s in Go and 64 s in Rust. A build after one edit in the engine takes 1.4 s in Go and 4.0 s in Rust.
- A cold release build takes about 24 s in Go and about 147 s in Rust. The Rust release compiles the server and the WebAssembly UI with optimization.
- In both versions, most of the time of `.mobius/check` goes to the tests, not to the compiler. In Go, the package `internal/engine` takes 226 s of the test time. In Rust, cargo starts the test binaries one after the other, and their run times add up to 206 s.
- The cold Rust check spends about 173 s in the compiler: 59 s for Clippy on the server, 29 s for Clippy on the WebAssembly UI, 10 s for the sqlx step, and 75 s to compile the tests. The tests take the rest of the time.
- The warm Go check takes 18 s because `go test` keeps the result of each package that did not change. `cargo test` runs all tests again each time. After a change in `internal/engine`, Go runs the engine tests again, and this takes about 226 s.
- The Go port uses less disk. After `.mobius/check`, Go uses 2.62 GiB and Rust uses 12.12 GiB. The Rust `target` folder alone is 11.6 GiB.
- The Go port uses less memory. The largest process of a Go build uses 0.75 GiB, and of a Rust build 2.0 GiB. The largest process of the Go check uses 1.5 GiB, and of the Rust check 2.6 GiB.
- The two release binaries have almost the same size. The Go archive is 4.5 MiB larger than the Rust archive.
- The UI tests are not the same tests. Go has 14 Playwright tests in `web/e2e`. Rust has 13 Chrome tests in `crates/mobius-testkit/tests/screenshots.rs` that run one at a time, as in its CI.

## Size of the code

At the measured commit, the Go version is the full port. It is not smaller in function than the Rust version. Thus the question of #4, which part of the Rust build time comes from code that the Go version does not have, does not apply. The largest difference is the UI. Rust compiles the UI to WebAssembly: Clippy on it takes 29 s of the cold check. Go builds the UI with Vite in 2 s.

These line counts include blank lines and comments:

| Code | Rust | Go |
| --- | --- | --- |
| Source files | 37,876 lines of Rust, with the UI | 31,863 lines of Go and 5,452 lines of TypeScript |
| Tests and test kit | 18,193 lines in `tests/` folders and `mobius-testkit` | 18,349 lines in `_test.go` files and `internal/testkit`, and 1,096 lines in `web/e2e` |

The Rust count of tests does not include the unit tests inside the source files. The Go count of source files does not include the generated sqlc files.

The Go test command ran 14 packages, and all passed. Playwright ran 14 tests, and all passed. The Rust test command ran 354 tests, and all passed. It ignored 16 tests:

- 13 tests that start Chrome. The row "UI tests" gives their time.
- 3 tests that start a real agent (Claude Code, Devin and Antigravity). These tests were not run.

## Machine

- Apple M1 Pro, 10 cores (8 performance cores and 2 efficiency cores)
- 32 GiB RAM
- macOS 27.0 (build 26A428)
- Go 1.27.1
- Rust 1.98.1, the stable toolchain. The Rust repository has no `rust-toolchain.toml`. Its CI also uses the stable toolchain.
- Node 24.3.0 and pnpm 12.0.0
- golangci-lint 2.14.0, dioxus-cli 0.7.10, sqlx-cli 0.9.0
- Playwright 1.63.0 with its Chromium 1243, and Google Chrome 154.0.8037.93 for the Rust UI tests
- Apple clang 21.0.0 and git 2.54.0

The machine also ran a live Rust Mobius server and other work. The [raw results](#raw-results) give the load average at the start of each run. The 1-minute load at the start of a run includes the run before it.

## Commands

Each measurement ran in a fresh clone in a temporary folder:

```sh
M=$TMPDIR/mobius-measure
git clone https://github.com/Mobius-Toolkit/Mobius $M/go
git clone https://github.com/Mobius-Toolkit/Mobius-rust $M/rust
```

Each command ran with these environment variables. `C` is a new empty folder for each cold measurement. The warm runs after it use the same `C`.

```sh
C=$M/caches/<name>
export HOME=$M/home
export RUSTUP_HOME=<the home folder of the user>/.rustup
export GOCACHE=$C/gocache GOMODCACHE=$C/gomod GOLANGCI_LINT_CACHE=$C/golangci
export CARGO_HOME=$C/cargo CARGO_TARGET_DIR=$C/target RUSTC_WRAPPER=
export pnpm_config_store_dir=$C/pnpm-store pnpm_config_cache_dir=$C/pnpm-cache
export PLAYWRIGHT_BROWSERS_PATH=$M/playwright
```

- `HOME` points to a temporary folder. Thus no tool reads or writes the files of the user. dioxus-cli downloads wasm-bindgen and esbuild again into this folder.
- `RUSTUP_HOME` keeps its usual value, so that rustup does not download the toolchain again.
- `RUSTC_WRAPPER=` (empty) turns off sccache.
- pnpm 12 reads `pnpm_config_*` variables. It does not read `npm_config_store_dir`.
- `cargo-sqlx` and `dx` come from `PATH`. The CI of the Rust repository installs them with `cargo install`. This time is not in the numbers.

Each number comes from `/usr/bin/time -l <command>`. Before each cold run, the clone gets `git checkout . && git clean -xdf`.

### Rust

Run these commands in `$M/rust`:

| Measurement | Command |
| --- | --- |
| Cold build, warm build | `cargo build --workspace --features mobius/server` |
| Warm build after one edit | `echo '// measure' >> crates/mobius-engine/src/lib.rs`, then the build command |
| `.mobius/check`, cold and warm | `.mobius/check` |
| CI test command, cold and warm | `cargo test --workspace --features mobius/server` |
| Release build, cold | `MOBIUS_VERSION=v0.0.0-measure dx bundle --web --release -p mobius --out-dir dist` |
| UI tests | First `dx bundle --web --release -p mobius --out-dir dist` (not in the time), then `DIOXUS_PUBLIC_PATH=$M/rust/dist/public MOBIUS_VERSION=v0.1.0 cargo test -p mobius-testkit --test screenshots -- --ignored --test-threads=1` |

The release command is the command of `.github/workflows/release.yml` of the Rust repository. The binary is `dist/server`, and the archive holds `dist/server` and `dist/public`. The UI test command is the command of `.github/workflows/screenshots.yml`. Its cold run has a release cache from `dx bundle`, but no test cache.

The warm check, the warm test command and the disk use come after the cold check, with its caches.

### Go

Run these commands in `$M/go`:

| Measurement | Command |
| --- | --- |
| Cold build, warm build | `go build -o bin/mobius ./cmd/mobius` |
| Warm build after one edit | `echo '// measure' >> internal/engine/engine.go`, then the build command |
| CI test command, cold and warm | `go test ./...` |
| CI test command, all tests run again | `go test -count=1 ./...` |
| `pnpm install`, cold store | `cd web && pnpm install --frozen-lockfile` |
| Vite build | `cd web && pnpm exec vite build` |
| `pnpm build` | `cd web && pnpm build` |
| `.mobius/check`, cold and warm | `.mobius/check` |
| UI tests | `cd web && pnpm e2e` |
| Release build, cold | `cd web && pnpm install --frozen-lockfile && pnpm build && cd .. && CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -ldflags '-X github.com/Mobius-Toolkit/mobius-go/internal/engine.Release=v0.0.0-measure' -o aarch64-apple-darwin/mobius ./cmd/mobius` |
| Release binary for Linux, warm | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags '-X github.com/Mobius-Toolkit/mobius-go/internal/engine.Release=v0.0.0-measure' -o x86_64-unknown-linux-gnu/mobius ./cmd/mobius` |

The cold build has no UI in `web/dist`, only `web/dist/.gitkeep`. The release command is the command of `.github/workflows/release.yml`: the UI first, then the binary with `CGO_ENABLED=0` and the ldflags.

The warm UI tests come after the warm check, with its caches. The cold UI tests start with new caches and run `pnpm install --frozen-lockfile` first (not in the time). `pnpm exec playwright install chromium` ran one time before the first UI test, in 14.6 s.

The disk numbers come from `du -sk -I .git` after the warm check.

## What was not measured

- The Rust release for `x86_64-unknown-linux-gnu`. Its CI builds it on Ubuntu 22.04, and this machine is a Mac. The Go release for Linux is a cross-compile on this machine.
- The 3 Rust tests that start a real agent. The tests of both versions do not call the real GitHub or a real agent.
- The time to install sqlx-cli, dioxus-cli and golangci-lint. The installed versions are the same as the versions of CI.

## Raw results

The load is the load average of 1, 5 and 15 minutes at the start of the run. `rust-build-prep-3` made the caches for the third warm Rust build. `rust-build-warm-4` is an extra run that the results do not use.

| Time | Run | Wall time (s) | Peak memory (MiB) | Load |
| --- | --- | --- | --- | --- |
| 04:13:37 | `rust-build-cold-1` | 64.74 | 2004 | 3.41 3.93 3.71 |
| 04:13:38 | `rust-build-warm-1` | 0.77 | 147 | 9.19 5.67 4.38 |
| 04:13:42 | `rust-build-edit-1` | 4.03 | 859 | 8.86 5.66 4.38 |
| 04:20:45 | `rust-check-cold-1` | 420.52 | 2688 | 8.63 5.67 4.39 |
| 04:25:13 | `rust-check-warm-1` | 267.75 | 1058 | 4.93 7.01 5.90 |
| 04:29:31 | `rust-test-warm-1` | 257.33 | 1056 | 4.96 7.74 6.81 |
| 04:35:14 | `rust-test-cold-1` | 333.53 | 2688 | 5.79 7.37 6.99 |
| 04:37:47 | `rust-release-cold-1` | 149.01 | 2196 | 5.77 8.64 8.10 |
| 04:40:41 | `rust-ui-bundle-1` | 145.19 | 2190 | 27.04 16.94 11.53 |
| 04:43:15 | `rust-ui-test-cold-1` | 153.48 | 2688 | 18.42 17.08 12.40 |
| 04:44:46 | `rust-ui-test-warm-1` | 91.24 | 1056 | 8.56 13.50 11.71 |
| 04:45:11 | `go-build-cold-1` | 16.85 | 753 | 6.06 11.20 11.01 |
| 04:45:12 | `go-build-warm-1` | 0.27 | 47 | 7.07 11.15 11.00 |
| 04:45:13 | `go-build-edit-1` | 1.42 | 480 | 7.07 11.15 11.00 |
| 04:49:23 | `go-test-cold-1` | 249.07 | 750 | 7.07 11.08 10.97 |
| 04:49:29 | `go-test-warm-1` | 5.66 | 92 | 2.51 6.88 9.20 |
| 04:53:15 | `go-test-warm-count1-1` | 226.08 | 419 | 2.39 6.78 9.15 |
| 04:53:22 | `go-pnpm-install-cold-1` | 5.27 | 338 | 2.40 4.50 7.59 |
| 04:53:24 | `go-vite-build-1` | 1.88 | 361 | 2.37 4.46 7.56 |
| 04:53:26 | `go-pnpm-build-1` | 2.14 | 439 | 2.50 4.45 7.54 |
| 04:58:46 | `go-check-cold-1` | 315.58 | 1515 | 2.54 4.43 7.51 |
| 04:59:05 | `go-check-warm-1` | 18.27 | 495 | 7.43 5.56 7.13 |
| 04:59:22 | `go-playwright-install` | 14.56 | 282 | 6.86 5.57 7.09 |
| 04:59:45 | `go-e2e-warm-1` | 22.48 | 433 | 6.50 5.55 7.06 |
| 05:00:02 | `go-e2e-deps-1` | 5.10 | 334 | 4.64 5.19 6.87 |
| 05:00:41 | `go-e2e-cold-1` | 38.77 | 717 | 5.47 5.35 6.91 |
| 05:01:18 | `go-release-cold-1` | 24.25 | 708 | 5.32 5.35 6.83 |
| 05:01:30 | `go-release-linux-warm-1` | 10.57 | 702 | 4.69 5.22 6.73 |
| 05:03:28 | `rust-build-cold-2` | 64.07 | 2000 | 4.42 5.03 6.55 |
| 05:03:28 | `rust-build-warm-2` | 0.53 | 147 | 7.07 5.89 6.77 |
| 05:03:32 | `rust-build-edit-2` | 3.99 | 858 | 7.07 5.89 6.77 |
| 05:10:32 | `rust-check-cold-2` | 416.87 | 2688 | 6.67 5.85 6.74 |
| 05:15:04 | `rust-check-warm-2` | 271.67 | 1058 | 5.31 6.98 7.22 |
| 05:19:22 | `rust-test-warm-2` | 257.26 | 1058 | 4.52 5.99 6.76 |
| 05:24:59 | `rust-test-cold-2` | 327.77 | 2687 | 4.57 5.73 6.51 |
| 05:27:29 | `rust-release-cold-2` | 145.02 | 2204 | 5.39 10.02 8.99 |
| 05:29:58 | `rust-ui-bundle-2` | 144.56 | 2203 | 31.60 16.98 11.81 |
| 05:32:25 | `rust-ui-test-cold-2` | 147.13 | 2688 | 30.46 21.53 14.39 |
| 05:33:57 | `rust-ui-test-warm-2` | 91.59 | 1059 | 7.94 15.81 13.22 |
| 05:34:17 | `go-build-cold-2` | 16.31 | 789 | 10.93 14.08 12.78 |
| 05:34:18 | `go-build-warm-2` | 0.26 | 48 | 10.82 13.89 12.73 |
| 05:34:19 | `go-build-edit-2` | 1.40 | 496 | 10.82 13.89 12.73 |
| 05:38:29 | `go-test-cold-2` | 247.67 | 751 | 10.03 13.68 12.66 |
| 05:38:34 | `go-test-warm-2` | 5.65 | 92 | 2.79 8.20 10.56 |
| 05:42:19 | `go-test-warm-count1-2` | 225.06 | 409 | 3.17 8.11 10.50 |
| 05:42:27 | `go-pnpm-install-cold-2` | 5.81 | 310 | 3.60 5.60 8.83 |
| 05:42:29 | `go-vite-build-2` | 1.82 | 361 | 7.63 6.40 9.09 |
| 05:42:31 | `go-pnpm-build-2` | 2.12 | 433 | 7.63 6.40 9.09 |
| 05:47:50 | `go-check-cold-2` | 315.90 | 1543 | 7.26 6.34 9.06 |
| 05:48:08 | `go-check-warm-2` | 17.77 | 487 | 8.42 6.54 8.35 |
| 05:48:32 | `go-e2e-warm-2` | 21.48 | 427 | 6.72 6.28 8.20 |
| 05:49:00 | `go-e2e-deps-2` | 5.28 | 334 | 5.95 6.11 8.05 |
| 05:49:38 | `go-e2e-cold-2` | 38.39 | 762 | 6.04 6.13 8.04 |
| 05:50:08 | `go-release-cold-2` | 23.81 | 752 | 7.77 6.65 8.14 |
| 05:50:19 | `go-release-linux-warm-2` | 10.46 | 791 | 13.67 8.07 8.61 |
| 05:51:46 | `rust-build-prep-3` | 64.26 | 2009 | 11.29 8.28 8.67 |
| 05:51:47 | `rust-build-warm-3` | 0.52 | 147 | 12.65 9.39 9.06 |
| 05:51:47 | `rust-build-warm-4` | 0.48 | 146 | 12.65 9.39 9.06 |
