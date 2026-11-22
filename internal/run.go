package towline

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Config struct{ URL, Token, Codex, Dir string }

func eventLogPath(c Config) (string, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir, err := filepath.Abs(c.Dir)
	if err != nil {
		return "", err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	store := filepath.Join(root, "towline", "events")
	if err := os.MkdirAll(store, 0700); err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte(c.URL + "\x00" + dir))
	return filepath.Join(store, fmt.Sprintf("%x.jsonl", key)), nil
}

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

	logPath, err := eventLogPath(c)
	if err != nil {
		return fmt.Errorf("locate event journal: %w", err)
	}
	j, err := openJournal(logPath)
	if err != nil {
		return fmt.Errorf("open event journal: %w", err)
	}
	defer j.file.Close()

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
