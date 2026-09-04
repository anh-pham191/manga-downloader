package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anhpham/downloader/internal/pipeline"
	"github.com/anhpham/downloader/internal/registry"
)

func TestFormatSummary(t *testing.T) {
	res := pipeline.UpdateAllResult{Outcomes: []pipeline.MangaOutcome{
		{Name: "Gintama", NewChapters: 2, Status: "ok"},
		{Name: "Naruto", Status: "no-archive", Err: pipeline.ErrNoArchive},
		{Name: "One Piece", Status: "failed", Err: errors.New("boom")},
	}}
	s := formatSummary(res)
	for _, want := range []string{"Gintama", "2", "ok", "Naruto", "no-archive", "One Piece", "failed", "boom", "new chapters: 2", "failed: 2"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary missing %q:\n%s", want, s)
		}
	}
}

func TestPromptDomain_ReadsLine(t *testing.T) {
	var out strings.Builder
	ask := promptDomain(strings.NewReader("  truyenqqnew.com \n"), &out)
	got := ask("truyenqqko.com", errors.New("no such host"))
	if got != "truyenqqnew.com" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(out.String(), "truyenqqko.com") || !strings.Contains(out.String(), "no such host") {
		t.Fatalf("prompt must show evidence:\n%s", out.String())
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

func TestRunRegister_WarnsWhenArchiveMissing(t *testing.T) {
	dir := t.TempDir()
	var code int
	stderr := captureStderr(t, func() {
		code = runRegister([]string{"--out", dir, "Naruto", "https://example.com/manga/naruto"})
	})
	if code != 0 {
		t.Fatalf("runRegister exit = %d, want 0", code)
	}
	if !strings.Contains(stderr, "warning: no archive") || !strings.Contains(stderr, filepath.Join(dir, "Naruto.cbz")) {
		t.Fatalf("expected no-archive warning naming the path, got:\n%s", stderr)
	}
	reg, err := registry.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := reg.Get("Naruto"); !ok || e.URL != "https://example.com/manga/naruto" {
		t.Fatalf("registry not updated: %+v ok=%v", e, ok)
	}
}

func TestRunRegister_NoWarningWhenArchiveExists(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Naruto.cbz"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		if code := runRegister([]string{"--out", dir, "Naruto", "https://example.com/manga/naruto"}); code != 0 {
			t.Fatalf("runRegister exit = %d, want 0", code)
		}
	})
	if strings.Contains(stderr, "warning: no archive") {
		t.Fatalf("unexpected no-archive warning:\n%s", stderr)
	}
}

func TestPromptDomain_EmptyAborts(t *testing.T) {
	ask := promptDomain(strings.NewReader("\n"), &strings.Builder{})
	if got := ask("x", errors.New("e")); got != "" {
		t.Fatalf("expected abort, got %q", got)
	}
}
