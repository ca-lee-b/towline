//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || windows

package towline

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestJournalRecoversAfterPartialWrite(t *testing.T) {
	const crashExit = 42
	event := json.RawMessage(`{"method":"example"}`)
	if os.Getenv("TOWLINE_CRASH_WRITER") == "1" {
		j, err := openJournal("events.jsonl")
		if err != nil {
			t.Fatal(err)
		}
		if err := j.append(event); err != nil {
			t.Fatal(err)
		}
		if other, err := openJournal("events.jsonl"); err == nil {
			other.file.Close()
			t.Fatal("second writer acquired the journal")
		}
		if _, err := j.file.WriteAt([]byte(`{"seq":2,"event":`), j.end); err != nil {
			t.Fatal(err)
		}
		os.Exit(crashExit) // Skip cleanup with the journal lock still held.
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestJournalRecoversAfterPartialWrite$")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TOWLINE_CRASH_WRITER=1")
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != crashExit {
		t.Fatalf("crash writer: %v: %s", err, output)
	}
	path := filepath.Join(dir, "events.jsonl")
	recovered, err := openJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.file.Close()
	if got := recovered.next(); got != 2 {
		t.Fatalf("next sequence = %d, want 2", got)
	}
	info, err := recovered.file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != recovered.end {
		t.Fatalf("partial record was retained: size = %d, want %d", info.Size(), recovered.end)
	}
	if err := recovered.append(event); err != nil {
		t.Fatal(err)
	}
	for _, seq := range []int{1, 2} {
		data, err := recovered.read(seq)
		if err != nil {
			t.Fatal(err)
		}
		var record eventRecord
		if err := json.Unmarshal(data, &record); err != nil || record.Seq != seq || string(record.Event) != string(event) {
			t.Fatalf("sequence %d: got %s (%v)", seq, data, err)
		}
	}
}

func TestJournalRejectsInvalidCompleteRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, []byte("{\"seq\":2,\"event\":{}}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if j, err := openJournal(path); err == nil {
		j.file.Close()
		t.Fatal("accepted a skipped sequence")
	}
}
