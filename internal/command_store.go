package towline

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

const unknownOutcome = "command outcome unknown"

var commandBucket = []byte("commands")

type commandStore struct{ db *bolt.DB }

// opens the sandbox's durable command history
func openCommandStore(path string) (*commandStore, error) {
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		return nil, err
	}
	return &commandStore{db}, nil
}

// stores the payload digest before the first dispatch or returns the previous reply
func (store *commandStore) reserve(c command) (*commandReply, error) {
	payload, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	key := sha256.Sum256([]byte(c.ID))
	tx, err := store.db.Begin(true)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	b, err := tx.CreateBucketIfNotExists(commandBucket)
	if err != nil {
		return nil, err
	}
	data := b.Get(key[:])
	if data == nil {
		if err := b.Put(key[:], digest[:]); err != nil {
			return nil, err
		}
		return nil, tx.Commit()
	}
	if len(data) < sha256.Size {
		return nil, fmt.Errorf("invalid command record")
	}
	reply := errorReply(c.ID, unknownOutcome)
	if !bytes.Equal(data[:sha256.Size], digest[:]) {
		reply = errorReply(c.ID, "command ID reused with different payload")
	} else if len(data) > sha256.Size {
		var saved struct {
			Result json.RawMessage
			Error  *string
		}
		if err := json.Unmarshal(data[sha256.Size:], &saved); err != nil {
			return nil, err
		}
		reply = successReply(c.ID, saved.Result)
		if saved.Error != nil {
			reply = errorReply(c.ID, *saved.Error)
		}
	}
	return &reply, nil
}

// appends the wire reply to its reserved digest in one transaction
func (store *commandStore) finish(reply commandReply) error {
	data, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	key := sha256.Sum256([]byte(reply.ID))
	return store.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(commandBucket)
		digest := b.Get(key[:])
		if len(digest) != sha256.Size {
			return fmt.Errorf("missing command reservation")
		}
		return b.Put(key[:], append(bytes.Clone(digest), data...))
	})
}
