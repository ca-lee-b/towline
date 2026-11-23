package towline

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type claudeAdapter struct {
	*agentIO
	thread, turn string
	pending      map[string]bool
	stopping     bool
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func (*claudeAdapter) initialize() error    { return nil }
func (*claudeAdapter) operations() []string { return []string{"send", "interrupt", "respond"} }

type claudeFrame struct {
	Type      string
	RequestID string `json:"request_id"`
	Request   struct{ Subtype string }
	Response  struct {
		RequestID      string `json:"request_id"`
		Subtype, Error string
	}
}

func (s *claudeAdapter) decode(in incoming) (claudeFrame, error) {
	var f claudeFrame
	if in.err != nil {
		return f, in.err
	}
	if err := json.Unmarshal(in.raw, &f); err != nil {
		return f, err
	}
	if f.Type == "control_response" {
		return f, nil
	}
	if err := s.record(f, in.raw); err != nil {
		return f, err
	}
	switch f.Type {
	case "control_request":
		if f.Request.Subtype == "can_use_tool" {
			s.pending[f.RequestID] = true
		}
	case "control_cancel_request":
		delete(s.pending, f.RequestID)
	case "result":
		s.turn = ""
		clear(s.pending)
	}
	return f, nil
}

func (s *claudeAdapter) event(in incoming) error {
	_, err := s.decode(in)
	return err
}

func (s *claudeAdapter) handle(c command) (any, error) {
	switch c.Op {
	case "prepare_shutdown":
		if s.turn != "" || len(s.pending) > 0 {
			return nil, errors.New("busy")
		}
		s.stopping = true
		return "ready_to_stop", nil
	case "send":
		if s.stopping {
			return nil, errors.New("stopping")
		}
		if strings.TrimSpace(c.Text) == "" {
			return nil, errors.New("text required")
		}
		if s.turn != "" {
			return nil, errors.New("busy")
		}
		id, err := newUUID()
		if err != nil {
			return nil, err
		}
		err = s.write(object{"type": "user", "uuid": id, "session_id": s.thread, "parent_tool_use_id": nil, "message": object{"role": "user", "content": c.Text}})
		if err != nil {
			return nil, err
		}
		s.turn = id
		if s.common {
			if err := s.logCommon("turn.started", s.thread, id, nil); err != nil {
				s.fatal = err
				return nil, err
			}
		}
		return object{"threadId": s.thread, "turnId": id}, nil
	case "respond":
		var id string
		if json.Unmarshal(c.RequestID, &id) != nil || !s.pending[id] {
			return nil, errors.New("unknown request ID")
		}
		var decision struct {
			Behavior     string
			UpdatedInput map[string]any
			Message      string
		}
		if json.Unmarshal(c.Result, &decision) != nil || !(decision.Behavior == "allow" && decision.UpdatedInput != nil || decision.Behavior == "deny" && decision.Message != "") {
			return nil, errors.New("expected allow with updatedInput or deny with message")
		}
		err := s.write(object{"type": "control_response", "response": object{"subtype": "success", "request_id": id, "response": c.Result}})
		if err == nil {
			delete(s.pending, id)
		}
		return nil, err
	case "interrupt":
		if c.TurnID == "" || c.TurnID != s.turn {
			return nil, errors.New("turn is not active")
		}
		return nil, s.interrupt()
	case "steer":
		return nil, errors.New("steer is unsupported by Claude Code")
	default:
		return nil, errors.New("unknown command")
	}
}

func (s *claudeAdapter) interrupt() error {
	id, err := newUUID()
	if err != nil {
		return err
	}
	if err = s.write(object{"type": "control_request", "request_id": id, "request": object{"subtype": "interrupt"}}); err != nil {
		return err
	}
	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case in := <-s.stdout:
			f, err := s.decode(in)
			if err != nil {
				s.fatal = err
				return err
			}
			if f.Type == "control_response" && f.Response.RequestID == id {
				if f.Response.Subtype != "success" {
					return fmt.Errorf("Claude interrupt failed: %s", f.Response.Error)
				}
				return nil
			}
		}
	}
}
