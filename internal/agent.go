package towline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
)

type object = map[string]any

type command struct {
	ID        string          `json:"id"`
	Op        string          `json:"op"`
	Text      string          `json:"text"`
	TurnID    string          `json:"turnId"`
	RequestID json.RawMessage `json:"requestId"`
	Result    json.RawMessage `json:"result,omitempty"`
	Model     json.RawMessage `json:"model,omitempty"`
	Effort    json.RawMessage `json:"effort,omitempty"`
	From      int             `json:"from"`
}

type commandReply struct {
	ID     string  `json:"id"`
	Result *any    `json:"result,omitempty"`
	Error  *string `json:"error,omitempty"`
}

func successReply(id string, result any) commandReply {
	// A present result may encode as JSON null.
	return commandReply{ID: id, Result: &result}
}

func errorReply(id, message string) commandReply {
	return commandReply{ID: id, Error: &message}
}

type commandOutcome struct {
	digest [sha256.Size]byte
	reply  commandReply
}

type request struct {
	command
	reply chan commandReply
}
type incoming struct {
	raw json.RawMessage
	err error
}

// The session loop serializes controller commands and provider events.
type adapter interface {
	initialize() error
	handle(command) (any, error)
	event(incoming) error
	operations() []string
}

type agentIO struct {
	ctx    context.Context
	stdin  *json.Encoder
	stdout <-chan incoming
	log    *journal
	fatal  error
}

type session struct {
	*agentIO
	adapter
	outcomes map[string]commandOutcome
}

func (s *agentIO) write(v object) error {
	if err := s.stdin.Encode(v); err != nil {
		s.fatal = err
		return err
	}
	return nil
}

// The session records the outcome before attempting reply delivery.
func (s *session) execute(c command) commandReply {
	var digest [sha256.Size]byte
	mutating := c.Op == "send" || c.Op == "steer" || c.Op == "interrupt" || c.Op == "respond" || c.Op == "set_model_settings"
	if mutating {
		payload, err := json.Marshal(c)
		if err != nil {
			return errorReply(c.ID, err.Error())
		}
		digest = sha256.Sum256(payload)
		if previous, ok := s.outcomes[c.ID]; ok {
			if previous.digest != digest {
				return errorReply(c.ID, "command ID reused with different payload")
			}
			return previous.reply
		}
	}
	if c.Op == "capabilities" {
		return successReply(c.ID, object{"operations": s.operations()})
	}
	result, err := s.handle(c)
	reply := successReply(c.ID, result)
	if err != nil {
		reply = errorReply(c.ID, err.Error())
	}
	if mutating {
		if s.outcomes == nil {
			s.outcomes = make(map[string]commandOutcome)
		}
		s.outcomes[c.ID] = commandOutcome{digest, reply}
	}
	return reply
}

func (s *session) run(commands <-chan request) error {
	if err := s.initialize(); err != nil {
		return err
	}

	for {
		select {
		case <-s.ctx.Done():
			return s.ctx.Err()
		case in := <-s.stdout:
			if err := s.event(in); err != nil {
				return err
			}
		case req := <-commands:
			req.reply <- s.execute(req.command)
			if s.fatal != nil {
				return s.fatal
			}
		}
	}
}

// reads JSON messages from agent stdout and passes them to the session
func readAgent(ctx context.Context, r io.Reader, out chan<- incoming) {
	dec := json.NewDecoder(r)
	for {
		var raw json.RawMessage
		err := dec.Decode(&raw)
		select {
		case out <- incoming{raw, err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}
