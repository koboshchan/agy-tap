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
agy-tap messages 12345
agy-tap messages 12345 12
agy-tap screen 12345
```

The optional number after messages is an exclusive step index. Pass a full existing conversation ID explicitly if you want transcript access. A nonexistent ID can make agy start a different conversation; agy-tap does not discover that replacement automatically.

Messages queue until the terminal appears idle and the local input is empty. Detection is conservative and depends on agy's current prompt layout, not an official busy-state API. Terminal response sequences are parsed separately from editing. A settled redraw of the current empty prompt clears stale input state, including after menus. Unknown input remains blocked until a confirming redraw or Ctrl-U. Multiline messages wait for negotiated bracketed-paste support. Permission prompts remain the user's responsibility. Do not treat this wrapper as a guaranteed collision-free automation API.

Each session listens only on a random 127.0.0.1 port. Its private ~/.agy-tap/PID.json contains a bearer token, port and conversation ID. Unix permissions and Windows ACLs restrict the state directory to its owner. Keep it private. The send endpoint can cause the agent to act with your account's permissions; do not expose the port to the network. Origin-bearing requests are refused, and the bundled client ignores HTTP proxy variables for loopback requests. Injected text is logged locally and may contain secrets.

The authenticated HTTP interface provides POST /send with a JSON text property, GET /status, GET /screen and GET /messages?since=12. The CLI messages command reads the database directly and prints only plain user/assistant text, so it also works with an older running wrapper. Accepted sends return a queue receipt, not proof that agy answered. Transcript reads open agy's SQLite database read-only with WAL support, decode user and assistant text from the observed v1.5.9 protobuf layout, and preserve unknown payloads as base64. The terminal snapshot is a fallback when transcript access is unavailable.

The implementation uses Charm's xpty and vt packages, modernc SQLite and protobuf wire decoding. Terminal input/output passes through the PTY; resize events follow the caller's terminal.

```sh
go test -race ./...
go vet ./...
go build .
GOOS=windows GOARCH=amd64 go build -o agy-tap.exe .
```

Tests cover authentication, cross-origin rejection, control-character rejection, queue guards, bracketed paste and SQLite WAL transcript reads. Desktop testing verified Windows ConPTY and WSL PTY startup, local HTTP injection appearing in agy's TUI and the same conversation database, plus transcript retrieval. The test agent rejected generation because its plan model was unspecified, so a successful model response has not been verified. A regression run on both Windows and WSL verified that terminal cursor-response bytes no longer poison idle state and queued text is injected. A Windows model request reached generation but returned model-unavailable retries. PowerShell and Git Bash binary invocation were checked; manual interactive resize and editing across both shells have not been fully exercised.

MIT licensed. See LICENSE.
