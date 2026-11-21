package towline

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRetryReturnsOriginalOutcome(t *testing.T) {
	var writes bytes.Buffer
	s := session{stdin: json.NewEncoder(&writes), pending: map[string]bool{"1": true}}
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
		s := session{stdin: json.NewEncoder(&writes), pending: map[string]bool{"1": true}}
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
