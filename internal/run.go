package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Config struct{ URL, Token, Agent, Claude, Codex, Dir string }

// this starts the selected agent and manages the Towline WebSocket connection and event log
func Run(parent context.Context, c Config) error {
	root, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("locate event journal: %w", err)
	}

	return run(parent, c, filepath.Join(root, "towline"))
}

// starts the session using the specified journal directory
func run(parent context.Context, c Config, store string) error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") {
		return fmt.Errorf("endpoint must be a ws or wss URL")
	}

	binary := c.Codex
	args := []string{"app-server", "--listen", "stdio://"}
	var agent adapter
	pipes := &agentIO{}
	switch c.Agent {
	case "", "codex":
		if binary == "" {
			binary = "codex"
		}
		agent = &codexAdapter{agentIO: pipes, pending: make(map[string]bool)}
	case "claude":
		binary = c.Claude
		if binary == "" {
			binary = "claude"
		}
		id, err := newUUID()
		if err != nil {
			return err
		}
		args = []string{"-p", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json", "--permission-prompt-tool", "stdio", "--session-id", id}
		agent = &claudeAdapter{agentIO: pipes, thread: id, pending: make(map[string]bool)}
	default:
		return fmt.Errorf("unknown agent %q (choose codex or claude)", c.Agent)
	}
	path, err := exec.LookPath(binary)
	if err != nil {
		return fmt.Errorf("locate agent executable %q: %w", binary, err)
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	if err := os.MkdirAll(store, 0o700); err != nil {
		return fmt.Errorf("create event journal directory: %w", err)
	}

	j, err := openJournal(filepath.Join(store, "events.jsonl"))
	if err != nil {
		return fmt.Errorf("open event journal: %w", err)
	}
	defer j.file.Close()

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = c.Dir
	cmd.Stderr = os.Stderr

	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "TOWLINE_TOKEN=") {
			cmd.Env = append(cmd.Env, v)
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	if err = cmd.Start(); err != nil {
		return err
	}

	frames := make(chan incoming)
	go readAgent(ctx, stdout, frames)

	commands := make(chan request)
	done := make(chan struct{})
	go func() {
		defer close(done)
		connect(ctx, c, j, commands)
	}()

	pipes.ctx, pipes.stdin, pipes.stdout, pipes.log = ctx, json.NewEncoder(stdin), frames, j
	s := session{agentIO: pipes, adapter: agent}
	err = s.run(commands)
	cancel()
	stdin.Close()
	cmd.Wait()
	<-done

	if parent.Err() != nil {
		return nil
	}
	return err
}
