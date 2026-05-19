package server

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	"github.com/pi-vs-cc/coms-go/internal/util"
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

// writeServerSecret writes {token: tok} to path with mode 0600.
// Uses util.AtomicWrite (temp-then-rename) as the canonical implementation.
func writeServerSecret(path, tok string) error {
	data, err := json.MarshalIndent(map[string]string{"token": tok}, "", "  ")
	if err != nil {
		return err
	}
	if err := util.AtomicWrite(path, data, 0600); err != nil {
		return err
	}
	// Belt-and-suspenders chmod, matching the TS server's post-rename chmod.
	_ = os.Chmod(path, 0600)
	return nil
}
