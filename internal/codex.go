package towline

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type codexFrame struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}
type codexAdapter struct {
	*agentIO
	thread, turn, completed     string
	stopping                    bool
	pending                     map[string]bool
	serial                      int
	settings, defaults, applied modelSettings
	modelEfforts                map[string]string
}

func newCodexAdapter(pipes *agentIO) (adapter, []string, error) {
	return &codexAdapter{agentIO: pipes, pending: make(map[string]bool)}, []string{"app-server", "--listen", "stdio://"}, nil
}

func (*codexAdapter) operations() []string {
	return []string{"send", "steer", "interrupt", "respond", "list_models", "get_model_settings", "set_model_settings"}
}
func (s *codexAdapter) event(in incoming) error { _, err := s.decode(in); return err }

// waits for the next Codex frame or session cancellation
func (s *codexAdapter) receive() (codexFrame, error) {
	select {
	case <-s.ctx.Done():
		return codexFrame{}, s.ctx.Err()
	case in := <-s.stdout:
		return s.decode(in)
	}
}

// parses a Codex frame, logs events, and tracks active turns and pending requests
func (s *codexAdapter) decode(in incoming) (codexFrame, error) {
	var f codexFrame
	if in.err != nil {
		return f, in.err
	}

	if err := json.Unmarshal(in.raw, &f); err != nil {
		return f, err
	}

	if f.Method == "" {
		return f, nil
	}

	if err := s.log.append(in.raw); err != nil {
		return f, err
	}

	if len(f.ID) > 0 {
		s.pending[string(f.ID)] = true
	}

	var p struct {
		ThreadID  string
		Turn      struct{ ID string }
		RequestID json.RawMessage
	}
	if f.Method == "turn/started" || f.Method == "turn/completed" || f.Method == "serverRequest/resolved" {
		if err := json.Unmarshal(f.Params, &p); err != nil {
			return f, err
		}

		if f.Method == "serverRequest/resolved" {
			delete(s.pending, string(p.RequestID))
		}

		if p.ThreadID == s.thread {
			if f.Method == "turn/started" {
				s.turn = p.Turn.ID
			}
			if f.Method == "turn/completed" {
				s.completed = p.Turn.ID
				if s.turn == p.Turn.ID {
					s.turn = ""
				}
			}
		}
	}

	return f, nil
}

// sends a Codex request and processes events until its reply arrives
func (s *codexAdapter) call(method string, params object) (json.RawMessage, error) {
	s.serial++
	id := fmt.Sprint(s.serial)
	if err := s.write(object{"id": s.serial, "method": method, "params": params}); err != nil {
		return nil, err
	}

	for {
		f, err := s.receive()
		if err != nil {
			s.fatal = err
			return nil, err
		}
		if f.Method != "" {
			continue
		}
		if string(f.ID) != id {
			s.fatal = fmt.Errorf("unexpected Codex response ID %s", f.ID)
			return nil, s.fatal
		}
		if f.Error != nil {
			return nil, errors.New(f.Error.Message)
		}
		return f.Result, nil
	}
}

// executes one endpoint command against the current session
func (s *codexAdapter) handle(c command) (any, error) {
	switch c.Op {
	case "list_models":
		models, _, err := s.listModels()
		return models, err
	case "get_model_settings":
		return s.settings, nil
	case "set_model_settings":
		return s.setModelSettings(c)
	case "prepare_shutdown":
		if s.turn != "" || len(s.pending) > 0 {
			return nil, errors.New("busy")
		}

		s.stopping = true
		return "ready_to_stop", nil
	case "send", "steer":
		if s.stopping {
			return nil, errors.New("stopping")
		}

		if strings.TrimSpace(c.Text) == "" {
			return nil, errors.New("text required")
		}

		input := []object{{"type": "text", "text": c.Text, "text_elements": []string{}}}

		if c.Op == "steer" {
			if c.TurnID == "" || c.TurnID != s.turn {
				return nil, errors.New("turn is not active")
			}
			return s.call("turn/steer", object{"threadId": s.thread, "expectedTurnId": c.TurnID, "input": input})
		}

		if s.turn != "" {
			return nil, errors.New("busy")
		}

		if s.thread == "" {
			raw, err := s.call("thread/start", object{})
			if err != nil {
				return nil, err
			}

			var r struct {
				Thread          struct{ ID string }
				Model           *string
				ReasoningEffort *string
			}
			if json.Unmarshal(raw, &r) != nil || r.Thread.ID == "" {
				return nil, errors.New("missing Codex thread ID")
			}

			s.thread = r.Thread.ID
			s.defaults = modelSettings{r.Model, r.ReasoningEffort}
		}
		params := object{"threadId": s.thread, "input": input}
		s.addModelSettings(params)
		raw, err := s.call("turn/start", params)
		if err != nil {
			return nil, err
		}

		s.applied = s.settings
		var r struct{ Turn struct{ ID string } }
		if json.Unmarshal(raw, &r) != nil || r.Turn.ID == "" {
			return nil, errors.New("missing Codex turn ID")
		}

		if s.completed != r.Turn.ID {
			s.turn = r.Turn.ID
		}

		return object{"turnId": r.Turn.ID, "threadId": s.thread}, nil
	case "interrupt":
		if c.TurnID == "" || c.TurnID != s.turn {
			return nil, errors.New("turn is not active")
		}

		return s.call("turn/interrupt", object{"threadId": s.thread, "turnId": s.turn})
	case "respond":
		id := string(c.RequestID)
		if !s.pending[id] {
			return nil, errors.New("unknown request ID")
		}

		if len(c.Result) == 0 {
			return nil, errors.New("result required")
		}

		if err := s.write(object{"id": c.RequestID, "result": c.Result}); err != nil {
			return nil, err
		}

		delete(s.pending, id)
		return nil, nil
	default:
		return nil, errors.New("unknown command")
	}
}

