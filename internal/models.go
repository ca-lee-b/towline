package towline

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type modelOption struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Efforts []string `json:"efforts"`
}

type modelSettings struct {
	Model  *string `json:"model"`
	Effort *string `json:"effort"`
}

func (c command) modelSettings(current modelSettings) (modelSettings, error) {
	if c.Model == nil && c.Effort == nil {
		return current, errors.New("model or effort required")
	}
	for _, field := range []struct {
		name string
		raw  json.RawMessage
		dst  **string
	}{
		{"model", c.Model, &current.Model},
		{"effort", c.Effort, &current.Effort},
	} {
		if field.raw == nil {
			continue
		}
		var value *string
		if err := json.Unmarshal(field.raw, &value); err != nil || (value != nil && strings.TrimSpace(*value) == "") {
			return current, fmt.Errorf("%s must be a nonempty string or null", field.name)
		}
		*field.dst = value
	}
	return current, nil
}

func (s modelSettings) validate(models []modelOption, defaultModel string) error {
	id := defaultModel
	if s.Model != nil {
		id = *s.Model
	}
	for _, model := range models {
		if model.ID == id {
			if s.Effort != nil && !slices.Contains(model.Efforts, *s.Effort) {
				return fmt.Errorf("model %q does not support effort %q", id, *s.Effort)
			}
			return nil
		}
	}
	if s.Model == nil && s.Effort == nil {
		return nil
	}
	return fmt.Errorf("model %q is not listed by the provider", id)
}
