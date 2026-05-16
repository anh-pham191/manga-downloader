package archive

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func buildCBZ(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "sample.cbz")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for name, body := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Store}
		zw, err := w.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(zw, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestInspect_ClassifiesEntries(t *testing.T) {
	p := buildCBZ(t, map[string][]byte{
		"chap-0001/001.jpg":          []byte("jpg-1"),
		"chap-0001/002.jpg":          []byte("jpg-2"),
		"chap-0001/zzz-comments.png": []byte("png"),
		"chap-0002/001.jpg":          []byte("jpg-3"),
		"chap-0042-5/001.webp":       []byte("webp"),
		".DS_Store":                  []byte("junk"),
	})
	in, err := Inspect(p)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"chap-0001":   true,
		"chap-0002":   true,
		"chap-0042-5": true,
	}
	if len(in.Have) != len(want) {
		t.Fatalf("Have = %v, want %v", in.Have, want)
	}
	for k := range want {
		if !in.Have[k] {
			t.Errorf("Have missing %q", k)
		}
	}
	if !in.HaveComments["chap-0001"] || in.HaveComments["chap-0002"] {
		t.Errorf("HaveComments = %v", in.HaveComments)
	}
}

func TestInspect_MissingFile(t *testing.T) {
	in, err := Inspect("/nonexistent.cbz")
	if err != nil {
		t.Fatalf("Inspect missing should not error, got %v", err)
	}
	if len(in.Have) != 0 {
		t.Errorf("Have should be empty")
	}
}
