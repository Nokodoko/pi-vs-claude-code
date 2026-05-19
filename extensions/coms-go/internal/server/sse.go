package server

import (
	"encoding/json"
	"fmt"
)

// sseFrameWithID builds an SSE frame matching the TS sseFrame() function verbatim.
//
// Format:
//
//	event: <name>
//	id: <n>
//	data: <json>
//	\n
func sseFrameWithID(event string, data any, id int) string {
	dataBytes, _ := json.Marshal(data)
	return fmt.Sprintf("event: %s\nid: %d\ndata: %s\n\n", event, id, dataBytes)
}

// ssePingFrame returns a SSE keepalive comment line (no event, no id).
// Matches the TS keepaliveTick: `: ping <iso>\n\n`.
func ssePingFrame(ts string) string {
	return fmt.Sprintf(": ping %s\n\n", ts)
}
