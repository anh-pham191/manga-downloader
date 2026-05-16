package mcp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunState_AcquireReleaseAcquire(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	ar, err := rs.Acquire("Gintama", "resume", cancel)
	if err != nil {
		t.Fatal(err)
	}
	if ar.Name != "Gintama" {
		t.Fatal("name not stored")
	}
	rs.Release()
	if _, err := rs.Acquire("Other", "sync-manga", cancel); err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	rs.Release()
}

func TestRunState_ConcurrentAcquireRejected(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	if _, err := rs.Acquire("A", "resume", cancel); err != nil {
		t.Fatal(err)
	}
	_, err := rs.Acquire("B", "resume", cancel)
	var te *ToolError
	if !errors.As(err, &te) || te.Code != CodeRunInProgress {
		t.Fatalf("err = %v, want RUN_IN_PROGRESS", err)
	}
}

func TestRunState_SnapshotIsCopy(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	if _, err := rs.Acquire("A", "resume", cancel); err != nil {
		t.Fatal(err)
	}
	snap := rs.Snapshot()
	if snap == nil || snap.Name != "A" {
		t.Fatalf("snapshot = %+v", snap)
	}
	// Releasing must not blank the snapshot we already took.
	rs.Release()
	if snap.Name != "A" {
		t.Fatal("snapshot was aliased to internal state")
	}
	if rs.Snapshot() != nil {
		t.Fatal("post-release snapshot must be nil")
	}
}

func TestRunState_StartedAtRecent(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	if _, err := rs.Acquire("A", "resume", cancel); err != nil {
		t.Fatal(err)
	}
	defer rs.Release()
	if time.Since(rs.Snapshot().StartedAt) > time.Second {
		t.Fatal("StartedAt not set near now")
	}
}
