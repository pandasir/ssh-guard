package sshproxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CastRecorder writes terminal output in asciinema v2 (.cast) format.
// Only PTY output ("o") is recorded so passwords typed with echo disabled are never stored.
type CastRecorder struct {
	mu        sync.Mutex
	file      *os.File
	writer    *bufio.Writer
	startTime time.Time
	closed    bool
	filePath  string
}

type castHeader struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Title     string            `json:"title,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

func NewCastRecorder(dir, sessionID, title string, cols, rows int) (*CastRecorder, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create recordings dir: %w", err)
	}
	fp := filepath.Join(dir, sessionID+".cast")
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create cast file: %w", err)
	}

	now := time.Now()
	hdr := castHeader{
		Version:   2,
		Width:     cols,
		Height:    rows,
		Timestamp: now.Unix(),
		Title:     title,
		Env:       map[string]string{"TERM": "xterm-256color"},
	}
	hdrBytes, err := json.Marshal(hdr)
	if err != nil {
		_ = f.Close()
		return nil, err
	}

	bw := bufio.NewWriter(f)
	if _, err := bw.Write(append(hdrBytes, '\n')); err != nil {
		_ = f.Close()
		return nil, err
	}
	_ = bw.Flush()

	return &CastRecorder{
		file:      f,
		writer:    bw,
		startTime: now,
		filePath:  fp,
	}, nil
}

func (r *CastRecorder) RecordOutput(data []byte) {
	if len(data) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return
	}
	elapsed := time.Since(r.startTime).Seconds()
	event := []any{elapsed, "o", string(data)}
	line, err := json.Marshal(event)
	if err != nil {
		return
	}
	_, _ = r.writer.Write(append(line, '\n'))
}

func (r *CastRecorder) FilePath() string {
	return r.filePath
}

func (r *CastRecorder) Close() (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return 0, nil
	}
	r.closed = true
	_ = r.writer.Flush()
	info, _ := r.file.Stat()
	var size int64
	if info != nil {
		size = info.Size()
	}
	err := r.file.Close()
	return size, err
}
