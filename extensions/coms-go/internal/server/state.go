package server

import (
	"strconv"
	"sync"

	"github.com/pi-vs-cc/coms-go/internal/proto"
	"github.com/pi-vs-cc/coms-go/internal/util"
)

// Awaiter holds the resolve channel and timer for a long-poll await.
type Awaiter struct {
	ch    chan proto.ComsMessage
	timer interface{ Stop() bool } // *time.Timer
}

// SseWriter is the handle for an open SSE connection.
type SseWriter struct {
	sessionID string
	ch        chan string // buffered; server writes SSE frames here
	done      chan struct{}
	lastID    int
}

// ProjectState is the per-project in-memory state, guarded by mu.
type ProjectState struct {
	mu        sync.RWMutex
	agents    map[string]*proto.NetRegistryEntry // session_id → entry
	nameIndex map[string]map[string]struct{}     // name → set of session_ids
	messages  map[string]*proto.ComsMessage      // msg_id → message
	streams   map[string]*SseWriter              // session_id → writer
	awaiters  map[string]map[*Awaiter]struct{}   // msg_id → set of awaiters
}

func newProjectState() *ProjectState {
	return &ProjectState{
		agents:    make(map[string]*proto.NetRegistryEntry),
		nameIndex: make(map[string]map[string]struct{}),
		messages:  make(map[string]*proto.ComsMessage),
		streams:   make(map[string]*SseWriter),
		awaiters:  make(map[string]map[*Awaiter]struct{}),
	}
}

// ServerState is the global server state.
type ServerState struct {
	serverID  string
	startedAt string
	mu        sync.RWMutex
	projects  map[string]*ProjectState
}

func newServerState() *ServerState {
	return &ServerState{
		serverID:  util.NewULID(),
		startedAt: util.NowIso(),
		projects:  make(map[string]*ProjectState),
	}
}

// getOrCreateProject returns the ProjectState for name, creating it if absent.
// Caller must NOT hold s.mu when calling — this function acquires it.
func (s *ServerState) getOrCreateProject(name string) *ProjectState {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.projects[name]
	if !ok {
		p = newProjectState()
		s.projects[name] = p
	}
	return p
}

// getProject returns the ProjectState for name, or nil if not found.
func (s *ServerState) getProject(name string) *ProjectState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.projects[name]
}

// allProjects returns a snapshot of project names → states.
func (s *ServerState) allProjects() map[string]*ProjectState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]*ProjectState, len(s.projects))
	for k, v := range s.projects {
		out[k] = v
	}
	return out
}

// ─── ProjectState helpers (all require p.mu held by caller unless noted) ───────

func nameIndexAdd(p *ProjectState, name, sessionID string) {
	bag, ok := p.nameIndex[name]
	if !ok {
		bag = make(map[string]struct{})
		p.nameIndex[name] = bag
	}
	bag[sessionID] = struct{}{}
}

func nameIndexRemove(p *ProjectState, name, sessionID string) {
	bag, ok := p.nameIndex[name]
	if !ok {
		return
	}
	delete(bag, sessionID)
	if len(bag) == 0 {
		delete(p.nameIndex, name)
	}
}

// entryToCard converts a NetRegistryEntry to an AgentCard (strips housekeeping fields).
func entryToCard(e *proto.NetRegistryEntry) proto.AgentCard {
	return e.AgentCard
}

// resolveUniqueName returns desiredName if unused, else desiredName2, desiredName3, …
// Caller must hold p.mu (at least RLock).
func resolveUniqueName(p *ProjectState, desired string) string {
	liveNames := make(map[string]struct{}, len(p.agents))
	for _, a := range p.agents {
		liveNames[a.Name] = struct{}{}
	}
	if _, taken := liveNames[desired]; !taken {
		return desired
	}
	for n := 2; ; n++ {
		candidate := desired + strconv.Itoa(n)
		if _, taken := liveNames[candidate]; !taken {
			return candidate
		}
	}
}

// inboxDepthFor counts queued/delivered messages targeting sessionID.
// Caller must hold p.mu (at least RLock).
func inboxDepthFor(p *ProjectState, targetSession string) int {
	n := 0
	for _, m := range p.messages {
		if m.TargetSession != targetSession {
			continue
		}
		if m.Status == proto.MsgStatusQueued || m.Status == proto.MsgStatusDelivered {
			n++
		}
	}
	return n
}

// broadcast sends an SSE frame to all streams EXCEPT excludeSession.
// Caller must hold p.mu (at least RLock).
func broadcast(p *ProjectState, event string, data any, excludeSession string) {
	for sid, w := range p.streams {
		if sid == excludeSession {
			continue
		}
		w.lastID++
		sendFrame(w, sseFrameWithID(event, data, w.lastID))
	}
}

// sendToStream sends an SSE frame to the specific session's stream (if open).
// Caller must hold p.mu (at least RLock).
func sendToStream(p *ProjectState, sessionID, event string, data any) {
	w, ok := p.streams[sessionID]
	if !ok {
		return
	}
	w.lastID++
	sendFrame(w, sseFrameWithID(event, data, w.lastID))
}

// sendFrame non-blockingly writes a frame to an SseWriter's channel.
func sendFrame(w *SseWriter, frame string) {
	select {
	case w.ch <- frame:
	default:
		// channel full or writer dead; the goroutine will close on ctx done
	}
}

// releaseAwaiters wakes all awaiters for msg_id.
// Caller must hold p.mu write lock.
func releaseAwaiters(p *ProjectState, msgID string) {
	set, ok := p.awaiters[msgID]
	if !ok {
		return
	}
	msg := p.messages[msgID]
	for a := range set {
		if a.timer != nil {
			a.timer.Stop()
		}
		if msg != nil {
			select {
			case a.ch <- *msg:
			default:
			}
		}
	}
	delete(p.awaiters, msgID)
}
