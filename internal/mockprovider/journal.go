package mockprovider

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// minCompactSize is how large the journal grows before it is first rewritten.
const minCompactSize = 64 << 20

const (
	recordAccepted = "accepted"
	recordRepeated = "repeated"
	recordOpened   = "opened"
	recordReported = "reported"
	recordCleared  = "cleared"
	recordDropped  = "dropped"
)

// record is one line of the journal. A single line is what makes accepting a message
// all or nothing: its key, its inbox entry and its unsent report are saved together.
type record struct {
	Kind    string        `json:"kind"`
	Key     string        `json:"key,omitempty"`
	ID      string        `json:"id,omitempty"`
	Message *InboxMessage `json:"message,omitempty"`
	// Reports are events still owed to the sender. An event's OccurredAt is also when it
	// becomes due, so a report restored after a restart keeps its original time.
	Reports []Event    `json:"reports,omitempty"`
	At      *time.Time `json:"at,omitempty"`
	Event   string     `json:"event,omitempty"`
	Count   int        `json:"count,omitempty"`
}

// journal is the provider's write-ahead log: one JSON record per line, replayed on start.
// It is rewritten down to what is still needed when it has grown, so a long run does not
// keep every message body on disk.
type journal struct {
	path      string
	file      *os.File
	size      int64
	compactAt int64
}

// openJournal reads what earlier runs recorded. A crash can only leave the last line
// unfinished, and that line was never answered or acted on, so it is dropped. Damage
// anywhere else is reported, since starting anyway would forget keys without saying so.
func openJournal(path string) (*journal, []record, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open journal: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("read journal: %w", err)
	}
	finished := bytes.LastIndexByte(raw, '\n') + 1
	var records []record
	for number, line := range bytes.Split(raw[:finished], []byte{'\n'}) {
		if len(line) == 0 {
			continue
		}
		var entry record
		if err := json.Unmarshal(line, &entry); err != nil {
			_ = file.Close()
			return nil, nil, fmt.Errorf("journal line %d: %w", number+1, err)
		}
		records = append(records, entry)
	}
	return &journal{path: path, file: file, size: int64(finished), compactAt: minCompactSize}, records, nil
}

// add writes a record. With durable set it returns only once the record is safely on
// disk. On failure the file is put back as it was, so a half-written line cannot run
// into the next one.
func (j *journal) add(entry record, durable bool) error {
	line, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("encode journal record: %w", err)
	}
	line = append(line, '\n')
	if _, err := j.file.WriteAt(line, j.size); err != nil {
		_ = j.file.Truncate(j.size)
		return fmt.Errorf("write journal: %w", err)
	}
	if durable {
		if err := j.file.Sync(); err != nil {
			_ = j.file.Truncate(j.size)
			return fmt.Errorf("sync journal: %w", err)
		}
	}
	j.size += int64(len(line))
	return nil
}

// replace swaps the journal for one holding only records. The rename means a crash at
// any point leaves either the whole old file or the whole new one.
func (j *journal) replace(records []record) error {
	temporary := j.path + ".tmp"
	next, err := os.OpenFile(temporary, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create journal copy: %w", err)
	}
	size, err := writeRecords(next, records)
	if err == nil {
		err = os.Rename(temporary, j.path)
	}
	if err == nil {
		err = syncDirectory(filepath.Dir(j.path))
	}
	if err != nil {
		_ = next.Close()
		_ = os.Remove(temporary)
		return err
	}
	_ = j.file.Close()
	j.file, j.size = next, size
	j.compactAt = max(2*size, minCompactSize)
	return nil
}

func writeRecords(file *os.File, records []record) (int64, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	for _, entry := range records {
		if err := encoder.Encode(entry); err != nil {
			return 0, fmt.Errorf("encode journal record: %w", err)
		}
	}
	if _, err := file.Write(buffer.Bytes()); err != nil {
		return 0, fmt.Errorf("write journal copy: %w", err)
	}
	if err := file.Sync(); err != nil {
		return 0, fmt.Errorf("sync journal copy: %w", err)
	}
	return int64(buffer.Len()), nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open journal directory: %w", err)
	}
	return errors.Join(directory.Sync(), directory.Close())
}
