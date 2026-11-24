# Towline

Towline runs Codex or Claude Code in a sandbox. It connects the agent to your controller over an outbound WebSocket. If the connection drops, the agent keeps running while Towline reconnects.

## Motivation

As cloud agents/sandboxes are created, users need a way to follow its progress through network interruptions. Towline provides that connection, keeping the agent session alive and making missed events available for replay.

## Usage

Build Towline from the repository root. You need Go 1.23 or later.

```sh
go build -o ./bin/towline ./cmd/towline
```

Codex is the default adapter. Select an adapter with `-agent`. Set `TOWLINE_PATH` to the directories Towline should search for the selected executable (for example, `TOWLINE_PATH=/opt/agents/bin:/usr/local/bin` on Unix). If unset or empty, Towline uses the system `PATH`. An explicit executable path bypasses the search. Set the controller URL with `-endpoint` or `TOWLINE_ENDPOINT`. If the controller requires a bearer token, set `TOWLINE_TOKEN`.

### Codex

In the sandbox, install and authenticate the Codex CLI. It must support `app-server`. Start Towline with your controller's WebSocket URL and the directory where Codex should work.

```sh
./bin/towline -endpoint wss://controller.example.com/sandboxes/123 -dir /workspace
```

Use `-codex /path/to/codex` to select a different executable.

### Claude Code

In the sandbox, install and authenticate the Claude CLI. Start Towline with your controller's WebSocket URL and the directory where Claude Code should work. Use `-claude /path/to/claude` to select a different executable.

```sh
./bin/towline -agent claude -endpoint wss://controller.example.com/sandboxes/123 -dir /workspace
```

Towline writes connection and error logs to stderr. Add `-verbose` to log command reply metadata and event sequence numbers.

## Controller protocol

Send one JSON object in each WebSocket text frame. Each Towline process runs one agent. Later turns use the same conversation.

```json
{"id":"1","op":"send","text":"Run the tests and summarize any failures."}
```

A successful reply contains the command ID and a `result`. An error reply contains an `error` string. Towline returns `busy` while the agent works. Queue additional sends in the controller.

| Operation | Purpose |
| --- | --- |
| `capabilities` | List supported agent operations in `result.operations`. |
| `send` | Start a turn with `text`. Returns `threadId` and `turnId`. |
| `steer` / `interrupt` | Add `text` to an active turn or interrupt it with its `turnId`. |
| `respond` | Answer an agent request with its `requestId` and `result`. |
| `replay` | Resume events at sequence `from` (inclusive), then receive live events. |
| `prepare_shutdown` | Return `busy` during work or `ready_to_stop` when the sandbox can stop. |

Event frames contain `seq` and `event`. Sequence numbers start at 1 for each stream. After reconnect, the controller can replay missed events from the last processed sequence plus one.

Claude supports `send`, `interrupt`, and `respond`. For `respond`, copy the string `request_id` from Claude's `can_use_tool` request into `requestId`. To approve a request, send `{"behavior":"allow","updatedInput":{...}}` with the approved tool arguments. To deny it, send `{"behavior":"deny","message":"reason"}`. Claude runs its own tools.

Codex streams its app-server events. Claude streams objects with `agent: "claude"`, `threadId`, `turnId`, and the native stream-JSON frame in `message`. Claude's `result` message ends a turn, including when the turn fails.

### Replay after restart

Run one Towline process per sandbox. Towline stores live events and replay data in `towline/events.jsonl` under the sandbox user's configuration directory. After a restart, send `replay` from the last processed sequence plus one. New events continue the existing sequence, even if the endpoint or working directory changes.

Towline syncs each event to disk before it streams the event. On startup, it discards an unfinished final record. It stops if a complete record is invalid. Only one Towline process can use a journal at a time. The file grows with each event and can contain sensitive agent output. A restart starts a new agent session and clears cached command outcomes.

## Benchmarks and recovery tests

See [BENCHMARK.md](BENCHMARK.md) for latency data, a raw baseline, and steps to reproduce the measurements.

Run `go test -race ./...` to run the offline suite. It includes disconnect and replay checks, plus recovery after a writer exits with a partial record on disk.
