package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/anhpham/downloader/internal/pipeline"
)

func TestFormatSummary(t *testing.T) {
	res := pipeline.UpdateAllResult{Outcomes: []pipeline.MangaOutcome{
		{Name: "Gintama", NewChapters: 2, Status: "ok"},
		{Name: "Naruto", Status: "no-archive", Err: pipeline.ErrNoArchive},
		{Name: "One Piece", Status: "failed", Err: errors.New("boom")},
	}}
	s := formatSummary(res)
	for _, want := range []string{"Gintama", "2", "ok", "Naruto", "no-archive", "One Piece", "failed", "boom", "new chapters: 2", "failed: 1"} {
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

func TestPromptDomain_EmptyAborts(t *testing.T) {
	ask := promptDomain(strings.NewReader("\n"), &strings.Builder{})
	if got := ask("x", errors.New("e")); got != "" {
		t.Fatalf("expected abort, got %q", got)
	}
}
