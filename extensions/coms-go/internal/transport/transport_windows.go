//go:build windows

// Package transport provides named-pipe stubs for Windows.
// Pi targets are Linux/macOS; this file exists for cross-compile cleanliness
// only and is not part of the acceptance criteria.
package transport

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
)

const (
	LineCap      = 64 * 1024
	ProbeTimeout = 0
)

// EnvelopeOrErr is the result type for ReadEnvelopes.
type EnvelopeOrErr struct {
	Data []byte
	Err  error
}

// WriteEnvelope is not implemented on Windows.
func WriteEnvelope(w io.Writer, v any) error {
	return fmt.Errorf("transport: WriteEnvelope not implemented on Windows")
}

// ReadEnvelopes is not implemented on Windows.
func ReadEnvelopes(r io.Reader) <-chan EnvelopeOrErr {
	ch := make(chan EnvelopeOrErr, 1)
	ch <- EnvelopeOrErr{Err: fmt.Errorf("transport: ReadEnvelopes not implemented on Windows")}
	close(ch)
	return ch
}

// SocketDir returns a named-pipe prefix path stub.
func SocketDir() string {
	return `\\.\pipe\`
}

// MakeEndpoint returns a named-pipe path stub.
func MakeEndpoint(sessionID string) string {
	return fmt.Sprintf(`\\.\pipe\pi-coms-%s`, sessionID)
}

// ProbeStaleSocket always returns "stale" on Windows (stub).
func ProbeStaleSocket(endpoint string) string {
	return "stale"
}

// BindEndpoint is not implemented on Windows.
func BindEndpoint(endpoint string) (net.Listener, error) {
	return nil, fmt.Errorf("transport: BindEndpoint not implemented on Windows")
}

// SendEnvelope is not implemented on Windows.
func SendEnvelope(endpoint string, v any) ([]byte, error) {
	return nil, fmt.Errorf("transport: SendEnvelope not implemented on Windows")
}

// Root is a no-op stub.
func Root() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".pi", "coms")
}
