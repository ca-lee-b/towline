# Towline

Towline runs Codex inside a sandbox and connects it to your controller over an outbound WebSocket. The controller sends commands and receives Codex events. If the connection drops, Codex keeps running while Towline reconnects.

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

## Controller protocol

Send one JSON object per WebSocket text frame. The first `send` opens a Codex thread. Later turns reuse it.

```json
{"id":"1","op":"send","text":"Run the tests and summarize any failures."}
```

A successful reply contains the same ID and a result. Errors carry an `error` string. Towline returns `busy` during an active turn, so queue additional sends in the controller. Retrying a command can execute it twice.

| Operation | Purpose |
| --- | --- |
| `send` | Start a turn with `text`. Returns `threadId` and `turnId`. |
| `steer` / `interrupt` | Add `text` to an active turn or interrupt it using its `turnId`. |
| `respond` | Answer a Codex request with its `requestId` and `result`. |
| `replay` | Resume events at sequence `from` (inclusive), then follow live events. |
| `prepare_shutdown` | Return `busy` during work or `ready_to_stop` when the sandbox can stop. |

Event frames contain `seq` and `event`. Sequence numbers start at 1 for each Towline process. Reconnected controllers receive live events and can replay missed events from the last processed sequence plus one.
