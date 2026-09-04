package registry

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoad_MissingFileIsEmpty(t *testing.T) {
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Manga) != 0 || r.Version != 1 {
		t.Fatalf("expected empty v1 registry, got %+v", r)
	}
}

func TestUpsertSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	r, _ := Load(root)
	if r.Root() != root {
		t.Fatal("Root() should return the loaded root")
	}
	r.Upsert("Gintama", "https://truyenqqko.com/truyen-tranh/gintama-216")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, FileName)); err != nil {
		t.Fatal("registry file not written:", err)
	}
	r2, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Root() != root {
		t.Fatal("Root() should return the loaded root after reload")
	}
	e, ok := r2.Get("Gintama")
	if !ok || e.URL != "https://truyenqqko.com/truyen-tranh/gintama-216" {
		t.Fatalf("round trip lost entry: %+v ok=%v", e, ok)
	}
	if e.Added.IsZero() {
		t.Fatal("Added should be set on first upsert")
	}
}

func TestUpsert_KeepsAddedOnUpdate(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("A", "https://x.com/a")
	first := r.Manga["A"].Added
	time.Sleep(2 * time.Millisecond)
	r.Upsert("A", "https://x.com/a2")
	if !r.Manga["A"].Added.Equal(first) {
		t.Fatal("Added must not change on update")
	}
	if r.Manga["A"].URL != "https://x.com/a2" {
		t.Fatal("URL must update")
	}
}

func TestTouch(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("A", "https://x.com/a")
	at := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	r.Touch("A", at)
	r.Touch("missing", at) // must not panic or create
	if !r.Manga["A"].LastSynced.Equal(at) {
		t.Fatal("LastSynced not set")
	}
	if _, ok := r.Manga["missing"]; ok {
		t.Fatal("Touch must not create entries")
	}
}

func TestNames_Sorted(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("b", "https://x.com/b")
	r.Upsert("A", "https://x.com/a")
	r.Upsert("c", "https://x.com/c")
	got := r.Names()
	want := []string{"A", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestRewriteHost(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("A", "https://truyenqqko.com/truyen-tranh/a-1")
	r.Upsert("B", "https://truyenqqko.com/truyen-tranh/b-2?x=1")
	r.Upsert("C", "https://other.example/keep-me")
	n, err := r.RewriteHost("truyenqqnew.com")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("expected 3 rewritten, got %d", n)
	}
	if r.Manga["A"].URL != "https://truyenqqnew.com/truyen-tranh/a-1" {
		t.Fatal("A:", r.Manga["A"].URL)
	}
	if r.Manga["B"].URL != "https://truyenqqnew.com/truyen-tranh/b-2?x=1" {
		t.Fatal("B lost query:", r.Manga["B"].URL)
	}
	if r.Manga["C"].URL != "https://truyenqqnew.com/keep-me" {
		t.Fatal("C:", r.Manga["C"].URL)
	}
	// scheme supplied explicitly
	if _, err := r.RewriteHost("http://plain.example"); err != nil {
		t.Fatal(err)
	}
	if r.Manga["A"].URL != "http://plain.example/truyen-tranh/a-1" {
		t.Fatal("explicit scheme not honoured:", r.Manga["A"].URL)
	}
}

func TestSwapHost(t *testing.T) {
	got, err := SwapHost("https://truyenqqko.com/truyen-tranh/a-1?x=1", "truyenqqnew.com")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://truyenqqnew.com/truyen-tranh/a-1?x=1" {
		t.Fatalf("got %q", got)
	}
	got, err = SwapHost("https://old.example/a", "http://new.example")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://new.example/a" {
		t.Fatalf("explicit scheme not honoured: %q", got)
	}
	if _, err := SwapHost("https://old.example/a", "://bad"); err == nil {
		t.Fatal("expected error for invalid host")
	}
}

func TestRewriteHost_InvalidHostOnEmptyRegistry(t *testing.T) {
	r, _ := Load(t.TempDir())
	if _, err := r.RewriteHost("://bad"); err == nil {
		t.Fatal("expected error for invalid host even on an empty registry")
	}
}

