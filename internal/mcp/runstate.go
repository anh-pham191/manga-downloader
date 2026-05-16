package mcp

import (
	"context"
	"sync"
	"time"
)

// RunState enforces "one sync at a time" across all tool calls.
// Tools that don't run the pipeline never touch RunState.
type RunState struct {
	mu     sync.Mutex
	active *ActiveRun
}

type ActiveRun struct {
	Name      string
	Mode      string
	StartedAt time.Time
	Cancel    context.CancelFunc
}

// Acquire registers an active run. Returns a ToolError with code
// RUN_IN_PROGRESS if another run is already active.
func (r *RunState) Acquire(name, mode string, cancel context.CancelFunc) (*ActiveRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil {
		return nil, &ToolError{
			Code:    CodeRunInProgress,
			Message: "another sync is in progress (" + r.active.Mode + " " + r.active.Name + ")",
		}
	}
	r.active = &ActiveRun{
		Name:      name,
		Mode:      mode,
		StartedAt: time.Now().UTC(),
		Cancel:    cancel,
	}
	return r.active, nil
}

// Release clears the active run. Safe to call from defer even when
// Acquire failed (it'll be a no-op then).
func (r *RunState) Release() {
	r.mu.Lock()
	r.active = nil
	r.mu.Unlock()
}

// Snapshot returns a copy of the active run (or nil if idle), safe
// to read after the caller releases.
func (r *RunState) Snapshot() *ActiveRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return nil
	}
	cp := *r.active
	return &cp
}
