package registry_test

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/registry"
)

func setRoot(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PI_COMS_DIR", dir)
	return dir
}

func makeEntry(name string) proto.RegistryEntry {
	pid := os.Getpid() // use our own PID so it's alive
	return proto.RegistryEntry{
		SessionID: "01SESS" + name,
		Name:      name,
		Purpose:   "test",
		Model:     "test-model",
		Color:     "#72F1B8",
		Pid:       pid,
		Endpoint:  "/tmp/" + name + ".sock",
		Cwd:       "/tmp",
		StartedAt: "2026-05-19T00:00:00.000Z",
		Explicit:  false,
		Version:   1,
	}
}

func TestWriteAndReadAll(t *testing.T) {
	setRoot(t)
	project := "test-project"
	entries := []proto.RegistryEntry{makeEntry("agent1"), makeEntry("agent2")}
	for _, e := range entries {
		if _, err := registry.Write(e, project); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	got, err := registry.ReadAll(project)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if len(got) != len(entries) {
		t.Errorf("ReadAll count = %d, want %d", len(got), len(entries))
	}
}

func TestReadAllMissingDir(t *testing.T) {
	setRoot(t)
	got, err := registry.ReadAll("nonexistent")
	if err != nil {
		t.Fatalf("ReadAll on missing dir should not error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ReadAll on missing dir = %d entries, want 0", len(got))
	}
}

func TestAtomicWriteRoundTrip(t *testing.T) {
	setRoot(t)
	e := makeEntry("myagent")
	path, err := registry.Write(e, "default")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	// Temp file must not linger
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file still exists")
	}
	// Content must be valid JSON with correct session_id
	all, _ := registry.ReadAll("default")
	if len(all) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(all))
	}
	if all[0].SessionID != e.SessionID {
		t.Errorf("SessionID = %q, want %q", all[0].SessionID, e.SessionID)
	}
}

func TestPruneRemovesDeadPID(t *testing.T) {
	setRoot(t)
	// Write an entry with a PID that definitely doesn't exist.
	dead := makeEntry("dead")
	dead.Pid = 999999999 // no such PID
	if _, err := registry.Write(dead, "default"); err != nil {
		t.Fatal(err)
	}
	live := makeEntry("live")
	if _, err := registry.Write(live, "default"); err != nil {
		t.Fatal(err)
	}

	survivors, err := registry.Prune("default")
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	// "live" (our PID) should survive; "dead" should be pruned.
	for _, s := range survivors {
		if s.Name == "dead" {
			t.Error("Prune kept dead-PID entry")
		}
	}
	found := false
	for _, s := range survivors {
		if s.Name == "live" {
			found = true
		}
	}
	if !found {
		t.Error("Prune removed live entry")
	}
}

func TestResolveUniqueName(t *testing.T) {
	setRoot(t)
	// No entries yet — desired name should be returned unchanged.
	got, err := registry.ResolveUniqueName("default", "planner")
	if err != nil {
		t.Fatal(err)
	}
	if got != "planner" {
		t.Errorf("ResolveUniqueName = %q, want planner", got)
	}

	// Register an agent named "planner" with our PID.
	e := makeEntry("planner")
	if _, err := registry.Write(e, "default"); err != nil {
		t.Fatal(err)
	}

	// Now resolving "planner" should return "planner2".
	got2, err := registry.ResolveUniqueName("default", "planner")
	if err != nil {
		t.Fatal(err)
	}
	if got2 != "planner2" {
		t.Errorf("ResolveUniqueName (collision) = %q, want planner2", got2)
	}
}

func TestResolveUniqueNameMultipleCollisions(t *testing.T) {
	setRoot(t)
	// Register planner, planner2, planner3 — next should be planner4.
	for _, name := range []string{"planner", "planner2", "planner3"} {
		e := makeEntry(name)
		e.SessionID = "01SESS" + name
		if _, err := registry.Write(e, "default"); err != nil {
			t.Fatal(err)
		}
	}
	got, err := registry.ResolveUniqueName("default", "planner")
	if err != nil {
		t.Fatal(err)
	}
	n, _ := strconv.Atoi(got[len("planner"):])
	if n < 4 {
		t.Errorf("ResolveUniqueName = %q, want planner4+", got)
	}
}

func TestRemove(t *testing.T) {
	setRoot(t)
	e := makeEntry("rmtest")
	if _, err := registry.Write(e, "default"); err != nil {
		t.Fatal(err)
	}
	registry.Remove("default", "rmtest")
	all, _ := registry.ReadAll("default")
	for _, a := range all {
		if a.Name == "rmtest" {
			t.Error("Remove did not delete entry")
		}
	}
}

func TestSkipsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_COMS_DIR", dir)
	agentsDir := filepath.Join(dir, "projects", "default", "agents")
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Write a malformed JSON file
	if err := os.WriteFile(filepath.Join(agentsDir, "bad.json"), []byte("{bad"), 0644); err != nil {
		t.Fatal(err)
	}
	// Write a valid one
	e := makeEntry("good")
	if _, err := registry.Write(e, "default"); err != nil {
		t.Fatal(err)
	}
	all, err := registry.ReadAll("default")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "good" {
		t.Errorf("expected 1 valid entry, got %d", len(all))
	}
}
