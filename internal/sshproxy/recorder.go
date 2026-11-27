package sshproxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultCastWidth        = 100
	defaultCastHeight       = 26
	castScanInitBuf         = 64 * 1024
	castScanMaxBuf          = 4 * 1024 * 1024
	recorderShrinkThreshold = 32 * 1024
	jsonHexDigits           = "0123456789abcdef"
)

// CastHeader holds the asciinema v2 header metadata.
type CastHeader struct {
	Version   int               `json:"version"`
	Width     int               `json:"width"`
	Height    int               `json:"height"`
	Timestamp int64             `json:"timestamp"`
	Title     string            `json:"title,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// CastEvent represents a single timestamped event in an asciinema v2 recording.
type CastEvent struct {
	Time float64
	Kind string
	Data string
}

// CastRecorder writes terminal output in asciinema v2 (.cast) format.
// Only PTY output ("o") is recorded so passwords typed with echo disabled are never stored.
type CastRecorder struct {
	mu        sync.Mutex
	file      *os.File
	writer    *bufio.Writer
	lineBuf   bytes.Buffer
	startTime time.Time
	closed    bool
	filePath  string
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
	hdr := CastHeader{
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
	if elapsed < 0 || math.IsNaN(elapsed) || math.IsInf(elapsed, 0) {
		elapsed = 0
	}

	r.lineBuf.Reset()
	r.lineBuf.WriteByte('[')
	var numBuf [32]byte
	r.lineBuf.Write(strconv.AppendFloat(numBuf[:0], elapsed, 'f', 6, 64))
	r.lineBuf.WriteString(`,"o",`)
	appendJSONString(&r.lineBuf, data)
	r.lineBuf.WriteString("]\n")

	_, _ = r.writer.Write(r.lineBuf.Bytes())
	if r.lineBuf.Cap() > recorderShrinkThreshold {
		r.lineBuf = bytes.Buffer{}
	}
}

func appendJSONString(buf *bytes.Buffer, data []byte) {
	buf.WriteByte('"')
	start := 0
	for i := 0; i < len(data); {
		if b := data[i]; b < utf8.RuneSelf {
			if b >= 0x20 && b != '"' && b != '\\' {
				i++
				continue
			}
			if start < i {
				buf.Write(data[start:i])
			}
			switch b {
			case '"', '\\':
				buf.WriteByte('\\')
				buf.WriteByte(b)
			case '\n':
				buf.WriteString(`\n`)
			case '\r':
				buf.WriteString(`\r`)
			case '\t':
				buf.WriteString(`\t`)
			default:
				buf.WriteString(`\u00`)
				buf.WriteByte(jsonHexDigits[b>>4])
				buf.WriteByte(jsonHexDigits[b&0x0f])
			}
			i++
			start = i
			continue
		}
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			if start < i {
				buf.Write(data[start:i])
			}
			buf.WriteString(`\ufffd`)
			i++
			start = i
			continue
		}
		if r == '\u2028' || r == '\u2029' {
			if start < i {
				buf.Write(data[start:i])
			}
			buf.WriteString(`\u202`)
			buf.WriteByte(jsonHexDigits[r&0x0f])
			i += size
			start = i
			continue
		}
		i += size
	}
	if start < len(data) {
		buf.Write(data[start:])
	}
	buf.WriteByte('"')
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
	r.lineBuf = bytes.Buffer{}
	_ = r.writer.Flush()
	info, _ := r.file.Stat()
	var size int64
	if info != nil {
		size = info.Size()
	}
	err := r.file.Close()
	return size, err
}

// CastReader streams an asciinema v2 (.cast) recording line by line without
// decoding the full file into memory at once.
type CastReader struct {
	closer  io.Closer
	scanner *bufio.Scanner
	header  CastHeader
}

// OpenCastReader opens a .cast file on disk and parses its asciinema v2 header.
func OpenCastReader(path string) (*CastReader, error) {
	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	cr, err := NewCastReader(f)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	cr.closer = f
	return cr, nil
}

// NewCastReader constructs a streaming CastReader from any io.Reader and parses
// the first non-empty line as the asciinema v2 header.
func NewCastReader(r io.Reader) (*CastReader, error) {
	if r == nil {
		return nil, errors.New("nil cast reader")
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, castScanInitBuf), castScanMaxBuf)

	var hdr CastHeader
	foundHeader := false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if err := json.Unmarshal([]byte(line), &hdr); err != nil {
			return nil, fmt.Errorf("parse cast header: %w", err)
		}
		foundHeader = true
		break
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read cast header: %w", err)
	}
	if !foundHeader {
		return nil, errors.New("empty cast recording")
	}
	if hdr.Width <= 0 {
		hdr.Width = defaultCastWidth
	}
	if hdr.Height <= 0 {
		hdr.Height = defaultCastHeight
	}
	return &CastReader{
		scanner: sc,
		header:  hdr,
	}, nil
}

// Header returns the parsed asciinema v2 header metadata.
func (r *CastReader) Header() CastHeader {
	return r.header
}

// Next returns the next output ("o") event from the recording stream, or io.EOF.
func (r *CastReader) Next() (CastEvent, error) {
	for r.scanner.Scan() {
		line := strings.TrimSpace(r.scanner.Text())
		if line == "" {
			continue
		}
		var raw []json.RawMessage
		if err := json.Unmarshal([]byte(line), &raw); err != nil || len(raw) < 3 {
			continue
		}
		var t float64
		var kind, data string
		if err := json.Unmarshal(raw[0], &t); err != nil {
			continue
		}
		if err := json.Unmarshal(raw[1], &kind); err != nil {
			continue
		}
		if err := json.Unmarshal(raw[2], &data); err != nil {
			continue
		}
		if kind != "o" {
			continue
		}
		return CastEvent{Time: t, Kind: kind, Data: data}, nil
	}
	if err := r.scanner.Err(); err != nil {
		return CastEvent{}, err
	}
	return CastEvent{}, io.EOF
}

// Close releases the underlying file if opened via OpenCastReader.
func (r *CastReader) Close() error {
	if r.closer != nil {
		return r.closer.Close()
	}
	return nil
}
