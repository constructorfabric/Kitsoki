package studio

import (
	"context"
	"fmt"

	"kitsoki/internal/runstatus"
	rsserver "kitsoki/internal/runstatus/server"
)

// RunstatusProvider adapts open studio driving handles to the runstatus web
// provider contract. It lets render.web serve the real browser UI for the same
// live MCP session instead of creating a second, disconnected web registry.
type RunstatusProvider struct {
	sess *StudioSession
}

// NewRunstatusProvider returns a runstatus provider over sess. The provider is
// intentionally lifecycle-read-only: MCP owns session creation/closure, while
// runstatus reads and drives the already-open sessions through their existing
// Driver.
func NewRunstatusProvider(sess *StudioSession) *RunstatusProvider {
	return &RunstatusProvider{sess: sess}
}

// Get resolves a studio session by the underlying kitsoki session id.
func (p *RunstatusProvider) Get(sessionID string) (rsserver.Entry, bool) {
	sh, ok := p.sessionByID(sessionID)
	if !ok || sh.Runtime == nil {
		return rsserver.Entry{}, false
	}
	return rsserver.Entry{Source: sh.Runtime, Driver: sh.Driver}, true
}

// List returns headers for every open driving handle.
func (p *RunstatusProvider) List() []runstatus.SessionHeader {
	if p == nil || p.sess == nil {
		return nil
	}
	p.sess.mu.Lock()
	handles := make([]*SessionHandle, 0, len(p.sess.sessions))
	for _, sh := range p.sess.sessions {
		if sh.Runtime != nil {
			handles = append(handles, sh)
		}
	}
	p.sess.mu.Unlock()

	out := make([]runstatus.SessionHeader, 0, len(handles))
	for _, sh := range handles {
		snap, err := sh.Runtime.Snapshot()
		if err != nil {
			continue
		}
		hdr := snap.Session
		hdr.SessionID = string(sh.SID)
		out = append(out, hdr)
	}
	return out
}

// CurrentSession returns the most recently opened live studio session, if any.
func (p *RunstatusProvider) CurrentSession() (string, bool) {
	if p == nil || p.sess == nil {
		return "", false
	}
	p.sess.mu.Lock()
	defer p.sess.mu.Unlock()
	if p.sess.currentSID == "" {
		return "", false
	}
	if _, ok := p.sess.sessionByIDLocked(p.sess.currentSID); !ok {
		return "", false
	}
	return p.sess.currentSID, true
}

// NewSession is disabled here: MCP session.new is the owner of studio handle
// lifecycle, so the web surface must not create untracked sessions behind it.
func (p *RunstatusProvider) NewSession(context.Context, string) (string, error) {
	return "", fmt.Errorf("studio runstatus provider: create sessions through session.new")
}

func (p *RunstatusProvider) Reload(context.Context, string) (bool, error) {
	return false, fmt.Errorf("studio runstatus provider: reload through the studio session")
}

func (p *RunstatusProvider) Staleness(context.Context, string) (bool, string, error) {
	return false, "", nil
}

func (p *RunstatusProvider) ListStories() []rsserver.StoryHeader { return nil }

func (p *RunstatusProvider) Rescan() ([]rsserver.StoryHeader, error) {
	return nil, fmt.Errorf("studio runstatus provider: story discovery is owned by studio MCP")
}

func (p *RunstatusProvider) sessionByID(sessionID string) (*SessionHandle, bool) {
	if p == nil || p.sess == nil {
		return nil, false
	}
	p.sess.mu.Lock()
	defer p.sess.mu.Unlock()
	return p.sess.sessionByIDLocked(sessionID)
}

func (ss *StudioSession) sessionByIDLocked(sessionID string) (*SessionHandle, bool) {
	for _, sh := range ss.sessions {
		if string(sh.SID) == sessionID {
			return sh, true
		}
	}
	return nil, false
}

var (
	_ rsserver.SessionProvider        = (*RunstatusProvider)(nil)
	_ rsserver.CurrentSessionProvider = (*RunstatusProvider)(nil)
)
