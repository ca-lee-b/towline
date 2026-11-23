# Towline benchmarks

On my Apple M1 running macOS 26.6.2 and Go 1.27.4 (darwin/arm64), appending a 1 KiB JSON event to the journal took about 4 ms at p50. Writing a pre-encoded record of the same size and syncing it had (almost) the same median on the same local APFS SSD.

## Durable append

Each column shows the range across three runs of 1,024 consecutive appends, with one writer and a fresh file per run. The percentile columns summarize individual writes within each run. Max is the slowest observed write in each run.

| Operation | Mean | p50 | p99 | Max | Heap bytes/op | Allocs/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Journal append | 3.83–3.85 ms | 3.96–3.99 ms | 5.98–7.23 ms | 20.7–34.1 ms | 1,353–1,355 | 4 |
| Raw WriteAt + Sync | 3.82–3.85 ms | 3.97–3.98 ms | 6.40–7.09 ms | 20.9–21.4 ms | 0 | 0 |

Journal append includes locking, encoding the numbered JSONL record, writing and syncing it, updating the offset index, and signaling readers. The raw benchmark writes at consecutive offsets and syncs after every record. It reuses a pre-encoded line for each sequence-number width so the byte count matches at every offset. Both benchmarks time every operation.

## WebSocket replay

Each request starts at sequence 1 on an established loopback WebSocket with a warm journal and an idle writer. Timing includes the controller's timeout setup and cancellation, sending the request, reading its acknowledgement, and decoding and checking every sequence through the last event. Allocations include the server and controller.

| Events per request | Mean time per request, range across runs | Allocs/request |
| ---: | ---: | ---: |
| 1 | 39.6–52.5 µs | 84 |
| 64 | 332–341 µs | 1,662 |

## Reproduce and compare

Run from the repository root. On Unix, set `TMPDIR` to a directory on the deployment filesystem to measure its sync behavior.

```sh
go test -run '^$' -bench . -benchmem -benchtime=1024x -count=3 ./internal
```

For a revision comparison, collect more repetitions. Run the first command on the baseline revision and the second on the candidate, then compare the matching benchmark names with benchstat.

```sh
go test -run '^$' -bench . -benchmem -benchtime=1024x -count=10 ./internal > before.txt
go test -run '^$' -bench . -benchmem -benchtime=1024x -count=10 ./internal > after.txt
benchstat before.txt after.txt
```
