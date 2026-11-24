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

	// callers that bypass the CLI still get the default adapter
	// ex: -agent claude -claude /opt/bin/claude (selects Claude and runs /opt/bin/claude)
	name := c.Agent
	if name == "" {
		name = "codex"
	}
	pipes := &agentIO{}
	var agent adapter
	var binary string
	var args []string

	switch name {
	case "claude":
		binary = c.Claude
		agent, args, err = newClaudeAdapter(pipes)
	case "codex":
		binary = c.Codex
		agent, args, err = newCodexAdapter(pipes)
	default:
		return fmt.Errorf("unknown agent %q (choose claude or codex)", name)
	}
	if err != nil {
		return err
	}
	if binary == "" {
		binary = name
	}
	path, err := locateExecutable(binary)
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

func locateExecutable(binary string) (string, error) {
	search := os.Getenv("TOWLINE_PATH")
	if search == "" || filepath.Base(binary) != binary {
		return exec.LookPath(binary)
	}
	for _, dir := range filepath.SplitList(search) {
		if dir == "" {
			continue
		}
		candidate, err := filepath.Abs(filepath.Join(dir, binary))
		if err != nil {
			return "", err
		}
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("not found on TOWLINE_PATH: %w", exec.ErrNotFound)
}
