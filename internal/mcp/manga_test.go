package mcp

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anhpham/downloader/internal/layout"
	"github.com/anhpham/downloader/internal/registry"
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

func TestListManga_IncludesRegistryURL(t *testing.T) {
	root := t.TempDir()
	writeFakeArchive(t, root, "A.cbz", []string{"chap-0001/001.jpg"})
	reg, _ := registry.Load(root)
	reg.Upsert("A", "https://truyenqqko.com/truyen-tranh/a-1")
	reg.Touch("A", time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC))
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	items, err := ListManga(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].URL != "https://truyenqqko.com/truyen-tranh/a-1" || items[0].LastSynced != "2026-09-05T01:02:03Z" {
		t.Fatalf("items %+v", items)
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