// initializes the Codex protocol and processes commands and events in order
func (s *codexAdapter) initialize() error {
	if _, err := s.call("initialize", object{"clientInfo": object{"name": "towline", "version": "1"}, "capabilities": object{"experimentalApi": true}}); err != nil {
		return err
	}

	if err := s.write(object{"method": "initialized"}); err != nil {
		return err
	}

	return nil
}

func (s *codexAdapter) listModels() ([]modelOption, string, error) {
	models := []modelOption{}
	defaultEfforts := make(map[string]string)
	var defaultModel string
	params := object{}
	for {
		raw, err := s.call("model/list", params)
		if err != nil {
			return nil, "", err
		}
		var page struct {
			Data []struct {
				Model, DisplayName, DefaultReasoningEffort string
				IsDefault                                  bool
				SupportedReasoningEfforts                  []struct{ ReasoningEffort string }
			}
			NextCursor string
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, "", err
		}
		for _, m := range page.Data {
			efforts := make([]string, 0, len(m.SupportedReasoningEfforts))
			for _, effort := range m.SupportedReasoningEfforts {
				efforts = append(efforts, effort.ReasoningEffort)
			}
			models = append(models, modelOption{m.Model, m.DisplayName, efforts})
			defaultEfforts[m.Model] = m.DefaultReasoningEffort
			if m.IsDefault {
				defaultModel = m.Model
			}
		}
		if page.NextCursor == "" {
			s.modelEfforts = defaultEfforts
			return models, defaultModel, nil
		}
		params["cursor"] = page.NextCursor
	}
}

func (s *codexAdapter) setModelSettings(c command) (modelSettings, error) {
	if s.stopping {
		return s.settings, errors.New("stopping")
	}
	settings, err := c.modelSettings(s.settings)
	if err != nil {
		return s.settings, err
	}
	models, defaultModel, err := s.listModels()
	if err != nil {
		return s.settings, err
	}
	if s.defaults.Model != nil {
		defaultModel = *s.defaults.Model
	} else if settings.Model == nil && settings.Effort != nil {
		raw, err := s.call("config/read", object{})
		if err != nil {
			return s.settings, err
		}
		var result struct{ Config struct{ Model string } }
		if err := json.Unmarshal(raw, &result); err != nil {
			return s.settings, err
		}
		if result.Config.Model != "" {
			defaultModel = result.Config.Model
		}
	}
	if err := settings.validate(models, defaultModel); err != nil {
		return s.settings, err
	}
	s.settings = settings
	return settings, nil
}

func (s *codexAdapter) addModelSettings(params object) {
	model, effort := s.settings.Model, s.settings.Effort
	if model == nil && s.applied.Model != nil {
		model = s.defaults.Model
	}
	if effort == nil && (model != nil || s.applied.Effort != nil) {
		effort = s.defaults.Effort
		if s.settings.Model != nil || effort == nil {
			if model == nil {
				model = s.defaults.Model
			}
			if model != nil {
				if level := s.modelEfforts[*model]; level != "" {
					effort = &level
				}
			}
		}
	}
	if model != nil {
		params["model"] = *model
	}
	if effort != nil {
		params["effort"] = *effort
	}
}
