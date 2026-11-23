package towline

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

// A retry after reconnect must not write a second prompt or permission decision.
func TestClaudeTurnAndPermissionRetry(t *testing.T) {
	j, err := openJournal(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer j.file.Close()
	var writes bytes.Buffer
	pipes := &agentIO{stdin: json.NewEncoder(&writes), log: j, common: true}
	a := &claudeAdapter{agentIO: pipes, thread: "thread", pending: map[string]bool{}}
	s := session{agentIO: pipes, adapter: a}
	twice := func(c command) {
		t.Helper()
		writes.Reset()
		if r := s.execute(c); r.Error != nil || writes.Len() == 0 {
			t.Fatalf("%s: %+v", c.Op, r)
		}
		writes.Reset()
		if r := s.execute(c); r.Error != nil || writes.Len() != 0 {
			t.Fatalf("%s retry: %+v", c.Op, r)
		}
	}
	twice(command{ID: "send", Op: "send", Text: "hello"})
	turn := a.turn
	if r := s.execute(command{ID: "shutdown", Op: "prepare_shutdown"}); r.Error == nil || *r.Error != "busy" {
		t.Fatalf("shutdown: %+v", r)
	}
	emit := func(raw string) {
		t.Helper()
		if err := a.event(incoming{raw: json.RawMessage(raw)}); err != nil {
			t.Fatal(err)
		}
	}
	emit(`{"type":"control_request","request_id":"tool","request":{"subtype":"can_use_tool"}}`)
	twice(command{ID: "deny", Op: "respond", RequestID: json.RawMessage(`"tool"`), Result: json.RawMessage(`{"behavior":"deny","message":"skip"}`)})
	emit(`{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`)
	emit(`{"type":"result","subtype":"success","result":"done"}`)
	if a.turn != "" || len(a.pending) != 0 {
		t.Fatal("turn remained active")
	}
	for i, kind := range []string{"turn.started", "input.requested", "text", "turn.finished"} {
		raw, err := j.read(i + 1)
		if err != nil {
			t.Fatal(err)
		}
		var rec struct {
			Event struct{ Type, ThreadID, TurnID string }
		}
		if json.Unmarshal(raw, &rec) != nil || rec.Event.Type != kind || rec.Event.ThreadID != "thread" || rec.Event.TurnID != turn {
			t.Fatalf("record %d: %s", i+1, raw)
		}
	}
}

func TestCommonRequestResolutionReplays(t *testing.T) {
	for _, agent := range []string{"claude", "codex"} {
		t.Run(agent, func(t *testing.T) {
			j, err := openJournal(filepath.Join(t.TempDir(), "events.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			defer j.file.Close()
			pipes := &agentIO{log: j, common: true}
			var a adapter
			var raw string
			if agent == "claude" {
				a = &claudeAdapter{agentIO: pipes, thread: "t", turn: "u"}
				raw = `{"type":"control_cancel_request","request_id":"req"}`
			} else {
				a = &codexAdapter{agentIO: pipes, thread: "t", turn: "u"}
				raw = `{"method":"serverRequest/resolved","params":{"requestId":23}}`
			}
			if err := a.event(incoming{raw: json.RawMessage(raw)}); err != nil {
				t.Fatal(err)
			}
			data, err := j.read(1)
			if err != nil {
				t.Fatal(err)
			}
			var f struct {
				Event struct {
					Type, ThreadID, TurnID string
					RequestID              json.RawMessage
				}
			}
			id := "23"
			if agent == "claude" {
				id = `"req"`
			}
			if json.Unmarshal(data, &f) != nil || f.Event.Type != "input.resolved" || f.Event.ThreadID != "t" || f.Event.TurnID != "u" || string(f.Event.RequestID) != id {
				t.Fatalf("unexpected record %s", data)
			}
		})
	}
}
