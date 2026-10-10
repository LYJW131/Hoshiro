# Hoshiro on MyGo (native Go)

This branch rewrites Hoshiro in Go on [MyGo](https://github.com/egoist/mygo)
with its native `ui` package: no webview, no Rust, no frontend build.

```sh
go test ./...                    # unit + headless UI tests (any OS)
go run ./cmd/hoshiro             # run (macOS; Linux shows the UI with stubbed platform features)
go run ./cmd/hoshiro -demo       # cycle the floating bar through its states
go tool mygo build               # package Hoshiro.app + dmg (configuration: mygo.json)
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./cmd/hoshiro   # cross-compile, no cgo needed
HOSHIRO_SHOTS=/tmp/shots go test ./internal/ui/...                # write UI screenshots
```

| Package | Replaces |
|---|---|
| `cmd/hoshiro` | `lib.rs` setup, `tray.rs` (tray + app menu), `update.rs` wiring (MyGo updater), single instance, autostart |
| `internal/core` | `controller.rs`, `pipeline.rs`, `commands.rs`, `state.rs`, `notify.rs`, `update.rs` |
| `internal/audio` | `audio/` (capture, resampling to 16 kHz, WAV, levels, sounds) |
| `internal/providers` | `providers/`: ASR (MiMo, Cloudflare, Azure, Google; streaming) and the LLM layer, which builds OpenAI Chat requests and converts them to Claude / Gemini / OpenAI with [RelayKit](https://github.com/QuantumNous/new-api/tree/main/relaykit) (AGPL-3.0) |
| `internal/store` | `db.rs` (SQLite via modernc.org/sqlite, pure Go) |
| `internal/config`, `internal/secrets` | `config.rs`, Keychain (`secrets` uses Security.framework through purego) |
| `internal/hotkey`, `internal/macos`, `internal/platform` | event tap / hotkeys, text insertion, clipboard, permissions, bar window setup (purego, no cgo) |
| `internal/personalize`, `internal/autodict`, `internal/stats`, `internal/importer` | personalization, auto dictionary, usage, Typeless import |
| `internal/ui/hub` | `src/hub` (main window: home, history, dictionary, statistics, settings, onboarding, about) |
| `internal/ui/bar` | `src/bar` (pill, waveform, alerts, answer card, translation chip) |
| `internal/ui/kit`, `internal/ui/icons` | design tokens (`tokens.css`), shared components, Lucide icons |

The original `src/` and `src-tauri/` are kept for reference and can be
removed once the port is accepted.

Updates: MyGo's updater needs `updates` (public key + feed) in `mygo.json`;
until a signing key is set up, update checks report "无法检查更新".
