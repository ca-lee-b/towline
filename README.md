# Towline

Towline runs Codex inside a sandbox and connects it to your controller over an outbound WebSocket. The controller sends commands and receives Codex events. If the connection drops, Codex keeps running while Towline reconnects.

## Motivation

As cloud agents/sandboxes are created, users need a way to follow its progress through network interruptions. Towline provides that connection, keeping the Codex session alive and making missed events available for replay. It allows an easy way to stream Codex events.

## Usage

Build the binary from the repository root with Go 1.23 or later.

```sh
go build -o ./bin/towline ./cmd/towline
```

In the sandbox, install a Codex CLI that supports `app-server` and configure its credentials. Start Towline with your controller’s WebSocket URL and the directory where Codex should work.

```sh
./bin/towline -endpoint wss://controller.example.com/sandboxes/123 -dir /workspace
```

You can also set the URL with `TOWLINE_ENDPOINT`. Set `TOWLINE_TOKEN` if the controller requires a bearer token. Towline looks for `codex` on `PATH` unless you supply `-codex /path/to/codex`.

Towline writes readable connection and error logs to stderr. Add `-verbose` to see command reply metadata and event sequence numbers.

## Controller protocol

Send one JSON object per WebSocket text frame. The first `send` opens a Codex thread. Later turns reuse it.

```json
{"id":"1","op":"send","text":"Run the tests and summarize any failures."}
```

A successful reply contains the same ID and a result. Errors carry an `error` string. Towline returns `busy` during an active turn, so queue additional sends in the controller. Use a fresh ID for each new `send`, `steer`, `interrupt`, or `respond`. If the reply is lost, resend the same command with the same ID, including after reconnect. Towline returns the original result or error. A changed payload under the same ID returns an error. Use a new ID to try again after `busy`. Towline keeps these outcomes in memory for the lifetime of its process. After a restart, the outcome of an earlier command may be unknown.

| Operation | Purpose |
| --- | --- |
| `send` | Start a turn with `text`. Returns `threadId` and `turnId`. |
| `steer` / `interrupt` | Add `text` to an active turn or interrupt it using its `turnId`. |
| `respond` | Answer a Codex request with its `requestId` and `result`. |
| `replay` | Resume events at sequence `from` (inclusive), then follow live events. |
| `prepare_shutdown` | Return `busy` during work or `ready_to_stop` when the sandbox can stop. |

Event frames contain `seq` and `event`. Sequence numbers start at 1 for each stream. Reconnected controllers receive live events and can replay missed events from the last processed sequence plus one.

### Replay after restart

Towline uses the same disk journal for live events and replay. It stores journals under `towline/events` in the system’s per-user configuration directory, with one journal per endpoint URL and resolved working directory. Keep that directory on persistent storage. After restart, use the same endpoint and working directory, then send `replay` from the last processed sequence plus one. New events continue the existing sequence.

Towline syncs each event before streaming it. On startup it discards an unfinished final record and fails if a complete record is invalid. Only one Towline process can use a journal at a time. The file grows with each event and can contain sensitive Codex output. A restart starts a fresh Codex session and clears cached command outcomes.

## Live reconnect smoke test

With Codex authenticated, run `TOWLINE_LIVE_TEST=1 go test -race ./internal -run '^TestLiveReconnect$' -count=1`. The test starts a local WebSocket controller, sends one short turn in a temporary directory, disconnects after the turn starts, and checks replay sequence continuity through successful completion of that same turn.
