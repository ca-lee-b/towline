package towline

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

type Config struct{ URL, Token, Codex, Dir, EventLog string }

// this starts Codex and manages the Towline WebSocket connection and event log
func Run(parent context.Context, c Config) error {
	u, err := url.Parse(c.URL)
	if err != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") {
		return fmt.Errorf("endpoint must be a ws or wss URL")
	}

	path, err := exec.LookPath(c.Codex)
	if err != nil {
		return fmt.Errorf("install Codex before starting Towline: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	j, err := openJournal(c.EventLog)
	if err != nil {
		return fmt.Errorf("open event journal: %w", err)
	}
	defer j.close()

	cmd := exec.CommandContext(ctx, path, "app-server", "--listen", "stdio://")
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
	go readCodex(ctx, stdout, frames)

	commands := make(chan request)
	done := make(chan struct{})
	go func() {
		defer close(done)
		connect(ctx, c, j, commands)
	}()

	s := session{
		ctx:     ctx,
		stdin:   json.NewEncoder(stdin),
		stdout:  frames,
		log:     j,
		pending: make(map[string]bool),
	}
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
