package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// generateToken returns 32 random bytes encoded as hex, matching the TS
// crypto.randomBytes(32).toString("hex") output.
func generateToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// atomicWrite writes content to path using a temp-then-rename pattern, matching
// the TS atomicWriteSync() function. If mode != 0, chmod is applied to the temp
// file before the rename.
func atomicWrite(path string, content []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, content, 0644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if mode != 0 {
		_ = os.Chmod(tmp, mode) // best-effort, matching TS
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename to %s: %w", path, err)
	}
	return nil
}

// writeServerSecret writes {token: tok} to path with mode 0600.
func writeServerSecret(path, tok string) error {
	data, err := json.MarshalIndent(map[string]string{"token": tok}, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWrite(path, data, 0600); err != nil {
		return err
	}
	// Belt-and-suspenders chmod, matching the TS server's post-rename chmod.
	_ = os.Chmod(path, 0600)
	return nil
}
