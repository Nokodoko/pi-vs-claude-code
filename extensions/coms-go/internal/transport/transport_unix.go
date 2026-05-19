//go:build !windows

// Package transport provides Unix-socket frame I/O for coms-go client-local.
// Each frame is a single JSON line; lines are capped at 64 KB (LINE_CAP_BYTES)
// matching coms.ts LINE_CAP_BYTES = 64 * 1024.
package transport

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	// LineCap is the maximum bytes allowed in a single transport line (64 KB).
	LineCap = 64 * 1024
	// ProbeTimeout is how long probeStaleSocket waits for a TCP connect reply.
	ProbeTimeout = 250 * time.Millisecond
)

// EnvelopeOrErr is the result type sent on the channel returned by ReadEnvelopes.
type EnvelopeOrErr struct {
	Data []byte
	Err  error
}

// WriteEnvelope serializes v as a single JSON line to w.
// The line is terminated by '\n'. The total line must not exceed LineCap.
func WriteEnvelope(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("WriteEnvelope marshal: %w", err)
	}
	if len(data)+1 > LineCap {
		return fmt.Errorf("WriteEnvelope: envelope too large (%d bytes, cap %d)", len(data), LineCap)
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("WriteEnvelope write: %w", err)
	}
	return nil
}

// ReadEnvelopes returns a channel of raw JSON byte slices (one per line) read
// from r. The channel is closed when r returns EOF or an error; errors are
// delivered as EnvelopeOrErr.Err. Lines exceeding LineCap are delivered as an
// error and the read loop stops.
func ReadEnvelopes(r io.Reader) <-chan EnvelopeOrErr {
	ch := make(chan EnvelopeOrErr, 8)
	go func() {
		defer close(ch)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, LineCap), LineCap)
		for sc.Scan() {
			line := sc.Bytes()
			cp := make([]byte, len(line))
			copy(cp, line)
			ch <- EnvelopeOrErr{Data: cp}
		}
		if err := sc.Err(); err != nil {
			if !errors.Is(err, io.EOF) {
				ch <- EnvelopeOrErr{Err: err}
			}
		}
	}()
	return ch
}

// SocketDir returns the directory where Unix socket files are stored.
func SocketDir() string {
	root := os.Getenv("PI_COMS_DIR")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join("/tmp", ".pi", "coms", "sockets")
		}
		root = filepath.Join(home, ".pi", "coms")
	}
	return filepath.Join(root, "sockets")
}

// MakeEndpoint builds the socket path for a given session ID.
// Mirrors makeEndpoint() in coms.ts lines 193-198.
func MakeEndpoint(sessionID string) string {
	return filepath.Join(SocketDir(), sessionID+".sock")
}

// ProbeStaleSocket returns "in_use" if a server is actively listening on
// endpoint, or "stale" if the socket file exists but nobody answers.
// Mirrors probeStaleSocket() in coms.ts lines 373-398.
func ProbeStaleSocket(endpoint string) string {
	conn, err := net.DialTimeout("unix", endpoint, ProbeTimeout)
	if err != nil {
		return "stale"
	}
	conn.Close()
	return "in_use"
}

// BindEndpoint creates and binds a Unix-domain socket server at endpoint.
// If the socket file already exists it is probed; if stale it is removed before
// binding. Returns the net.Listener ready to Accept.
// Mirrors bindEndpoint() in coms.ts lines 400-423.
func BindEndpoint(endpoint string) (net.Listener, error) {
	if _, err := os.Stat(endpoint); err == nil {
		// File exists — probe it.
		verdict := ProbeStaleSocket(endpoint)
		if verdict == "in_use" {
			return nil, fmt.Errorf("transport: endpoint already in use (%s)", endpoint)
		}
		_ = os.Remove(endpoint)
	}
	if err := os.MkdirAll(filepath.Dir(endpoint), 0755); err != nil {
		return nil, fmt.Errorf("transport: mkdir: %w", err)
	}
	l, err := net.Listen("unix", endpoint)
	if err != nil {
		return nil, fmt.Errorf("transport: listen: %w", err)
	}
	return l, nil
}

// SendEnvelope dials endpoint, writes v as a JSON line, reads one JSON line
// response, and returns the raw response bytes.
// Mirrors sendEnvelope() in coms.ts lines 460-489.
func SendEnvelope(endpoint string, v any) ([]byte, error) {
	conn, err := net.DialTimeout("unix", endpoint, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("transport: dial %s: %w", endpoint, err)
	}
	defer conn.Close()

	if err := WriteEnvelope(conn, v); err != nil {
		return nil, err
	}

	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, LineCap), LineCap)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("transport: read response: %w", err)
		}
		return nil, fmt.Errorf("transport: connection closed before response")
	}
	line := sc.Bytes()
	cp := make([]byte, len(line))
	copy(cp, line)
	return cp, nil
}
