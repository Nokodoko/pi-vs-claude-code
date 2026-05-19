package server

import (
	"context"
	"time"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// startTickers launches the three background loops matching the TS server:
//   - staleScanTick  every 5  s — mark stale/offline, broadcast agent_left/agent_stale
//   - ttlScanTick    every 10 s — expire messages, releaseAwaiters
//   - keepaliveTick  every 15 s — SSE comment ping
//
// All loops are ctx-aware and exit cleanly when ctx is cancelled.
func startTickers(ctx context.Context, st *ServerState, cfg *Config) {
	go runTicker(ctx, time.Duration(staleScanIntervalMS)*time.Millisecond, func() {
		staleScanTick(st, cfg)
	})
	go runTicker(ctx, time.Duration(ttlScanIntervalMS)*time.Millisecond, func() {
		ttlScanTick(st, cfg)
	})
	go runTicker(ctx, time.Duration(ssKeepaliveMS)*time.Millisecond, func() {
		keepaliveTick(st)
	})
}

func runTicker(ctx context.Context, interval time.Duration, fn func()) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// staleScanTick mirrors the TS staleScanTick(). Runs under per-project lock.
func staleScanTick(st *ServerState, cfg *Config) {
	now := time.Now().UnixMilli()
	for projectName, p := range st.allProjects() {
		p.mu.Lock()
		for sid, entry := range p.agents {
			last, err := time.Parse(time.RFC3339Nano, entry.LastSeenAt)
			if err != nil {
				continue
			}
			dt := now - last.UnixMilli()
			if dt > int64(cfg.OfflineAfterMS) {
				// Remove agent, close stream, emit agent_left.
				delete(p.agents, sid)
				nameIndexRemove(p, entry.Name, sid)
				if w, ok := p.streams[sid]; ok {
					close(w.done)
					delete(p.streams, sid)
				}
				logOffline(entry.Name)
				broadcast(p, "agent_left", map[string]any{
					"project":    projectName,
					"session_id": sid,
					"name":       entry.Name,
					"reason":     "stale",
				}, sid)
			} else if dt > int64(cfg.StaleAfterMS) && entry.Status != proto.StatusStale {
				entry.Status = proto.StatusStale
				logStale(entry.Name, int(dt/1000))
				broadcast(p, "agent_stale", map[string]any{
					"project":      projectName,
					"session_id":   sid,
					"name":         entry.Name,
					"last_seen_at": entry.LastSeenAt,
				}, sid)
			}
		}
		p.mu.Unlock()
	}
}

// ttlScanTick mirrors the TS ttlScanTick().
func ttlScanTick(st *ServerState, cfg *Config) {
	now := time.Now().UnixMilli()
	for _, p := range st.allProjects() {
		p.mu.Lock()
		for id, m := range p.messages {
			expires, _ := time.Parse(time.RFC3339Nano, m.ExpiresAt)
			switch m.Status {
			case proto.MsgStatusQueued, proto.MsgStatusDelivered:
				if !expires.IsZero() && now > expires.UnixMilli() {
					errStr := "expired"
					m.Status = proto.MsgStatusError
					m.Error = &errStr
					now2 := util.NowIso()
					m.CompletedAt = now2
					releaseAwaiters(p, id)
					logExpired(id)
					delete(p.messages, id)
				}
			case proto.MsgStatusComplete, proto.MsgStatusError:
				if m.CompletedAt != "" {
					completed, _ := time.Parse(time.RFC3339Nano, m.CompletedAt)
					if !completed.IsZero() && now-completed.UnixMilli() > int64(cfg.MessageTTLMS) {
						delete(p.messages, id)
					}
				}
			case proto.MsgStatusTimeout:
				if !expires.IsZero() && now > expires.UnixMilli() {
					delete(p.messages, id)
				}
			}
		}
		p.mu.Unlock()
	}
}

// keepaliveTick sends an SSE comment ping to all open streams.
func keepaliveTick(st *ServerState) {
	ts := util.NowIso()
	frame := ssePingFrame(ts)
	for _, p := range st.allProjects() {
		p.mu.RLock()
		for _, w := range p.streams {
			sendFrame(w, frame)
		}
		p.mu.RUnlock()
	}
}
