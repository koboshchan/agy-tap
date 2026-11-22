<div align="center">

<strong>agy-tap</strong>

Send input to the Antigravity session already running in your terminal.

[![CI](https://github.com/koboshchan/agy-tap/actions/workflows/ci.yml/badge.svg)](https://github.com/koboshchan/agy-tap/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/koboshchan/agy-tap)](https://github.com/koboshchan/agy-tap/releases)
[![MIT](https://img.shields.io/badge/license-MIT-blue)](LICENSE)

[Install](#install) · [Quick start](#quick-start) · [Commands](#commands) · [Development](#development)

</div>

agy-tap wraps `agy` in a real terminal and gives other local processes a way to send messages, trigger the native send-immediately shortcut, read transcripts, and inspect the screen. You keep using the same live agent, not a second session.

Linux, macOS, and WSL use a Unix PTY. Native Windows uses ConPTY. Keyboard input, terminal output, and resize events pass through the wrapper.

<a id="install"></a>
**Install**

Download an archive from [Releases](https://github.com/koboshchan/agy-tap/releases/latest), verify its SHA-256 against `checksums.txt`, and put the extracted executable on PATH. Go is not required for prebuilt binaries.

| Platform | Archive |
| --- | --- |
| Linux x64 / ARM64 | `agy-tap-linux-amd64.tar.gz` / `agy-tap-linux-arm64.tar.gz` |
| macOS Apple Silicon | `agy-tap-darwin-arm64.tar.gz` |
| Windows x64 / ARM64 | `agy-tap-windows-amd64.zip` / `agy-tap-windows-arm64.zip` |

Or install from source with Go 1.27.2 or newer.

```sh
go install github.com/koboshchan/agy-tap@latest
agy-tap --version
```

Install `agy` separately and make sure both executables are on PATH. Existing running wrappers retain their old behavior until restarted; replacing the client binary does not upgrade them.

<a id="quick-start"></a>
**Quick start**

Start a wrapped session in your usual terminal. Your existing environment passes through unchanged.

```sh
agy-tap -- agy --conversation=EXISTING_ID
```

In another terminal, find the wrapper PID and send a message.

```sh
agy-tap list
agy-tap status 12345
agy-tap send 12345 "Check the last test result"
agy-tap messages 12345
```

Use a full, existing conversation ID for transcript access. If the ID does not exist, agy may create a different conversation; agy-tap does not discover that replacement automatically.

<a id="commands"></a>
**Commands**

| Command | Behavior |
| --- | --- |
| `agy-tap -- COMMAND [ARGS...]` | Run a command inside the wrapper |
| `agy-tap list` | Discover local wrapper sessions |
| `agy-tap status PID` | Show session details and bracketed-paste negotiation |
| `agy-tap send PID TEXT` | Immediately write text/paste and Enter |
| `agy-tap force PID` | Write exactly ESC followed by lowercase `m`, the native Alt+M shortcut |
| `agy-tap messages PID [SINCE]` | Read transcript text after an optional exclusive step index |
| `agy-tap screen PID` | Read the terminal snapshot and viewport dimensions |
| `agy-tap --version` | Print the installed client version |

```sh
agy-tap send 12345 -- "--literal-text"
agy-tap messages 12345 12
agy-tap force 12345
agy-tap screen 12345
```

**Send and force are different**

`send` writes immediately even while agy is busy. agy-tap has no message queue, idle detection, prompt matching, or draft guard. Agy's own Queued Messages setting decides what happens after submission. A successful receipt confirms terminal injection, not model execution.

`force` is trigger-only. It sends Alt+M once, with no text, Enter, Ctrl-C, or process signal. In agy's busy UI, this invokes the queued row's native "send immediately" binding. It does not promise to flush every queued message, and behavior in other UI states belongs to agy.

Send has no flags. Use `send PID -- "--literal-text"` for text beginning with dashes. The old experimental `send --force` form is unsupported. Multiline input requires negotiated bracketed paste; otherwise it fails immediately with HTTP 409.

> [!WARNING]
> Local typing and injected messages share one editor. A half-written draft can combine with an injected message and be submitted. There is no draft protection or restoration. Permission prompts remain your responsibility.

Complete control writes are serialized with individual keyboard-input reads. This prevents bytes from being inserted inside one control write, but does not make a fragmented keyboard paste atomic. Failed or short writes are not retried, since some input may already have reached agy.

**Local interface and configuration**

Each wrapper listens on a random `127.0.0.1` port. Its owner-only `~/.agy-tap/PID.json` discovery file contains the port, bearer token, and conversation ID. Keep that file private. The bundled client ignores HTTP proxy variables for loopback requests, and the server rejects Origin-bearing requests.

| Environment variable | Purpose |
| --- | --- |
| `AGY_TAP_DIR` | Override the local session-discovery directory |
| `AGY_TAP_DB` | Override the agy transcript database path |

| Authenticated HTTP route | Purpose |
| --- | --- |
| `POST /send` | JSON object with a `text` property; unknown properties rejected |
| `POST /force` | Bodyless native shortcut trigger |
| `GET /status` | Session status |
| `GET /screen` | Terminal snapshot |
| `GET /messages?since=12` | Transcript after an exclusive step index |

> [!CAUTION]
> The control interface can make the agent act with your account's permissions. Never expose it to the network. Injected text is logged locally and may contain sensitive material.

The CLI's `messages` command reads SQLite directly in read-only mode with WAL support, including when the running wrapper is older. Transcript decoding targets the observed agy 1.5.9 protobuf layout; plain user/assistant text is printed, and the decoder preserves unknown payloads as base64. Use `screen` when transcript access is unavailable.

**Under the terminal**

[Charm xpty and vt](go.mod) provide terminal hosting and screen tracking; modernc SQLite and protobuf decoding support transcripts. Unix children own the inner PTY as their controlling terminal and foreground process group. Windows uses the output console for viewport dimensions, enables VT output modes, and restores original modes on exit.

<a id="development"></a>
**Development**

```sh
git clone https://github.com/koboshchan/agy-tap.git
cd agy-tap
go test -race ./...
go vet ./...
go build .
python3 test_pty.py
python3 test_send_pty.py
```

The Python suites use offline fake TUIs, not model calls. They cover terminal ownership, resize delivery, cleanup, immediate sends, shortcut bytes, concurrent input, and shared-draft behavior. They establish wrapper behavior, not every native agy UI state.

[CI](.github/workflows/ci.yml) runs terminal tests on Linux and macOS and native Go tests on Windows. Windows ARM64 archives are cross-built, not native-console tested. Tagged releases publish five archives and checksums without reinstalling any machine.

[MIT licensed](LICENSE).
