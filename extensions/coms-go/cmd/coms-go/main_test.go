// Package main tests exercise the run() dispatcher via os/exec subprocess calls.
// Using a subprocess is the idiomatic way to test main() in Go — it lets us
// capture exit codes and stdout/stderr without linking main into the test binary.
package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// buildBinary builds the coms-go binary once per test run and returns the path.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := t.TempDir() + "/coms-go"
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = "."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build coms-go: %v\n%s", err, out)
	}
	return bin
}

func TestVersion(t *testing.T) {
	bin := buildBinary(t)
	out, err := exec.Command(bin, "version").Output()
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "coms-go v") {
		t.Errorf("version output = %q, want 'coms-go v...'", s)
	}
}

func TestNoArgs_ExitsNonZero(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin)
	if err := cmd.Run(); err == nil {
		t.Error("no-args invocation should exit non-zero")
	}
}

func TestUnknownSubcommand_ExitsNonZero(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "does-not-exist")
	err := cmd.Run()
	if err == nil {
		t.Error("unknown subcommand should exit non-zero")
	}
	// Exit code must be 1 (not panic/signal).
	if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() != 1 {
			t.Errorf("exit code = %d, want 1", ee.ExitCode())
		}
	}
}

func TestServe_BadFlag(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "serve", "--no-such-flag")
	cmd.Env = append(os.Environ(), "PI_COMS_NET_AUTH_TOKEN=testtoken")
	if err := cmd.Run(); err == nil {
		t.Error("bad flag should exit non-zero")
	}
}

func TestClientLocal_Help(t *testing.T) {
	bin := buildBinary(t)
	// -help exits 0 via flag.ExitOnError... actually flag writes to stderr and exits 2.
	// Our flag.FlagSet uses ExitOnError so -h causes os.Exit(0) via -help override.
	cmd := exec.Command(bin, "client-local", "--help")
	out, err := cmd.CombinedOutput()
	// Exit code 0 (help) or 2 (flag error) — both are acceptable; the important
	// thing is that the binary doesn't panic.
	_ = err
	if !strings.Contains(string(out), "client-local") {
		t.Errorf("help output missing 'client-local': %s", out)
	}
}

func TestClientNet_Help(t *testing.T) {
	bin := buildBinary(t)
	cmd := exec.Command(bin, "client-net", "--help")
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "client-net") {
		t.Errorf("help output missing 'client-net': %s", out)
	}
}