func TestRewriteHost_MalformedEntryLeavesRegistryUntouched(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("Good", "https://old.example/good")
	// Force a malformed stored URL directly (Upsert would parse fine
	// for most strings, so inject one that url.Parse rejects outright).
	e := r.Manga["Good"]
	r.Manga["Bad"] = e
	bad := r.Manga["Bad"]
	bad.URL = "http://[::1:bad"
	r.Manga["Bad"] = bad

	if _, err := r.RewriteHost("new.example"); err == nil {
		t.Fatal("expected error for malformed stored URL")
	}
	if r.Manga["Good"].URL != "https://old.example/good" {
		t.Fatalf("good entry must be untouched on abort: %s", r.Manga["Good"].URL)
	}
	if r.Manga["Bad"].URL != "http://[::1:bad" {
		t.Fatalf("bad entry must be untouched on abort: %s", r.Manga["Bad"].URL)
	}
}

func TestLoad_CorruptFile(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, FileName), []byte("{not json"), 0o644)
	if _, err := Load(root); err == nil {
		t.Fatal("expected error for corrupt registry")
	}
}

func TestSave_NoTempLeftBehind(t *testing.T) {
	root := t.TempDir()
	r, _ := Load(root)
	r.Upsert("A", "https://x.com/a")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() != FileName && e.Name() != FileName+".lock" {
			t.Fatalf("unexpected file left in root: %s", e.Name())
		}
	}
}

func TestSave_OmitZeroLastSynced(t *testing.T) {
	root := t.TempDir()
	r, _ := Load(root)
	r.Upsert("Unsynced", "https://x.com/unsynced")
	r.Upsert("Synced", "https://x.com/synced")
	r.Touch("Synced", time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC))
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, FileName))
	if err != nil {
		t.Fatal(err)
	}
	fileStr := string(content)
	// Unsynced entry must not contain last_synced field
	if strings.Contains(fileStr, `"Unsynced"`) && strings.Contains(fileStr[strings.Index(fileStr, `"Unsynced"`):], `"last_synced"`) {
		t.Fatal("Unsynced entry should not have last_synced field in JSON")
	}
	// Synced entry must contain last_synced field
	if !strings.Contains(fileStr, `"last_synced"`) {
		t.Fatal("Synced entry should have last_synced field in JSON")
	}
}

func TestSetFinished_RoundTrip(t *testing.T) {
	root := t.TempDir()
	r, _ := Load(root)
	r.Upsert("A", "https://x.com/a")
	r.Upsert("B", "https://x.com/b")
	if !r.SetFinished("A", true) {
		t.Fatal("SetFinished should report the entry exists")
	}
	if r.SetFinished("missing", true) {
		t.Fatal("SetFinished must not create entries")
	}
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(root, FileName))
	if !strings.Contains(string(raw), `"finished": true`) {
		t.Fatalf("finished flag not persisted:\n%s", raw)
	}
	// B is not finished: the key must be omitted, not written as false.
	if strings.Count(string(raw), `"finished"`) != 1 {
		t.Fatalf("finished should appear once (A only):\n%s", raw)
	}
	r2, _ := Load(root)
	if a, _ := r2.Get("A"); !a.Finished {
		t.Fatal("A should be finished after reload")
	}
	if b, _ := r2.Get("B"); b.Finished {
		t.Fatal("B should not be finished")
	}
	r2.SetFinished("A", false)
	if a, _ := r2.Get("A"); a.Finished {
		t.Fatal("unfinish failed")
	}
}

func TestActiveNames_ExcludesFinished(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("b", "https://x.com/b")
	r.Upsert("a", "https://x.com/a")
	r.Upsert("c", "https://x.com/c")
	r.SetFinished("b", true)
	got := r.ActiveNames()
	if len(got) != 2 || got[0] != "a" || got[1] != "c" {
		t.Fatalf("ActiveNames = %v, want [a c]", got)
	}
}
