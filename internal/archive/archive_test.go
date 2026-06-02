package archive

import (
	"archive/zip"
	"bytes"
	"io"
	"io/ioutil"
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

func TestStageAndRename_PreservesOriginalAndAppends(t *testing.T) {
	p := buildCBZ(t, map[string][]byte{
		"chap-0001/001.jpg": []byte("AAA"),
	})

	// Scratch dir: chap-0001 has comments to add; chap-0002 is brand new.
	scratch := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(scratch, "chap-0001"), 0o755))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", "zzz-comments.png"), []byte("PNG"), 0o644))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", ".ok"), nil, 0o644))
	must(os.MkdirAll(filepath.Join(scratch, "chap-0002"), 0o755))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0002", "001.jpg"), []byte("BBB"), 0o644))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0002", ".ok"), nil, 0o644))
	// Poisoned chapter: NO .ok marker, must be excluded.
	must(os.MkdirAll(filepath.Join(scratch, "chap-0003"), 0o755))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0003", "001.jpg"), []byte("CCC"), 0o644))

	if err := StageAndRename(p, scratch); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	got := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = b
	}
	want := map[string]string{
		"chap-0001/001.jpg":          "AAA",
		"chap-0001/zzz-comments.png": "PNG",
		"chap-0002/001.jpg":          "BBB",
	}
	if len(got) != len(want) {
		t.Fatalf("entries: got %v, want %v", got, want)
	}
	for k, v := range want {
		if string(got[k]) != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	if _, present := got["chap-0003/001.jpg"]; present {
		t.Error("chap-0003 should be excluded (no .ok marker)")
	}
	for k := range got {
		if filepath.Base(k) == ".ok" {
			t.Errorf(".ok leaked into archive: %s", k)
		}
	}
}

// TestStageAndRename_LargeArchiveTakesOSZipPath forces the OS-zip
// branch by lowering the threshold below the existing archive's
// size. Verifies the existing entry survives and the new entry is
// added — the same correctness contract as the Go-zip path, just
// exercised through `os/exec` to `zip`.
func TestStageAndRename_LargeArchiveTakesOSZipPath(t *testing.T) {
	// Lower the threshold so any non-empty archive trips the OS path.
	saved := zip64SafeThreshold
	zip64SafeThreshold = 1
	t.Cleanup(func() { zip64SafeThreshold = saved })

	p := buildCBZ(t, map[string][]byte{
		"chap-0001/001.jpg": []byte("AAA"),
	})

	scratch := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(scratch, "chap-0001"), 0o755))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", "zzz-comments.png"), []byte("PNG"), 0o644))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", ".ok"), nil, 0o644))

	if err := StageAndRename(p, scratch); err != nil {
		t.Fatalf("StageAndRename via OS zip: %v", err)
	}

	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		got[f.Name] = b
	}
	want := map[string]string{
		"chap-0001/001.jpg":          "AAA",
		"chap-0001/zzz-comments.png": "PNG",
	}
	for k, v := range want {
		if string(got[k]) != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
	if _, leaked := got["chap-0001/.ok"]; leaked {
		t.Error(".ok marker leaked into OS-zip output")
	}
}

func TestStageAndRename_ReplacesExistingComments(t *testing.T) {
	// Archive already has an image AND an old comments page for chap-0001.
	p := buildCBZ(t, map[string][]byte{
		"chap-0001/001.jpg":          []byte("AAA"),
		"chap-0001/zzz-comments.png": []byte("OLD"),
	})

	// A refresh re-renders only the comments page (no images) into scratch.
	scratch := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(scratch, "chap-0001"), 0o755))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", "zzz-comments.png"), []byte("NEW"), 0o644))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", ".ok"), nil, 0o644))

	if err := StageAndRename(p, scratch); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	var commentEntries int
	var commentBody string
	var haveImage bool
	for _, f := range zr.File {
		switch f.Name {
		case "chap-0001/zzz-comments.png":
			commentEntries++
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			commentBody = string(b)
		case "chap-0001/001.jpg":
			haveImage = true
		}
	}
	if commentEntries != 1 {
		t.Fatalf("zzz-comments.png entries = %d, want 1 (replaced, not duplicated)", commentEntries)
	}
	if commentBody != "NEW" {
		t.Errorf("comment body = %q, want NEW (scratch should win)", commentBody)
	}
	if !haveImage {
		t.Error("chap-0001/001.jpg should be preserved untouched")
	}
}

// TestStageAndRename_ReplacesExistingCommentsViaOSZip mirrors
// TestStageAndRename_ReplacesExistingComments but forces the OS-zip
// path by lowering zip64SafeThreshold below any real archive size,
// proving that replace-not-duplicate holds for large back catalogues.
func TestStageAndRename_ReplacesExistingCommentsViaOSZip(t *testing.T) {
	// Force OS-zip path: any non-empty archive trips the threshold.
	saved := zip64SafeThreshold
	zip64SafeThreshold = 1
	t.Cleanup(func() { zip64SafeThreshold = saved })

	// Archive already has an image AND an old comments page for chap-0001.
	p := buildCBZ(t, map[string][]byte{
		"chap-0001/001.jpg":          []byte("AAA"),
		"chap-0001/zzz-comments.png": []byte("OLD"),
	})

	// A refresh re-renders only the comments page (no images) into scratch.
	scratch := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(scratch, "chap-0001"), 0o755))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", "zzz-comments.png"), []byte("NEW"), 0o644))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", ".ok"), nil, 0o644))

	if err := StageAndRename(p, scratch); err != nil {
		t.Fatalf("StageAndRename via OS zip: %v", err)
	}

	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	var commentEntries int
	var commentBody string
	var haveImage bool
	for _, f := range zr.File {
		switch f.Name {
		case "chap-0001/zzz-comments.png":
			commentEntries++
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			commentBody = string(b)
		case "chap-0001/001.jpg":
			haveImage = true
		}
	}
	if commentEntries != 1 {
		t.Fatalf("zzz-comments.png entries = %d, want 1 (replaced, not duplicated)", commentEntries)
	}
	if commentBody != "NEW" {
		t.Errorf("comment body = %q, want NEW (scratch should win)", commentBody)
	}
	if !haveImage {
		t.Error("chap-0001/001.jpg should be preserved untouched")
	}
}

func TestStageAndRename_CreatesFreshIfTargetMissing(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "fresh.cbz")
	scratch := t.TempDir()
	if err := os.MkdirAll(filepath.Join(scratch, "chap-0001"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(filepath.Join(scratch, "chap-0001", "001.jpg"), []byte("X"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(filepath.Join(scratch, "chap-0001", ".ok"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := StageAndRename(p, scratch); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatal("fresh.cbz not created:", err)
	}
}
