package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestUpdateAll_EmptyRegistry_SkipsHealAndSucceeds asserts that an
// empty registry short-circuits before HealUserAgent ever needs a
// network round trip, so update_all is safe to call before anything
// has been registered.
//
// The non-empty-registry heal path (real HTTP probing against the
// first registered URL) mirrors runUpdateAll's identical logic in
// cmd_registry.go, which is exercised end-to-end by the CLI; adding
// a second httptest-server harness here to re-prove the same probe
// sequence would not add coverage, only duplication.
func TestUpdateAll_EmptyRegistry_SkipsHealAndSucceeds(t *testing.T) {
	root := t.TempDir()
	cookiesPath := filepath.Join(t.TempDir(), "cookies.json")
	cookieJSON := `{"user_agent":"Mozilla/5.0 Chrome/120.0.0.0","cookies":[{"name":"cf_clearance","value":"tok","domain":".example.com"}]}`
	if err := os.WriteFile(cookiesPath, []byte(cookieJSON), 0o600); err != nil {
		t.Fatal(err)
	}

	srv, err := New(Opts{Root: root, CookiesPath: cookiesPath})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	out, err := srv.updateAll(context.Background(), UpdateAllInput{})
	if err != nil {
		t.Fatalf("updateAll: %v", err)
	}
	if len(out.Outcomes) != 0 {
		t.Fatalf("expected no outcomes for empty registry, got %+v", out.Outcomes)
	}
}
