//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || windows

package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func benchmarkJournal(b *testing.B) (*journal, json.RawMessage) {
	b.Helper()
	j, err := openJournal(filepath.Join(b.TempDir(), "events.jsonl"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { j.file.Close() })
	return j, json.RawMessage(`{"delta":"` + strings.Repeat("x", 1012) + `"}`)
}

func BenchmarkJournalAppend(b *testing.B) {
	for _, mode := range []string{"journal", "raw_sync"} {
		b.Run(mode, func(b *testing.B) {
			j, event := benchmarkJournal(b)
			latencies := make([]time.Duration, b.N)
			appendOne := func(_ int) error { return j.append(event) }
			if mode == "raw_sync" {
				// One record per sequence width keeps writes the same size as the journal.
				var records [][]byte
				for seq := 1; ; seq *= 10 {
					data, err := json.Marshal(eventRecord{seq, event})
					if err != nil {
						b.Fatal(err)
					}
					records = append(records, append(data, '\n'))
					if seq > b.N/10 {
						break
					}
				}
				var offset int64
				var width int
				nextWidth := 10
				appendOne = func(seq int) error {
					if seq == nextWidth {
						width++
						nextWidth *= 10
					}
					data := records[width]
					if _, err := j.file.WriteAt(data, offset); err != nil {
						return err
					}
					offset += int64(len(data))
					return j.file.Sync()
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := range latencies {
				start := time.Now()
				err := appendOne(i + 1)
				latencies[i] = time.Since(start)
				if err != nil {
					b.Fatal(err)
				}
			}
			b.StopTimer()
			slices.Sort(latencies)
			b.ReportMetric(float64(latencies[(b.N-1)/2]), "p50-ns/op")
			b.ReportMetric(float64(latencies[b.N-1-b.N/100]), "p99-ns/op")
			b.ReportMetric(float64(latencies[b.N-1]), "max-ns/op")
		})
	}
}

func BenchmarkReplay(b *testing.B) {
	for _, count := range []int{1, 64} {
		b.Run(fmt.Sprintf("%d_events", count), func(b *testing.B) {
			j, event := benchmarkJournal(b)
			for i := 0; i < count; i++ {
				if err := j.append(event); err != nil {
					b.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err == nil {
					_ = serve(ctx, conn, j, nil)
				}
			}))
			defer srv.Close()
			conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
			if err != nil {
				b.Fatal(err)
			}
			defer conn.CloseNow()
			replay := command{ID: "bench", Op: "replay", From: 1}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				deadline, stop := context.WithTimeout(ctx, 15*time.Second)
				if err := wsjson.Write(deadline, conn, replay); err != nil {
					stop()
					b.Fatal(err)
				}
				var reply commandReply
				if err := wsjson.Read(deadline, conn, &reply); err != nil || reply.ID != replay.ID || reply.Error != nil {
					stop()
					b.Fatalf("replay reply %+v: %v", reply, err)
				}
				for seq := 1; seq <= count; seq++ {
					var frame struct{ Seq int }
					if err := wsjson.Read(deadline, conn, &frame); err != nil || frame.Seq != seq {
						stop()
						b.Fatalf("want sequence %d, got %d: %v", seq, frame.Seq, err)
					}
				}
				stop()
			}
			b.StopTimer()
		})
	}
}
