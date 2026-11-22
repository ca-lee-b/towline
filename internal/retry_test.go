package towline

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRetryReturnsOriginalOutcome(t *testing.T) {
	var writes bytes.Buffer
	s := session{commands: testCommandStore(t, filepath.Join(t.TempDir(), "commands.db")), stdin: json.NewEncoder(&writes), pending: map[string]bool{"1": true}}
	cmd := command{ID: "answer", Op: "respond", RequestID: json.RawMessage("1"), Result: json.RawMessage("true")}
	first := s.execute(cmd)
	if first.Error != nil || writes.Len() == 0 {
		t.Fatalf("first reply: %v", first)
	}
	writes.Reset()
	if reply := s.execute(cmd); reply.Error != nil || reply.ID != cmd.ID || writes.Len() != 0 {
		t.Fatalf("retry: %v", reply)
	}
	cmd.Result = json.RawMessage("false")
	if reply := s.execute(cmd); reply.Error == nil || *reply.Error != "command ID reused with different payload" {
		t.Fatalf("conflict: %v", reply)
	}
}

func TestRetryDistinguishesMissingAndNullResult(t *testing.T) {
	for _, firstNull := range []bool{false, true} {
		var writes bytes.Buffer
		s := session{commands: testCommandStore(t, filepath.Join(t.TempDir(), "commands.db")), stdin: json.NewEncoder(&writes), pending: map[string]bool{"1": true}}
		c := command{ID: "answer", Op: "respond", RequestID: json.RawMessage("1")}
		if firstNull {
			c.Result = json.RawMessage("null")
		}
		reply := s.execute(c)
		if firstNull && reply.Error != nil || !firstNull && (reply.Error == nil || *reply.Error != "result required") {
			t.Fatalf("firstNull=%t: initial reply %v", firstNull, reply)
		}
		if firstNull {
			c.Result = nil
		} else {
			c.Result = json.RawMessage("null")
		}
		if reply := s.execute(c); reply.Error == nil || *reply.Error != "command ID reused with different payload" {
			t.Fatalf("firstNull=%t: conflict reply %v", firstNull, reply)
		}
	}
}

func testCommandStore(t *testing.T, path string) *commandStore {
	t.Helper()
	store, err := openCommandStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.db.Close() })
	return store
}

func TestRetryAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commands.db")
	var writes bytes.Buffer
	start := func() *session {
		return &session{commands: testCommandStore(t, path), stdin: json.NewEncoder(&writes), pending: map[string]bool{"1": true}}
	}
	s := start()
	done := command{ID: "done", Op: "respond", RequestID: json.RawMessage("1"), Result: json.RawMessage("true")}
	first := s.execute(done)
	if first.Error != nil || writes.Len() == 0 {
		t.Fatal(first)
	}
	pending := done
	pending.ID = "pending"
	if prior, err := s.commands.reserve(pending); err != nil || prior != nil {
		t.Fatalf("reserve: %v %v", prior, err)
	}
	s.commands.db.Close()
	s = start()
	writes.Reset()
	got, _ := json.Marshal(s.execute(done))
	want, _ := json.Marshal(first)
	if !bytes.Equal(got, want) {
		t.Fatalf("retry: %s, want %s", got, want)
	}
	if reply := s.execute(pending); reply.Error == nil || *reply.Error != unknownOutcome {
		t.Fatalf("interrupted retry: %+v", reply)
	}
	if writes.Len() != 0 {
		t.Fatal("retry dispatched to Codex")
	}
}
