package logstore

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/runportless/portless/portless-daemon/model"
)

const (
	maxLineBytes    = 1 << 20
	maxLogFileBytes = 16 << 20
	retainedRuns    = 10
)

// Sink converts one process stream into bounded structured JSON Lines log entries.
type Sink struct {
	mu         sync.Mutex
	file       *os.File
	service    string
	stream     string
	generation int64
	partial    []byte
	written    int64
	full       bool
}

// OpenSink opens a private bounded log sink and prunes old service generations.
func OpenSink(directory, service, stream string, generation int64) (*Sink, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return nil, err
	}
	if err := pruneGenerations(filepath.Dir(directory), retainedRuns); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(directory, stream+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &Sink{file: file, service: service, stream: stream, generation: generation, written: info.Size(), full: info.Size() >= maxLogFileBytes}, nil
}

// Write buffers partial input and persists complete bounded log lines.
func (s *Sink) Write(content []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = append(s.partial, content...)
	for {
		index := bytesIndexByte(s.partial, '\n')
		if index < 0 {
			if len(s.partial) > maxLineBytes {
				if err := s.writeLine(s.partial[:maxLineBytes]); err != nil {
					return 0, err
				}
				s.partial = s.partial[maxLineBytes:]
			}
			break
		}
		if err := s.writeLine(s.partial[:index]); err != nil {
			return 0, err
		}
		s.partial = s.partial[index+1:]
	}
	return len(content), nil
}

// Close flushes any partial line and closes the log file.
func (s *Sink) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.partial) > 0 {
		if err := s.writeLine(s.partial); err != nil {
			_ = s.file.Close()
			return err
		}
		s.partial = nil
	}
	return s.file.Close()
}

func (s *Sink) writeLine(content []byte) error {
	if s.full {
		return nil
	}
	entry := model.LogEntry{
		Timestamp: time.Now().UTC(), Service: s.service, Stream: s.stream,
		Generation: s.generation, Message: strings.TrimRight(string(content), "\r"),
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if s.written+int64(len(encoded)) > maxLogFileBytes {
		s.full = true
		return nil
	}
	written, err := s.file.Write(encoded)
	s.written += int64(written)
	return err
}

// Read merges retained service streams chronologically and applies time and count limits.
func Read(root string, services []string, limit int, since time.Time) ([]model.LogEntry, error) {
	if limit <= 0 {
		limit = 500
	}
	var entries []model.LogEntry
	for _, service := range services {
		serviceRoot := filepath.Join(root, service)
		generations, err := os.ReadDir(serviceRoot)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, generation := range generations {
			if !generation.IsDir() {
				continue
			}
			if _, err := strconv.ParseInt(generation.Name(), 10, 64); err != nil {
				continue
			}
			for _, stream := range []string{"stdout.jsonl", "stderr.jsonl", "container.jsonl"} {
				items, err := readFile(filepath.Join(serviceRoot, generation.Name(), stream), since)
				if err != nil {
					return nil, err
				}
				entries = append(entries, items...)
			}
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Timestamp.Before(entries[j].Timestamp) })
	if len(entries) > limit {
		entries = entries[len(entries)-limit:]
	}
	if entries == nil {
		entries = []model.LogEntry{}
	}
	return entries, nil
}

func readFile(path string, since time.Time) ([]model.LogEntry, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var result []model.LogEntry
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			var entry model.LogEntry
			if json.Unmarshal(line, &entry) == nil && (since.IsZero() || !entry.Timestamp.Before(since)) {
				result = append(result, entry)
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func bytesIndexByte(content []byte, target byte) int {
	for index, value := range content {
		if value == target {
			return index
		}
	}
	return -1
}

func pruneGenerations(serviceRoot string, retain int) error {
	entries, err := os.ReadDir(serviceRoot)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	type generationDirectory struct {
		name   string
		number int64
	}
	var generations []generationDirectory
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		number, parseErr := strconv.ParseInt(entry.Name(), 10, 64)
		if parseErr == nil {
			generations = append(generations, generationDirectory{name: entry.Name(), number: number})
		}
	}
	sort.Slice(generations, func(i, j int) bool { return generations[i].number < generations[j].number })
	for len(generations) > retain {
		if err := os.RemoveAll(filepath.Join(serviceRoot, generations[0].name)); err != nil {
			return err
		}
		generations = generations[1:]
	}
	return nil
}
