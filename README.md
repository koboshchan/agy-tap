agy-tap wraps the Antigravity CLI in a terminal and lets another local process send messages to that same live session. It does not start a second agent or attach ACP to a different process.

Install with Go 1.27.2 or newer.

```sh
go install github.com/koboshchan/agy-tap@latest
HTTP_PROXY=http://127.0.0.1:8080 agy-tap -- agy --conversation=YOUR_CONVERSATION_ID
```

On PowerShell, set your usual environment variables first, then run the same wrapper command. Windows uses ConPTY; Linux and WSL use a Unix PTY. Environment variables pass through unchanged. Existing shells may need restarting after adding the binary directory to PATH.

In another terminal, find the wrapper PID and use its local client.

```sh
agy-tap list
agy-tap status 12345
agy-tap send 12345 "Check the last test result"
agy-tap force 12345
agy-tap messages 12345
agy-tap messages 12345 12
agy-tap screen 12345
```

The optional number after messages is an exclusive step index. Pass a full existing conversation ID explicitly if you want transcript access. A nonexistent ID can make agy start a different conversation; agy-tap does not discover that replacement automatically.

Every `send PID text` immediately types and submits an ordinary message through the shared terminal, even while agy is busy. There is no agy-tap message queue, idle detection, prompt matching, or draft guard. Agy's own Queued Messages setting decides whether it queues or acts on the message; a successful receipt is not a guarantee of immediate model execution. Multiline text requires negotiated bracketed paste; otherwise the call fails with HTTP 409 immediately instead of waiting.

`force PID` is a separate trigger-only command. It writes exactly the native Alt+M shortcut (ESC followed by lowercase m) once, with no text or Enter. It does not implement a queue, clear busy state, send Ctrl-C, or signal the process. Use it to send the queued message immediately through agy's native binding. The observed busy UI shows a queued row with ‘alt+m to send immediately’, and the shortcut works with an empty editor in that case. It is not a promise to flush every queued message. Agy controls the shortcut's behavior in other states; this command does not add a payload-bearing variant of send.

Local keyboard input and control commands share the same editor. Existing half-typed local text MAY COMBINE with a send and be submitted. Neither command detects, protects, erases, logs, or restores that local draft. Complete control writes and individual local-input read chunks are serialized, so a send's paste/text/Enter or a force shortcut cannot have other writer bytes inserted into it. This does not make a multi-read keyboard paste or fragmented terminal sequence one indivisible edit. Requests are applied in their serialized acceptance order; simultaneous callers have no guaranteed network-arrival order. A failed or short write returns an error without retry, because some input might already have reached agy.

Send has no flags. For text starting with dashes use `send PID -- "--literal-text"`; after text starts all remaining arguments are literal. The obsolete experimental `send --force` form is not supported. Permission prompts remain the user's responsibility. Do not treat this wrapper as a guaranteed collision-free automation API. Existing running wrappers keep their old behavior until restarted; installing a new client does not upgrade those processes.

Each session listens only on a random 127.0.0.1 port. Its private ~/.agy-tap/PID.json contains a bearer token, port and conversation ID. Unix permissions and Windows ACLs restrict the state directory to its owner. Keep it private. The send endpoint can cause the agent to act with your account's permissions; do not expose the port to the network. Origin-bearing requests are refused, and the bundled client ignores HTTP proxy variables for loopback requests. Injected text is logged locally and may contain secrets.

The authenticated HTTP interface provides POST /send with a JSON text property, bodyless POST /force, GET /status, GET /screen and GET /messages?since=12. Unknown send properties and any force body are rejected. Authentication and discovery permissions are unchanged. Status reports PID, conversation and bracketed-paste negotiation; obsolete queue/readiness/input-state fields are removed. The CLI messages command reads the database directly and prints only plain user/assistant text, so it also works with an older running wrapper. Accepted control commands return an injected receipt only after writing to the terminal, not proof that agy answered. Transcript reads open agy's SQLite database read-only with WAL support, decode user and assistant text from the observed v1.5.9 protobuf layout, and preserve unknown payloads as base64. The terminal snapshot is a fallback when transcript access is unavailable. It includes width and height so callers can check the child viewport.

The implementation uses Charm's xpty and vt packages, modernc SQLite and protobuf wire decoding. Terminal input/output passes through the PTY; resize events follow the caller's terminal. On Windows, dimensions come from the output console handle, not the input handle, which cannot report screen-buffer dimensions. Native console output enables VT processing and deferred end-of-line wrapping, with the original modes restored on exit.

```sh
go test -race ./...
go vet ./...
go build .
GOOS=windows GOARCH=amd64 go build -o agy-tap.exe .
```

Unix children now own the inner PTY as their controlling terminal and foreground process group. The kernel delivers SIGWINCH only after the inner size changes, rather than the child reading stale dimensions from the outer terminal's earlier signal. The existing 250 ms size polling remains unchanged. Windows ConPTY setup is unchanged.

Run `python3 test_pty.py` for offline real-terminal coverage. It uses a fake TUI, checks initial dimensions and three repeated resizes at signal delivery, tests direct and nested wrappers, verifies foreground ownership and typed input, and checks normal/Ctrl-C exit, terminal restoration and child cleanup. CI runs these terminal tests on Linux and macOS, plus native Go tests on Windows. Windows ARM64 archives are cross-built, not native-console tested. Tagged releases include five platform archives and SHA-256 checksums; publishing a release does not reinstall any existing machine.

Run `python3 test_send_pty.py` for offline fake-TUI input tests. They check immediate ordinary sends while the original turn stays busy, exactly one contiguous Alt+M per force command (including the intended busy + queued message + empty editor use), no interruption, concurrent send/force/local-write serialization, fragmented local Unicode/paste/cursor input, accepted shared-draft behavior, no delayed tap dispatch, and terminal/child cleanup. These fake-PTY tests prove emitted bytes and wrapper behavior, not native agy UI semantics. No actual agy or model calls are used.

Tests cover authentication, cross-origin rejection, control-character rejection, argument parsing, short writes, bracketed paste, output-handle dimension selection and SQLite WAL transcript reads. Windows regression testing used a real outer ConPTY at 161x58, both directly and through Git Bash, with an actual agy TUI and Gemini responses. Typed and injected turns each rendered their user block and assistant answer once, with one footer. Resizing the outer terminal to 103x35 updated the child and kept the footer intact. The same harness against 0.1.1 confirmed that its child stayed at 120x40 and its footer overflowed after resize. VT streams and screen snapshots were captured locally. These were disposable sessions, not the user's live agent. Separate mintty pipe-based terminals and arbitrary TUI layouts remain unverified.

MIT licensed. See LICENSE.
