package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
)

func TestUpdateClearance_PreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")

	seed := fetcher.CookieFile{
		UserAgent: "Mozilla/5.0 (existing)",
		Cookies: []fetcher.CookieRecord{
			{Name: "cf_clearance", Value: "OLD", Domain: ".truyenqqko.com"},
			{Name: "other", Value: "keep", Domain: ".truyenqqko.com"},
		},
	}
	mustWriteJSON(t, path, seed)

	if err := UpdateClearance(path, "NEW", "", ""); err != nil {
		t.Fatal(err)
	}

	got := mustReadJSON(t, path)
	if got.UserAgent != "Mozilla/5.0 (existing)" {
		t.Fatalf("UA = %q, want preserved", got.UserAgent)
	}
	if len(got.Cookies) != 2 {
		t.Fatalf("cookies = %d, want 2", len(got.Cookies))
	}
	if val := findCookie(got, "cf_clearance"); val != "NEW" {
		t.Fatalf("cf_clearance = %q, want NEW", val)
	}
	if val := findCookie(got, "other"); val != "keep" {
		t.Fatalf("other cookie lost: %q", val)
	}
}

func TestUpdateClearance_CreatesFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	if err := UpdateClearance(path, "FRESH", "ua/1.0", ".example.com"); err != nil {
		t.Fatal(err)
	}
	got := mustReadJSON(t, path)
	if got.UserAgent != "ua/1.0" {
		t.Fatalf("UA = %q, want ua/1.0", got.UserAgent)
	}
	if findCookie(got, "cf_clearance") != "FRESH" {
		t.Fatal("cf_clearance not set")
	}
	if got.Cookies[0].Domain != ".example.com" {
		t.Fatalf("domain = %q, want .example.com", got.Cookies[0].Domain)
	}
}

func TestUpdateClearance_RejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	err := UpdateClearance(path, "   ", "", "")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want empty-value rejection", err)
	}
}

func TestCookieStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	seed := fetcher.CookieFile{
		Cookies: []fetcher.CookieRecord{{Name: "cf_clearance", Value: "abcdefghIJKLMNOP", Domain: ".x"}},
	}
	mustWriteJSON(t, path, seed)
	st, err := CookieStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.HasClearance {
		t.Fatal("expected HasClearance = true")
	}
	if st.Last8 != "IJKLMNOP" {
		t.Fatalf("Last8 = %q", st.Last8)
	}
}

func TestCookieStatus_MissingFile(t *testing.T) {
	st, err := CookieStatus(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.HasClearance {
		t.Fatal("missing file must report HasClearance = false")
	}
}

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustReadJSON(t *testing.T, path string) fetcher.CookieFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cf fetcher.CookieFile
	if err := json.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	return cf
}

func findCookie(cf fetcher.CookieFile, name string) string {
	for _, c := range cf.Cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}
