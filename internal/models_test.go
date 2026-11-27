package towline

import (
	"encoding/json"
	"testing"
)

func TestModelSettingsUpdatesAndRetries(t *testing.T) {
	a := &claudeAdapter{models: []modelOption{
		{ID: "default", Efforts: []string{"medium"}},
		{ID: "sonnet", Efforts: []string{"high"}},
	}}
	s := session{adapter: a}
	set := func(id, model, effort string) commandReply {
		c := command{ID: id, Op: "set_model_settings"}
		if model != "" {
			c.Model = json.RawMessage(model)
		}
		if effort != "" {
			c.Effort = json.RawMessage(effort)
		}
		return s.execute(c)
	}
	want := func(reply commandReply, expected string) {
		t.Helper()
		raw, err := json.Marshal(reply.Result)
		if reply.Error != nil {
			t.Fatal(*reply.Error)
		}
		if err != nil || string(raw) != expected {
			t.Fatalf("got %s, error %v, want %s", raw, err, expected)
		}
	}
	// Updating settings during a turn must not send anything to the agent yet.
	a.turn = "active"
	want(set("1", `"sonnet"`, `"high"`), `{"model":"sonnet","effort":"high"}`)
	if reply := set("2", "null", ""); reply.Error == nil {
		t.Fatal("accepted an unsupported combination")
	}
	want(s.execute(command{Op: "get_model_settings"}), `{"model":"sonnet","effort":"high"}`)
	want(set("3", "", "null"), `{"model":"sonnet","effort":null}`)
	want(set("4", "null", ""), `{"model":null,"effort":null}`)
	want(set("1", `"sonnet"`, `"high"`), `{"model":"sonnet","effort":"high"}`)
	want(s.execute(command{Op: "get_model_settings"}), `{"model":null,"effort":null}`)
}

func TestCodexRestoresThreadDefaultsAfterOverrides(t *testing.T) {
	model, effort := "default", "medium"
	override, high := "override", "high"
	s := codexAdapter{
		defaults: modelSettings{&model, &effort},
		applied:  modelSettings{&override, &high},
	}
	params := object{}
	s.addModelSettings(params)
	if params["model"] != model || params["effort"] != effort {
		t.Fatalf("expected original model and effort, got %v", params)
	}
}
