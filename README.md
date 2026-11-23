# Towline

Towline runs Codex or Claude Code inside a sandbox and connects the agent to your controller over an outbound WebSocket. If the connection drops, the agent keeps running while Towline reconnects.

## Motivation

As cloud agents/sandboxes are created, users need a way to follow its progress through network interruptions. Towline provides that connection, keeping the agent session alive and making missed events available for replay.

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

To run Claude Code, install and authenticate the Claude CLI in the sandbox, then select it at startup. Supply `-claude /path/to/claude` to override the executable.

```sh
./bin/towline -agent claude -endpoint wss://controller.example.com/sandboxes/123 -dir /workspace
```

Towline writes readable connection and error logs to stderr. Add `-verbose` to see command reply metadata and event sequence numbers.

## Controller protocol

Send one JSON object per WebSocket text frame. Each process runs one agent. Later turns share the same conversation.

```json
{"id":"1","op":"send","text":"Run the tests and summarize any failures."}
```

A successful reply contains the same ID and a result. Errors carry an `error` string. Towline returns `busy` during an active turn, so queue additional sends in the controller. Use a fresh ID for each new `send`, `steer`, `interrupt`, or `respond`. If the reply is lost, resend the same command with the same ID, including after reconnect. Towline returns the original result or error. A changed payload under the same ID returns an error. Use a new ID to try again after `busy`. Towline keeps these outcomes in memory for the lifetime of its process. After a restart, the outcome of an earlier command may be unknown.

| Operation | Purpose |
| --- | --- |
| `capabilities` | List supported agent operations in `result.operations`. |
| `send` | Start a turn with `text`. Returns `threadId` and `turnId`. |
| `steer` / `interrupt` | Add `text` to an active turn or interrupt it using its `turnId`. |
| `respond` | Answer an agent request with its `requestId` and `result`. |
| `replay` | Resume events at sequence `from` (inclusive), then follow live events. |
| `prepare_shutdown` | Return `busy` during work or `ready_to_stop` when the sandbox can stop. |

Event frames contain `seq` and `event`. Sequence numbers start at 1 for each stream. Reconnected controllers receive live events and can replay missed events from the last processed sequence plus one.

Claude supports `send`, `interrupt`, and `respond`. For `respond`, copy the string `request_id` from Claude’s `can_use_tool` request into `requestId`. An approval uses `{"behavior":"allow","updatedInput":{...}}` with the approved tool arguments. A denial uses `{"behavior":"deny","message":"reason"}`. The agent executes its own tools.

Codex streams its existing app-server events. Claude streams objects with `agent: "claude"`, `threadId`, `turnId`, and the native stream-JSON frame in `message`. Its `result` message ends a turn, including failures.

### Replay after restart

Run one Towline per sandbox. Towline uses the same disk journal for live events and replay, at `towline/events.jsonl` under the sandbox user’s configuration directory. Keep that directory on persistent, sandbox-local storage. After restart, send `replay` from the last processed sequence plus one. New events continue the existing sequence even if the endpoint or working directory changes.

Towline syncs each event before streaming it. On startup it discards an unfinished final record and fails if a complete record is invalid. Only one Towline process can use a journal at a time. The file grows with each event and can contain sensitive agent output. A restart starts a fresh agent session and clears cached command outcomes.

## Benchmarks and recovery tests

See [BENCHMARK.md](BENCHMARK.md) for latency, a raw baseline, and instructions for reproducing the measurements.

Run `go test -race ./...` for the offline suite, including disconnect/replay and recovery after a writer exits abruptly with a partial record on disk.
