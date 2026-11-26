package towline

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type claudeAdapter struct {
	*agentIO
	thread, turn      string
	pending           map[string]bool
	stopping          bool
	settings, applied modelSettings
	effortNeedsApply  bool
	models            []modelOption
}

func newClaudeAdapter(pipes *agentIO) (adapter, []string, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, nil, err
	}
	thread := id.String()
	args := []string{"-p", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json", "--permission-prompt-tool", "stdio", "--session-id", thread}
	return &claudeAdapter{agentIO: pipes, thread: thread, pending: make(map[string]bool)}, args, nil
}

func (*claudeAdapter) initialize() error { return nil }
func (*claudeAdapter) operations() []string {
	return []string{"send", "interrupt", "respond", "list_models", "get_model_settings", "set_model_settings"}
}

type claudeFrame struct {
	Type      string
	RequestID string `json:"request_id"`
	Request   struct{ Subtype string }
	Response  struct {
		RequestID      string `json:"request_id"`
		Subtype, Error string
		Result         json.RawMessage `json:"response"`
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
	event, err := json.Marshal(object{"agent": "claude", "threadId": s.thread, "turnId": s.turn, "message": in.raw})
	if err != nil {
		return f, err
	}
	if err := s.log.append(event); err != nil {
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
	case "list_models":
		return s.listModels()
	case "get_model_settings":
		return s.settings, nil
	case "set_model_settings":
		if s.stopping {
			return nil, errors.New("stopping")
		}
		settings, err := c.modelSettings(s.settings)
		if err != nil {
			return nil, err
		}
		models, err := s.listModels()
		if err != nil {
			return nil, err
		}
		if err := settings.validate(models, "default"); err != nil {
			return nil, err
		}
		s.settings = settings
		return settings, nil
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
		id, err := uuid.NewRandom()
		if err != nil {
			return nil, err
		}
		if err := s.applyModelSettings(); err != nil {
			return nil, err
		}
		err = s.write(object{"type": "user", "uuid": id, "session_id": s.thread, "parent_tool_use_id": nil, "message": object{"role": "user", "content": c.Text}})
		if err != nil {
			return nil, err
		}
		s.turn = id.String()
		return object{"threadId": s.thread, "turnId": s.turn}, nil
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
		return s.control(object{"subtype": "interrupt"})
	case "steer":
		return nil, errors.New("steer is unsupported by Claude Code")
	default:
		return nil, errors.New("unknown command")
	}
}

func (s *claudeAdapter) control(request object) (json.RawMessage, error) {
	id, err := uuid.NewRandom()
	if err != nil {
		return nil, err
	}
	if err = s.write(object{"type": "control_request", "request_id": id, "request": request}); err != nil {
		return nil, err
	}
	for {
		select {
		case <-s.ctx.Done():
			return nil, s.ctx.Err()
		case in := <-s.stdout:
			f, err := s.decode(in)
			if err != nil {
				s.fatal = err
				return nil, err
			}
			if f.Type == "control_response" && f.Response.RequestID == id.String() {
				if f.Response.Subtype != "success" {
					return nil, fmt.Errorf("Claude %s failed: %s", request["subtype"], f.Response.Error)
				}
				return f.Response.Result, nil
			}
		}
	}
}

func (s *claudeAdapter) listModels() ([]modelOption, error) {
	if s.models != nil {
		return s.models, nil
	}
	raw, err := s.control(object{"subtype": "initialize"})
	if err != nil {
		return nil, err
	}
	var result struct {
		Models []struct {
			ID      string   `json:"value"`
			Name    string   `json:"displayName"`
			Efforts []string `json:"supportedEffortLevels"`
		}
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	s.models = make([]modelOption, 0, len(result.Models))
	for _, m := range result.Models {
		s.models = append(s.models, modelOption{m.ID, m.Name, append([]string{}, m.Efforts...)})
	}
	return s.models, nil
}

func (s *claudeAdapter) applyModelSettings() error {
	if s.settings.Model != s.applied.Model {
		if _, err := s.control(object{"subtype": "set_model", "model": s.settings.Model}); err != nil {
			return err
		}
		s.applied.Model = s.settings.Model
		// Reapply an explicit effort against the model that will use it.
		s.effortNeedsApply = s.settings.Effort != nil
	}
	if s.settings.Effort != s.applied.Effort || (s.effortNeedsApply && s.settings.Effort != nil) {
		if _, err := s.control(object{"subtype": "apply_flag_settings", "settings": object{"effortLevel": s.settings.Effort}}); err != nil {
			return err
		}
		s.applied.Effort = s.settings.Effort
	}
	s.effortNeedsApply = false
	return nil
}
