package util

// AbbreviateModel shortens a model name for display, stripping the "claude-"
// prefix and capping at 14 characters. Mirrors abbreviateModel() in coms.ts
// line 204-209 and coms-net.ts line 244-249.
func AbbreviateModel(model string) string {
	m := model
	if len(m) > 7 && m[:7] == "claude-" {
		m = m[7:]
	}
	if len(m) > 14 {
		m = m[:14]
	}
	return m
}
