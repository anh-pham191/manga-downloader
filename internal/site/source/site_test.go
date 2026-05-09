package source

import (
	"os"
	"path/filepath"
	"testing"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

func TestParseChapters(t *testing.T) {
	html := loadFixture(t, "manga.html")
	const mangaURL = "https://example.com/truyen-tranh/sample-manga"

	chapters, err := ParseChapters(html, mangaURL)
	if err != nil {
		t.Fatalf("ParseChapters: %v", err)
	}

	wantNumbers := []string{"1", "1.5", "2", "3"}
	if len(chapters) != len(wantNumbers) {
		t.Fatalf("got %d chapters, want %d: %#v", len(chapters), len(wantNumbers), chapters)
	}
	for i, want := range wantNumbers {
		if chapters[i].Number != want {
			t.Errorf("chapters[%d].Number = %q, want %q", i, chapters[i].Number, want)
		}
		if chapters[i].URL == "" {
			t.Errorf("chapters[%d].URL empty", i)
		}
	}

	// Relative hrefs must be resolved to absolute URLs.
	for i, c := range chapters {
		if got, want := c.URL[:8], "https://"; got != want {
			t.Errorf("chapters[%d].URL = %q, want absolute (https://...)", i, c.URL)
		}
	}

	// First entry must be chapter 1, last the highest. (Source page
	// lists newest-first; the parser flips to ascending so callers
	// can iterate naturally.)
	if chapters[0].Number != "1" {
		t.Errorf("first chapter = %q, want %q (ascending order)", chapters[0].Number, "1")
	}
	if chapters[len(chapters)-1].Number != "3" {
		t.Errorf("last chapter = %q, want %q", chapters[len(chapters)-1].Number, "3")
	}
}

func TestParseChapterImages(t *testing.T) {
	html := loadFixture(t, "chapter.html")
	const chapterURL = "https://example.com/truyen-tranh/sample-manga-chap-1"

	images, err := ParseChapterImages(html, chapterURL)
	if err != nil {
		t.Fatalf("ParseChapterImages: %v", err)
	}

	want := []string{
		"https://cdn.example.com/hxh/1/01.jpg",
		"https://cdn.example.com/hxh/1/02.jpg",
		"https://cdn.example.com/hxh/1/03.webp", // lazy-loaded via data-original
		"https://cdn.example.com/hxh/1/04.png",  // protocol-relative resolved
	}
	if len(images) != len(want) {
		t.Fatalf("got %d images, want %d: %#v", len(images), len(want), images)
	}
	for i, w := range want {
		if images[i].URL != w {
			t.Errorf("images[%d].URL = %q, want %q", i, images[i].URL, w)
		}
		if images[i].Referer != chapterURL {
			t.Errorf("images[%d].Referer = %q, want %q", i, images[i].Referer, chapterURL)
		}
	}
}

func TestParseChapterImages_RejectsEmpty(t *testing.T) {
	// A chapter page that returned zero images must be a hard error,
	// not a silent empty slice — otherwise we'd happily write `.done`
	// for a broken chapter. (Hazard H8 in PLAN.md.)
	_, err := ParseChapterImages("<html><body>nothing here</body></html>", "https://example.com/chap-1")
	if err == nil {
		t.Fatal("ParseChapterImages on empty page returned nil error, want error")
	}
}
