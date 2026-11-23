package towline

import "encoding/json"

// Common events retain native tool requests so controllers can answer them.
func (s *agentIO) logCommon(kind, thread, turn string, details object) error {
	if details == nil {
		details = object{}
	}
	details["type"], details["threadId"], details["turnId"] = kind, thread, turn
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	return s.log.append(raw)
}

func (s *codexAdapter) record(f frame, raw json.RawMessage) error {
	if !s.common {
		return s.log.append(raw)
	}
	if len(f.ID) == 0 && f.Method != "turn/started" && f.Method != "item/agentMessage/delta" && f.Method != "turn/completed" && f.Method != "serverRequest/resolved" {
		return nil
	}
	var p struct {
		ThreadID, TurnID, Delta string
		RequestID               json.RawMessage
		Turn                    struct {
			ID, Status string
			Error      json.RawMessage
		}
	}
	if len(f.Params) > 0 {
		if err := json.Unmarshal(f.Params, &p); err != nil {
			return err
		}
	}
	if len(f.ID) > 0 || f.Method == "serverRequest/resolved" {
		if p.ThreadID == "" {
			p.ThreadID = s.thread
		}
		if p.TurnID == "" {
			p.TurnID = s.turn
		}
		if f.Method == "serverRequest/resolved" {
			return s.logCommon("input.resolved", p.ThreadID, p.TurnID, object{"requestId": p.RequestID})
		}
		return s.logCommon("input.requested", p.ThreadID, p.TurnID, object{"requestId": f.ID, "request": raw})
	}
	switch f.Method {
	case "turn/started":
		return s.logCommon("turn.started", p.ThreadID, p.Turn.ID, nil)
	case "item/agentMessage/delta":
		return s.logCommon("text", p.ThreadID, p.TurnID, object{"text": p.Delta})
	case "turn/completed":
		return s.logCommon("turn.finished", p.ThreadID, p.Turn.ID, object{"status": p.Turn.Status, "error": p.Turn.Error})
	}
	return nil
}

func (s *claudeAdapter) record(f claudeFrame, raw json.RawMessage) error {
	if !s.common {
		event, err := json.Marshal(object{"agent": "claude", "threadId": s.thread, "turnId": s.turn, "message": raw})
		if err != nil {
			return err
		}
		return s.log.append(event)
	}
	switch f.Type {
	case "control_cancel_request":
		return s.logCommon("input.resolved", s.thread, s.turn, object{"requestId": f.RequestID})
	case "control_request":
		if f.Request.Subtype == "can_use_tool" {
			return s.logCommon("input.requested", s.thread, s.turn, object{"requestId": f.RequestID, "request": raw})
		}
	case "assistant":
		var msg struct {
			Message struct{ Content []struct{ Type, Text string } }
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			return err
		}
		for _, b := range msg.Message.Content {
			if b.Type == "text" {
				if err := s.logCommon("text", s.thread, s.turn, object{"text": b.Text}); err != nil {
					return err
				}
			}
		}
	case "result":
		var result struct {
			IsError bool `json:"is_error"`
			Subtype string
			Result  string
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		status := "completed"
		if result.IsError || result.Subtype != "success" {
			status = "failed"
		}
		return s.logCommon("turn.finished", s.thread, s.turn, object{"status": status, "result": result.Result})
	}
	return nil
}
