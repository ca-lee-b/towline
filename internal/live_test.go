package towline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestLiveReconnect(t *testing.T) {
	if os.Getenv("TOWLINE_LIVE_TEST") != "1" {
		t.Skip("set TOWLINE_LIVE_TEST=1 to run an authenticated Codex turn")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	connections := make(chan *websocket.Conn)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		select {
		case connections <- conn:
		case <-ctx.Done():
			conn.CloseNow()
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	done := make(chan struct{})
	var runErr error
	go func() {
		defer close(done)
		runErr = Run(ctx, Config{URL: "ws" + strings.TrimPrefix(server.URL, "http"), Codex: "codex", Dir: dir})
	}()
	defer func() { cancel(); <-done }()
	connect := func() *websocket.Conn {
		t.Helper()
		select {
		case conn := <-connections:
			conn.SetReadLimit(4 << 20)
			return conn
		case <-done:
			t.Fatalf("Towline exited: %v", runErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		return nil
	}
	type frame struct {
		ID, Error string
		Result    struct{ ThreadID, TurnID string }
		Seq       int
		Event     struct {
			Method string
			Params struct {
				ThreadID string
				Turn     struct{ ID, Status string }
			}
		}
	}
	read := func(conn *websocket.Conn) frame {
		t.Helper()
		var f frame
		if err := wsjson.Read(ctx, conn, &f); err != nil {
			t.Fatal(err)
		}
		if f.Error != "" {
			t.Fatalf("command %s: %s", f.ID, f.Error)
		}
		return f
	}
	first := connect()
	defer first.CloseNow()
	if err := wsjson.Write(ctx, first, command{ID: "send", Op: "send", Text: "Reply with just OK. Do not use tools."}); err != nil {
		t.Fatal(err)
	}
	var thread, turn string
	last := 0
	for {
		f := read(first)
		if f.ID == "send" {
			thread, turn = f.Result.ThreadID, f.Result.TurnID
			continue
		}
		last = f.Seq
		if f.Event.Method == "turn/started" && turn != "" && f.Event.Params.Turn.ID == turn && f.Event.Params.ThreadID == thread {
			break
		}
	}
	// Lose one delivered frame before the controller records its sequence.
	// Its presence in the journal guarantees that reconnect must rewind.
	missed := read(first)
	if missed.Seq != last+1 {
		t.Fatalf("expected unprocessed sequence %d, got %d", last+1, missed.Seq)
	}
	first.CloseNow()
	second := connect()
	defer second.CloseNow()
	if err := wsjson.Write(ctx, second, command{ID: "resume", Op: "replay", From: last + 1}); err != nil {
		t.Fatal(err)
	}
	// Live events can arrive before Towline acknowledges the replay cursor.
	for read(second).ID != "resume" {
	}
	for {
		f := read(second)
		last++
		if f.Seq != last {
			t.Fatalf("expected sequence %d, got %d", last, f.Seq)
		}
		if f.Event.Method == "turn/completed" {
			p := f.Event.Params
			if p.ThreadID != thread || p.Turn.ID != turn || p.Turn.Status != "completed" {
				t.Fatalf("unexpected completion: %+v", p)
			}
			return
		}
	}
}
