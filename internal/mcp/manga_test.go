package mcp

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/anhpham/downloader/internal/layout"
)

func TestListManga_EmptyRoot(t *testing.T) {
	got, err := ListManga(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 entries, got %d", len(got))
	}
}

func TestListAndInspect(t *testing.T) {
	root := t.TempDir()
	writeFakeArchive(t, root, "Foo.cbz", []string{
		"chap-001/001.jpg",
		"chap-001/" + layout.CommentsFilename,
		"chap-002/001.jpg",
	})
	writeFakeArchive(t, root, "Bar.cbz", []string{
		"chap-001/001.jpg",
	})

	list, err := ListManga(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}

	insp, err := InspectManga(root, "Foo")
	if err != nil {
		t.Fatal(err)
	}
	if insp.Chapters != 2 {
		t.Fatalf("chapters = %d, want 2", insp.Chapters)
	}
	if insp.CommentsAttached != 1 {
		t.Fatalf("commented = %d, want 1", insp.CommentsAttached)
	}
	if insp.MissingComments != 1 {
		t.Fatalf("missing = %d, want 1", insp.MissingComments)
	}

	if _, err := InspectManga(root, "Nonesuch"); err == nil {
		t.Fatal("expected NO_ARCHIVE for missing manga")
	}
}

func writeFakeArchive(t *testing.T, root, name string, entries []string) {
	t.Helper()
	f, err := os.Create(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for _, e := range entries {
		fw, err := w.Create(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}
