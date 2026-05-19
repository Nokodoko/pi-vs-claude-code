// Package registry manages the on-disk agent registry under
// ~/.pi/coms/projects/<project>/agents/<name>.json (local mode).
// All writes are atomic (temp-file-then-rename). Reads tolerate missing files.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// Root returns the base coms directory. Defaults to ~/.pi/coms but can be
// overridden via PI_COMS_DIR for testing.
func Root() string {
	if v := os.Getenv("PI_COMS_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join("/tmp", ".pi", "coms")
	}
	return filepath.Join(home, ".pi", "coms")
}

// projectAgentsDir returns the directory that holds agent JSON files for project.
func projectAgentsDir(project string) string {
	return filepath.Join(Root(), "projects", project, "agents")
}

// filePath returns the JSON file path for a given agent name in project.
func filePath(project, name string) string {
	return filepath.Join(projectAgentsDir(project), name+".json")
}

// Write atomically persists entry for project. The file is mode 0644.
// Mirrors writeRegistryAtomic() in coms.ts lines 248-256.
func Write(entry proto.RegistryEntry, project string) (string, error) {
	final := filePath(project, entry.Name)
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return "", fmt.Errorf("registry Write marshal: %w", err)
	}
	if err := util.AtomicWrite(final, data, 0); err != nil {
		return "", fmt.Errorf("registry Write: %w", err)
	}
	return final, nil
}

// Remove deletes the registry entry for name in project. Best-effort.
func Remove(project, name string) {
	_ = os.Remove(filePath(project, name))
}

// ReadAll returns all valid registry entries for project. Missing directory or
// malformed files are silently skipped — matches TS readAllRegistryEntries().
func ReadAll(project string) ([]proto.RegistryEntry, error) {
	dir := projectAgentsDir(project)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []proto.RegistryEntry
	for _, de := range entries {
		if de.IsDir() || filepath.Ext(de.Name()) != ".json" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			continue
		}
		var e proto.RegistryEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			continue
		}
		if e.SessionID == "" {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// Prune removes entries whose PID is no longer alive (ESRCH) and returns the
// surviving live entries. Entries where kill(pid, 0) returns EPERM are kept
// as live (process exists, we just can't signal it).
// Mirrors pruneDeadEntries() in coms.ts lines 312-329.
func Prune(project string) ([]proto.RegistryEntry, error) {
	all, err := ReadAll(project)
	if err != nil {
		return nil, err
	}
	var live []proto.RegistryEntry
	for _, e := range all {
		proc, err := os.FindProcess(e.Pid)
		if err != nil {
			// On Linux FindProcess never errors; on other platforms treat as live.
			live = append(live, e)
			continue
		}
		// Signal 0 checks liveness without sending a real signal.
		err = proc.Signal(syscall.Signal(0))
		if err == nil {
			// Process is alive.
			live = append(live, e)
			continue
		}
		if errors.Is(err, syscall.EPERM) {
			// Exists but we can't signal — treat as live.
			live = append(live, e)
			continue
		}
		if errors.Is(err, syscall.ESRCH) || errors.Is(err, os.ErrProcessDone) {
			// Process is dead — remove entry.
			Remove(project, e.Name)
			continue
		}
		// Unknown error — keep alive.
		live = append(live, e)
	}
	return live, nil
}

// ResolveUniqueName returns a name that does not collide with any live agent in
// project. If desiredName is free it is returned unchanged; otherwise it appends
// a numeric suffix (2, 3, ...).
// Mirrors resolveUniqueName() in coms.ts lines 331-340.
func ResolveUniqueName(project, desiredName string) (string, error) {
	live, err := Prune(project)
	if err != nil {
		return desiredName, err
	}
	liveNames := make(map[string]bool, len(live))
	for _, e := range live {
		liveNames[e.Name] = true
	}
	if !liveNames[desiredName] {
		return desiredName, nil
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s%d", desiredName, n)
		if !liveNames[candidate] {
			return candidate, nil
		}
	}
}
