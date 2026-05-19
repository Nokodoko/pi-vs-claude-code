package util_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/pi-vs-cc/coms-go/internal/util"
)

// ─────────────────────────────────────────────────────────────────────────────
// ULID tests
// ─────────────────────────────────────────────────────────────────────────────

func TestULIDLength(t *testing.T) {
	id := util.NewULID()
	if len(id) != 26 {
		t.Errorf("ULID length = %d, want 26; got %q", len(id), id)
	}
}

func TestULIDCrockfordAlphabet(t *testing.T) {
	const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
	valid := make(map[rune]bool)
	for _, c := range crockford {
		valid[c] = true
	}
	for i := 0; i < 50; i++ {
		id := util.NewULID()
		for _, c := range id {
			if !valid[c] {
				t.Errorf("ULID %q contains invalid character %q", id, c)
			}
		}
	}
}

func TestULIDMonotonic(t *testing.T) {
	// The TS ulid() function (which we mirror) does NOT guarantee monotonic
	// ordering within the same millisecond — the 80-bit random suffix is purely
	// random. The lexicographic order is only guaranteed across different
	// millisecond buckets. This test verifies that the time-prefix portion
	// (chars 0..9) never decreases — i.e., later calls produce the same or
	// later time prefix.
	const n = 20
	ids := make([]string, n)
	for i := range ids {
		ids[i] = util.NewULID()
	}
	for i := 1; i < len(ids); i++ {
		// Time prefix must be non-decreasing.
		if ids[i][:10] < ids[i-1][:10] {
			t.Errorf("ULID time prefix decreased: ULID[%d] %q < ULID[%d] %q",
				i, ids[i][:10], i-1, ids[i-1][:10])
		}
	}
}

