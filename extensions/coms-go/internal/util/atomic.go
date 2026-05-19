package util

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWrite writes data to path atomically using temp-file-then-rename.
// If mode is non-zero it is applied to the temp file before rename (e.g. 0600
// for server.secret.json). Mirrors atomicWriteSync() in coms-net-server.ts
// lines 342-355.
func AtomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("AtomicWrite mkdir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return fmt.Errorf("AtomicWrite write: %w", err)
	}
	if mode != 0 {
		if err := os.Chmod(tmp, mode); err != nil {
			// best-effort, mirrors TS behavior
			_ = err
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("AtomicWrite rename: %w", err)
	}
	return nil
}
