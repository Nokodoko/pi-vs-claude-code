package util

import (
	"os"
	"strings"
)

// Frontmatter holds the values extracted from a YAML-like frontmatter block.
type Frontmatter struct {
	Name        string
	Description string
	Color       string
	Body        string
}

// ParseFrontmatter extracts the frontmatter block from raw markdown content.
// The format is:
//
//	---
//	key: value
//	---
//	body
//
// If no valid frontmatter is found, the entire input is returned as Body.
// Mirrors parseFrontmatter() in coms.ts lines 169-191 and coms-net.ts lines 217-238.
func ParseFrontmatter(raw string) Frontmatter {
	// Must start with ---\n
	if !strings.HasPrefix(raw, "---\n") {
		return Frontmatter{Body: raw}
	}
	// Find closing ---
	rest := raw[4:]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return Frontmatter{Body: raw}
	}
	header := rest[:end]
	body := rest[end+5:] // skip "\n---\n"

	fields := make(map[string]string)
	for _, line := range strings.Split(header, "\n") {
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+1:])
		// Strip surrounding quotes (same as TS)
		if len(val) >= 2 &&
			((val[0] == '"' && val[len(val)-1] == '"') ||
				(val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		fields[key] = val
	}

	return Frontmatter{
		Name:        fields["name"],
		Description: fields["description"],
		Color:       fields["color"],
		Body:        body,
	}
}

// FindSystemPromptPath scans argv for --system-prompt or --append-system-prompt
// followed by a .md file path that exists on disk.
// Mirrors findSystemPromptPath() in coms-net.ts lines 251-270.
func FindSystemPromptPath(argv []string) string {
	for _, flag := range []string{"--system-prompt", "--append-system-prompt"} {
		for i := 0; i < len(argv)-1; i++ {
			if argv[i] != flag {
				continue
			}
			candidate := argv[i+1]
			if !strings.HasSuffix(candidate, ".md") {
				continue
			}
			fi, err := os.Stat(candidate)
			if err == nil && fi.Mode().IsRegular() {
				return candidate
			}
		}
	}
	return ""
}

// ReadFrontmatterFromArgv reads the system-prompt file from argv and extracts
// frontmatter fields. Returns an empty Frontmatter if no file is found.
// Mirrors readFrontmatterFromArgv() in coms-net.ts lines 272-282.
func ReadFrontmatterFromArgv(argv []string) Frontmatter {
	p := FindSystemPromptPath(argv)
	if p == "" {
		return Frontmatter{}
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return Frontmatter{}
	}
	return ParseFrontmatter(string(raw))
}
