// Package netclient — ask.go: broadcast collect-with-deadline for coms_net_ask.
//
// T6: implements the no-target path of coms_net_ask. Fans a single prompt to
// every peer in the project, registers per-peer pendingReply entries, then
// waits on all of them concurrently under one shared deadline. Returns the
// bag of responses received before the deadline; zero responses is NOT an
// error — the model gets an empty bag.
//
// Spec: SPEC/coms_auto_await/coms_auto_await.md §6.

package netclient

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/ipc"
	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// ─────────────────────────────────────────────────────────────────────────────
// Types (spec §6.3)
// ─────────────────────────────────────────────────────────────────────────────

// BroadcastResponse is the details bag returned by a broadcast coms_net_ask.
type BroadcastResponse struct {
	Broadcast  bool           `json:"broadcast"`
	TimeoutMs  int            `json:"timeout_ms"`
	TotalPeers int            `json:"total_peers"`
	Responded  int            `json:"responded"`
	TimedOut   int            `json:"timed_out"`
	Responses  []PeerResponse `json:"responses"`
	NoResponse []PeerIdentity `json:"no_response"`
}

// PeerResponse — one entry in the responses bag.
type PeerResponse struct {
	Agent     string          `json:"agent"`
	SessionID string          `json:"session_id"`
	Response  json.RawMessage `json:"response"`
	Error     *string         `json:"error"`
}

// PeerIdentity — one entry in the no_response bag.
type PeerIdentity struct {
	Agent     string `json:"agent"`
	SessionID string `json:"session_id"`
}

// ─────────────────────────────────────────────────────────────────────────────
// netAskBroadcast — replaces the stub in tools.go
// ─────────────────────────────────────────────────────────────────────────────

