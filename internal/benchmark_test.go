//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || windows

package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
	j, event := benchmarkJournal(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := j.append(event); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	b.ReportMetric(float64(b.N)/b.Elapsed().Seconds(), "events/s")
	b.ReportMetric(float64(cap(j.offsets))*8/float64(b.N), "index-B/event")
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
