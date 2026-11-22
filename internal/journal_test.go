//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || windows

package towline

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestJournalRecoversAfterPartialWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	j, err := openJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	event := json.RawMessage(`{"method":"example"}`)
	if err := j.append(event); err != nil {
		j.close()
		t.Fatal(err)
	}
	if other, err := openJournal(path); err == nil {
		other.close()
		j.close()
		t.Fatal("second writer acquired the journal")
	}
	// Emulate a crash during the next record's write.
	_, err = j.file.WriteAt([]byte(`{"seq":2,"event":`), j.end)
	j.close()
	if err != nil {
		t.Fatal(err)
	}

	recovered, err := openJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.close()
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
		j.close()
		t.Fatal("accepted a skipped sequence")
	}
}
