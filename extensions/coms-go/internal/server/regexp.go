package server

import "regexp"

// Compiled regexps for route matching.
var (
	compiledAgentRe = regexp.MustCompile(`^/v1/agents/([^/]+)(?:/(heartbeat))?$`)
	compiledMsgRe   = regexp.MustCompile(`^/v1/messages/([^/]+)(?:/(await|response))?$`)
)
