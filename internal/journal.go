package towline

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
)

type eventRecord struct {
	Seq   int             `json:"seq"`
	Event json.RawMessage `json:"event"`
}

// Only offsets live in memory. The journal file holds the payloads.
type journal struct {
	mu      sync.Mutex
	file    *os.File
	offsets []int64
	end     int64
	wake    chan struct{}
	durable bool
}

func openJournal(path string) (*journal, error) {
	j := &journal{wake: make(chan struct{}), durable: path != ""}
	var err error
	if !j.durable {
		j.file, err = os.CreateTemp("", "towline-*.jsonl")
		return j, err
	}

	j.file, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	// Hold an exclusive lock until close. The OS releases it on process exit.
	err = lockJournal(j.file)
	if err != nil {
		j.file.Close()
		return nil, fmt.Errorf("lock event journal: %w", err)
	}
	if err := j.recover(); err != nil {
		j.file.Close()
		return nil, err
	}
	return j, nil
}

func (j *journal) close() {
	j.file.Close()
	if !j.durable {
		os.Remove(j.file.Name())
	}
}

func (j *journal) recover() error {
	reader := bufio.NewReader(j.file)
	for {
		line, err := reader.ReadBytes('\n')
		if errors.Is(err, io.EOF) {
			if len(line) > 0 {
				// A crash can leave the final record without its newline.
				if err := j.file.Truncate(j.end); err != nil {
					return err
				}
			}
			// A complete record may have reached the page cache before a crash.
			return j.file.Sync()
		}
		if err != nil {
			return err
		}
		var record eventRecord
		if err := json.Unmarshal(line, &record); err != nil || record.Seq != len(j.offsets)+1 || len(record.Event) == 0 {
			return fmt.Errorf("invalid event journal record at byte %d", j.end)
		}
		j.offsets = append(j.offsets, j.end)
		j.end += int64(len(line))
	}
}

// assigns a sequence number, writes the event to disk, and wakes the reader
func (j *journal) append(event json.RawMessage) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	data, err := json.Marshal(eventRecord{len(j.offsets) + 1, event})
	if err != nil {
		return err
	}

	data = append(data, '\n')
	if _, err := j.file.WriteAt(data, j.end); err != nil {
		return err
	}
	if j.durable {
		if err := j.file.Sync(); err != nil {
			return err
		}
	}

	j.offsets = append(j.offsets, j.end)
	j.end += int64(len(data))

	close(j.wake)
	j.wake = make(chan struct{})

	return nil
}

// Observe the cursor and its wake channel under the same lock.
func (j *journal) ready(cursor int, alwaysReady <-chan struct{}) <-chan struct{} {
	j.mu.Lock()
	defer j.mu.Unlock()
	if cursor <= len(j.offsets) {
		return alwaysReady
	}
	return j.wake
}

// simply returns the sequence number the next event will receive
// mainly used for the cursor
func (j *journal) next() int {
	j.mu.Lock()
	defer j.mu.Unlock()

	return len(j.offsets) + 1
}

// loads one event by sequence number, returning nil if it has yet to arrive
func (j *journal) read(seq int) ([]byte, error) {
	j.mu.Lock()

	// out of range
	if seq > len(j.offsets) {
		j.mu.Unlock()
		return nil, nil
	}

	start, end := j.offsets[seq-1], j.end
	if seq < len(j.offsets) {
		end = j.offsets[seq]
	}

	j.mu.Unlock()

	data := make([]byte, end-start)
	_, err := j.file.ReadAt(data, start)
	return data, err
}
