package netclient

// export_test.go — controlled, in-package test surface for the receiver-side
// handlers introduced by the coms_auto_await spec (T1, T1.5, regression-guard
// for §12 "Concurrent-senders regression"). All wrappers stay test-only.

import (
	"io"

	"github.com/pi-vs-cc/coms-go/internal/ipc"
)

// NewClientForTest constructs a Client wired with an ipc.Writer over the
// given output destination. The Client has no SSE loop, no HTTP, no
// registration — it is the minimal shell needed to exercise the inbound
// handlers in isolation.
func NewClientForTest(out io.Writer, project, sessionID, name string) *Client {
	cfg := Config{Project: project, SessionID: sessionID, Name: name}
	c := newClient(cfg)
	c.ipcWriter = ipc.NewWriter(out)
	c.identity = &identity{
		project:   project,
		sessionID: sessionID,
		name:      name,
	}
	return c
}

// CallHandleInboundPrompt invokes c.handleInboundPrompt directly. Used by
// TestInboundPromptEvent and the concurrent-senders regression test.
func (c *Client) CallHandleInboundPrompt(data map[string]any) {
	c.handleInboundPrompt(data)
}

// CallHandleLifecycle invokes c.handleLifecycle directly. Used by
// TestHandleLifecycle_LastText to verify the T1.5 gate at client.go line
// where onAgentEnd is skipped on empty last_text.
func (c *Client) CallHandleLifecycle(req ipc.Request) {
	c.handleLifecycle(req)
}

// InboundQueueLen returns the size of c.inboundQueue, the receiver-side map
// keyed by msg_id. Used to assert that two inbounds arriving within one
// agent_start window are both present (regression for the last-wins bug).
func (c *Client) InboundQueueLen() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.inboundQueue)
}