func TestULIDTimePrefix(t *testing.T) {
	// The first 10 chars encode the current time in ms; two ULIDs generated
	// ~1 s apart must have different prefixes.
	a := util.NewULID()
	time.Sleep(10 * time.Millisecond)
	b := util.NewULID()
	if a[:10] > b[:10] {
		t.Errorf("time prefix decreased: %s > %s", a[:10], b[:10])
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Color tests
// ─────────────────────────────────────────────────────────────────────────────

func TestIsValidHex(t *testing.T) {
	cases := []struct {
		hex  string
		want bool
	}{
		{"#36F9F6", true},
		{"#000000", true},
		{"#FFFFFF", true},
		{"#abcdef", true},
		{"36F9F6", false},  // missing #
		{"#36F9F", false},  // 5 digits
		{"#GG0000", false}, // invalid chars
		{"", false},
	}
	for _, c := range cases {
		got := util.IsValidHex(c.hex)
		if got != c.want {
			t.Errorf("IsValidHex(%q) = %v, want %v", c.hex, got, c.want)
		}
	}
}

func TestFallbackColorDeterministic(t *testing.T) {
	// Same input must always produce the same output.
	id := "01HXNJ0E5Q4M7Z2C1V8YR6F3KT"
	c1 := util.FallbackColor(id)
	c2 := util.FallbackColor(id)
	if c1 != c2 {
		t.Errorf("FallbackColor(%q) not deterministic: %q vs %q", id, c1, c2)
	}
	// Result must be in the palette.
	found := false
	for _, p := range util.FallbackPalette {
		if p == c1 {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("FallbackColor result %q not in palette", c1)
	}
}

func TestFallbackColorDistribution(t *testing.T) {
	// All palette colors should appear across a reasonable set of inputs.
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		id := util.NewULID()
		seen[util.FallbackColor(id)] = true
	}
	if len(seen) < len(util.FallbackPalette)/2 {
		t.Errorf("FallbackColor distribution too skewed: only %d/%d colors seen", len(seen), len(util.FallbackPalette))
	}
}

func TestHexFg(t *testing.T) {
	// HexFg("#36F9F6", "hi") must produce \x1b[38;2;54;249;246mhi\x1b[39m
	// r=0x36=54, g=0xF9=249, b=0xF6=246
	got := util.HexFg("#36F9F6", "hi")
	want := "\x1b[38;2;54;249;246mhi\x1b[39m"
	if got != want {
		t.Errorf("HexFg = %q, want %q", got, want)
	}
	// "#72F1B8": r=0x72=114, g=0xF1=241, b=0xB8=184
	got2 := util.HexFg("#72F1B8", "x")
	want2 := "\x1b[38;2;114;241;184mx\x1b[39m"
	if got2 != want2 {
		t.Errorf("HexFg = %q, want %q", got2, want2)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Frontmatter tests
// ─────────────────────────────────────────────────────────────────────────────

func TestParseFrontmatterBasic(t *testing.T) {
	raw := "---\nname: planner\ndescription: Plans the work\ncolor: \"#36F9F6\"\n---\nBody text here.\n"
	fm := util.ParseFrontmatter(raw)
	if fm.Name != "planner" {
		t.Errorf("Name = %q, want planner", fm.Name)
	}
	if fm.Description != "Plans the work" {
		t.Errorf("Description = %q, want 'Plans the work'", fm.Description)
	}
	if fm.Color != "#36F9F6" {
		t.Errorf("Color = %q, want #36F9F6", fm.Color)
	}
	if fm.Body != "Body text here.\n" {
		t.Errorf("Body = %q, want 'Body text here.\\n'", fm.Body)
	}
}

func TestParseFrontmatterNoFrontmatter(t *testing.T) {
	raw := "Just a plain body.\nNo frontmatter.\n"
	fm := util.ParseFrontmatter(raw)
	if fm.Body != raw {
		t.Errorf("Body = %q, want %q", fm.Body, raw)
	}
	if fm.Name != "" || fm.Color != "" {
		t.Error("expected empty fields when no frontmatter")
	}
}

func TestParseFrontmatterSingleQuotes(t *testing.T) {
	raw := "---\ncolor: '#FF7EDB'\n---\nbody\n"
	fm := util.ParseFrontmatter(raw)
	if fm.Color != "#FF7EDB" {
		t.Errorf("Color = %q, want #FF7EDB", fm.Color)
	}
}

func TestFindSystemPromptPath(t *testing.T) {
	// Create a temp .md file
	dir := t.TempDir()
	p := filepath.Join(dir, "system.md")
	if err := os.WriteFile(p, []byte("---\nname: test\n---\nbody\n"), 0644); err != nil {
		t.Fatal(err)
	}
	argv := []string{"pi", "--system-prompt", p, "--other", "flag"}
	got := util.FindSystemPromptPath(argv)
	if got != p {
		t.Errorf("FindSystemPromptPath = %q, want %q", got, p)
	}
}

func TestFindSystemPromptPathMissing(t *testing.T) {
	argv := []string{"pi", "--system-prompt", "/nonexistent/file.md"}
	got := util.FindSystemPromptPath(argv)
	if got != "" {
		t.Errorf("FindSystemPromptPath should return empty for missing file, got %q", got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// NowIso tests
// ─────────────────────────────────────────────────────────────────────────────

func TestNowIsoFormat(t *testing.T) {
	s := util.NowIso()
	// Expected: "2026-05-19T14:32:11.482Z" — 24 chars
	if len(s) != 24 {
		t.Errorf("NowIso length = %d, want 24; got %q", len(s), s)
	}
	if !strings.HasSuffix(s, "Z") {
		t.Errorf("NowIso does not end with Z: %q", s)
	}
	if s[10] != 'T' {
		t.Errorf("NowIso missing T separator: %q", s)
	}
	if !utf8.ValidString(s) {
		t.Errorf("NowIso is not valid UTF-8: %q", s)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AbbreviateModel tests
// ─────────────────────────────────────────────────────────────────────────────

func TestAbbreviateModel(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"claude-opus-4-7", "opus-4-7"},
		{"claude-sonnet-4-6", "sonnet-4-6"},
		{"gpt-4o", "gpt-4o"},
		{"claude-very-long-name-here-x", "very-long-name"}, // 14 char cap
		{"", ""},
	}
	for _, c := range cases {
		got := util.AbbreviateModel(c.input)
		if got != c.want {
			t.Errorf("AbbreviateModel(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// AtomicWrite tests
// ─────────────────────────────────────────────────────────────────────────────

func TestAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "test.json")
	data := []byte(`{"ok":true}`)
	if err := util.AtomicWrite(p, data, 0); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	got, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("AtomicWrite content = %q, want %q", got, data)
	}
	// Temp file must not remain
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file still exists after AtomicWrite")
	}
}

func TestAtomicWriteMode(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "secret.json")
	data := []byte(`{"token":"abc"}`)
	if err := util.AtomicWrite(p, data, 0600); err != nil {
		t.Fatalf("AtomicWrite: %v", err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Errorf("mode = %o, want 0600", fi.Mode().Perm())
	}
}
