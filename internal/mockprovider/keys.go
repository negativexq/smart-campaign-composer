package mockprovider

import (
	"bytes"
	"fmt"
	"os"
)

// keyLog keeps every accepted idempotency key on disk, because a gateway that forgets its
// keys when it restarts would deliver a retried message a second time. Each line is
// "id<TAB>key". A header cannot hold a line break, and an id never holds a tab.
type keyLog struct {
	file *os.File
	size int64
}

// openKeyLog reads what earlier runs accepted. A crash can only leave the last line
// unfinished, and that key was never answered or acted on, so it is dropped.
func openKeyLog(path string) (*keyLog, map[string]string, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("open key log: %w", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("read key log: %w", err)
	}
	finished := bytes.LastIndexByte(raw, '\n') + 1
	if err := file.Truncate(int64(finished)); err != nil {
		_ = file.Close()
		return nil, nil, fmt.Errorf("repair key log: %w", err)
	}
	accepted := map[string]string{}
	for _, line := range bytes.Split(raw[:finished], []byte{'\n'}) {
		if id, key, found := bytes.Cut(line, []byte{'\t'}); found {
			accepted[string(key)] = string(id)
		}
	}
	return &keyLog{file: file, size: int64(finished)}, accepted, nil
}

// add returns only once the key is safely on disk. On failure the log is put back as it
// was, so a half-written line cannot run into the next one.
func (l *keyLog) add(key, id string) error {
	line := []byte(id + "\t" + key + "\n")
	if _, err := l.file.WriteAt(line, l.size); err != nil {
		_ = l.file.Truncate(l.size)
		return fmt.Errorf("write key log: %w", err)
	}
	if err := l.file.Sync(); err != nil {
		_ = l.file.Truncate(l.size)
		return fmt.Errorf("sync key log: %w", err)
	}
	l.size += int64(len(line))
	return nil
}