// netAskBroadcastImpl is the real implementation; tools.go's netAskBroadcast
// delegates here. Kept in a separate file (per spec §15 file index) so the
// broadcast surface is reviewable independently of the unicast handler.
func (c *Client) netAskBroadcastImpl(req ipc.Request, w *ipc.Writer, p netAskParams, timeoutMs int) {
	c.mu.RLock()
	id := c.identity
	serverURL := c.serverURL
	authToken := c.authToken
	currentInbound := c.currentInbound
	c.mu.RUnlock()

	if id == nil {
		_ = w.RespondError(req.ID, "coms-net not initialised")
		return
	}
	if serverURL == "" || authToken == "" {
		_ = w.RespondError(req.ID, "coms-net: no server connection")
		return
	}

	hops := 0
	if currentInbound != nil {
		hops = currentInbound.hops + 1
	}
	if hops >= c.cfg.MaxHops {
		_ = w.RespondError(req.ID, fmt.Sprintf("coms_net_ask: hop limit reached (%d >= %d)", hops, c.cfg.MaxHops))
		return
	}

	// 1. Enumerate peers via GET /v1/agents (inline; avoids an IPC round-trip).
	peers, err := c.listPeersForBroadcast(id.project, id.sessionID, serverURL, authToken)
	if err != nil {
		_ = w.RespondError(req.ID, fmt.Sprintf("coms_net_ask: peer list failed: %v", safeErrorStr(err.Error(), authToken)))
		return
	}

	// Empty peer set is not an error: empty bag.
	if len(peers) == 0 {
		details := mustMarshalBroadcast(BroadcastResponse{
			Broadcast:  true,
			TimeoutMs:  timeoutMs,
			TotalPeers: 0,
			Responded:  0,
			TimedOut:   0,
			Responses:  []PeerResponse{},
			NoResponse: []PeerIdentity{},
		})
		_ = w.Respond(req.ID,
			[]ipc.ContentItem{{Type: "text", Text: "0 of 0 peers responded (no peers in project)."}},
			details)
		return
	}

	// 2. Fan out: one POST /v1/messages per peer, bounded by GOMAXPROCS.
	type fanOut struct {
		agent     string
		sessionID string
		msgID     string
		pr        *netPendingReply
		sendErr   string
	}

	results := make([]fanOut, len(peers))
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for i := range peers {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			peer := peers[i]
			sendReq := proto.SendRequest{
				Project:        id.project,
				SenderSession:  id.sessionID,
				Target:         peer.Name,
				TargetSession:  nil,
				Prompt:         p.Prompt,
				ConversationID: p.ConversationID,
				ResponseSchema: p.ResponseSchema,
				Hops:           hops,
			}
			respData, err := c.httpPost(context.Background(), serverURL+"/v1/messages", authToken, sendReq)
			if err != nil {
				results[i] = fanOut{
					agent:     peer.Name,
					sessionID: peer.SessionID,
					sendErr:   safeErrorStr(err.Error(), authToken),
				}
				return
			}
			var sendResp proto.SendResponse
			if err := json.Unmarshal(respData, &sendResp); err != nil {
				results[i] = fanOut{
					agent:     peer.Name,
					sessionID: peer.SessionID,
					sendErr:   "malformed send response",
				}
				return
			}
			pr := &netPendingReply{
				ready:         make(chan struct{}),
				targetName:    peer.Name,
				targetSession: sendResp.TargetSession,
				createdAt:     util.NowIso(),
			}
			c.mu.Lock()
			c.pendingReplies[sendResp.MsgID] = pr
			c.mu.Unlock()
			// T7: per-peer ask_send audit (broadcast: true).
			_ = c.audit.Append(map[string]any{
				"event":          "ask_send",
				"msg_id":         sendResp.MsgID,
				"target":         peer.Name,
				"target_session": sendResp.TargetSession,
				"hops":           hops,
				"broadcast":      true,
				"ts":             util.NowIso(),
			})
			results[i] = fanOut{
				agent:     peer.Name,
				sessionID: peer.SessionID,
				msgID:     sendResp.MsgID,
				pr:        pr,
			}
		}()
	}
	wg.Wait()

	// 3. Wait under a single shared deadline.
	timer := time.NewTimer(time.Duration(timeoutMs) * time.Millisecond)
	defer timer.Stop()

	type collected struct {
		idx    int
		result *netPendingResult
		ok     bool
	}
	collectCh := make(chan collected, len(results))
	for i, fo := range results {
		if fo.pr == nil {
			// Send failed already — feed a synthetic timeout/error.
			continue
		}
		i, fo := i, fo
		go func() {
			select {
			case <-fo.pr.ready:
				fo.pr.mu.Lock()
				res := fo.pr.result
				fo.pr.mu.Unlock()
				collectCh <- collected{idx: i, result: res, ok: true}
			case <-timer.C:
				collectCh <- collected{idx: i, ok: false}
			}
		}()
	}

	// Gather replies until all goroutines report. Note: send-failure entries
	// (where fo.pr == nil) are not pushed onto collectCh — they're treated as
	// no-response below with a synthetic error string.
	expected := 0
	for _, fo := range results {
		if fo.pr != nil {
			expected++
		}
	}
	replies := make(map[int]*netPendingResult)
	timedOut := make(map[int]bool)
	for i := 0; i < expected; i++ {
		c := <-collectCh
		if c.ok {
			replies[c.idx] = c.result
		} else {
			timedOut[c.idx] = true
		}
	}

	// 4. Build the response bag.
	bag := BroadcastResponse{
		Broadcast:  true,
		TimeoutMs:  timeoutMs,
		TotalPeers: len(results),
		Responses:  []PeerResponse{},
		NoResponse: []PeerIdentity{},
	}
	for i, fo := range results {
		if fo.sendErr != "" {
			errStr := fo.sendErr
			bag.Responses = append(bag.Responses, PeerResponse{
				Agent:     fo.agent,
				SessionID: fo.sessionID,
				Response:  nil,
				Error:     &errStr,
			})
			bag.Responded++
			continue
		}
		if r, ok := replies[i]; ok && r != nil {
			if r.errMsg != "" {
				e := r.errMsg
				bag.Responses = append(bag.Responses, PeerResponse{
					Agent:     fo.agent,
					SessionID: fo.sessionID,
					Response:  nil,
					Error:     &e,
				})
			} else {
				bag.Responses = append(bag.Responses, PeerResponse{
					Agent:     fo.agent,
					SessionID: fo.sessionID,
					Response:  r.response,
					Error:     nil,
				})
			}
			bag.Responded++
			continue
		}
		// Timed out.
		bag.NoResponse = append(bag.NoResponse, PeerIdentity{
			Agent:     fo.agent,
			SessionID: fo.sessionID,
		})
		bag.TimedOut++
	}

	// 5. Human-readable summary.
	summary := fmt.Sprintf("%d of %d peers responded within %dms.", bag.Responded, bag.TotalPeers, bag.TimeoutMs)
	if bag.Responded > 0 {
		summary += "\n\n"
		for _, r := range bag.Responses {
			body := ""
			if r.Error != nil {
				body = "error: " + *r.Error
			} else {
				body = jsonToStr(r.Response)
			}
			summary += fmt.Sprintf("%s: %s\n", r.Agent, body)
		}
	}

	_ = w.Respond(req.ID,
		[]ipc.ContentItem{{Type: "text", Text: summary}},
		mustMarshalBroadcast(bag))
}

// listPeersForBroadcast enumerates peers in the project (excluding self and
// --explicit agents). Inline HTTP GET; avoids an IPC round-trip.
func (c *Client) listPeersForBroadcast(project, selfSession, serverURL, authToken string) ([]proto.AgentCard, error) {
	qs := fmt.Sprintf("?project=%s&include_explicit=false", urlEscape(project))
	data, err := c.httpGet(context.Background(), serverURL+"/v1/agents"+qs, authToken)
	if err != nil {
		return nil, err
	}
	var listResp proto.ListAgentsResponse
	if err := json.Unmarshal(data, &listResp); err != nil {
		return nil, fmt.Errorf("malformed agent list response")
	}
	out := make([]proto.AgentCard, 0, len(listResp.Agents))
	for _, a := range listResp.Agents {
		if a.SessionID == selfSession {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func mustMarshalBroadcast(b BroadcastResponse) map[string]any {
	// Return as map[string]any so it merges into the ipc Respond details
	// shape. Round-trip through JSON to flatten typed-struct fields.
	raw, _ := json.Marshal(b)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}
