package downloader

import (
	"context"
	"errors"
	"image/png"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/site"
)

// TestExtFromURL_StripsQueryAndFragment proves the source CDN's
// cache-buster query strings don't leak into local filenames. Past
// behaviour produced filenames like `001.jpg?r=r8645456`, which
// broke archive.Inspect's image-entry regex and caused resyncs to
// see "empty" archives and re-download everything.
func TestExtFromURL_StripsQueryAndFragment(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"plain jpg", "https://cdn.example.com/path/001.jpg", "jpg"},
		{"jpg with cache-buster query", "https://cdn.example.com/001.jpg?r=r8645456", "jpg"},
		{"webp with multiple query params", "https://cdn.example.com/a.webp?v=1&t=2", "webp"},
		{"png with fragment", "https://cdn.example.com/img.PNG#top", "png"},
		{"jpeg with query AND fragment", "https://cdn.example.com/x.jpeg?q=1#f", "jpeg"},
		{"upper-case extension", "https://cdn.example.com/A.JPG", "jpg"},
		{"no extension", "https://cdn.example.com/file?q=1", "jpg"},
		{"trailing dot", "https://cdn.example.com/file.", "jpg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extFromURL(tc.url); got != tc.want {
				t.Errorf("extFromURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

// --- missing-image tolerance -------------------------------------------

type scriptedFetcher struct {
	chapterHTML string
	failURLs    map[string]error // image URL -> error to return
	got         []string
}

func (s *scriptedFetcher) Get(_ context.Context, req fetcher.Request) (*fetcher.Response, error) {
	s.got = append(s.got, req.URL)
	if err, ok := s.failURLs[req.URL]; ok {
		return nil, err
	}
	if strings.HasSuffix(req.URL, ".jpg") {
		return &fetcher.Response{Body: []byte("\xff\xd8\xff\xd9"), ContentType: "image/jpeg"}, nil
	}
	return &fetcher.Response{Body: []byte(s.chapterHTML), ContentType: "text/html"}, nil
}
func (s *scriptedFetcher) Post(_ context.Context, _ fetcher.Request, _ url.Values) (*fetcher.Response, error) {
	return &fetcher.Response{}, nil
}

const threeImageChapter = `<div class="page-chapter"><img src="https://cdn.example/a/1.jpg"></div>
<div class="page-chapter"><img src="https://cdn.example/a/2.jpg"></div>
<div class="page-chapter"><img src="https://cdn.example/a/3.jpg"></div>`

func TestFetchChapterImages_OneMissingGetsPlaceholder(t *testing.T) {
	dir := t.TempDir()
	f := &scriptedFetcher{chapterHTML: threeImageChapter, failURLs: map[string]error{
		"https://cdn.example/a/2.jpg": &net.DNSError{Err: "no such host", Name: "cdn.example"},
	}}
	missing, err := FetchChapterImages(context.Background(), site.Chapter{Number: "1", URL: "https://x.example/c/1"}, dir, f)
	if err != nil {
		t.Fatalf("chapter must complete with one missing image, got %v", err)
	}
	if len(missing) != 1 || missing[0].Index != 2 || missing[0].URL != "https://cdn.example/a/2.jpg" || missing[0].Err == nil {
		t.Fatalf("missing = %+v", missing)
	}
	for _, name := range []string{"001.jpg", "003.jpg"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s should exist: %v", name, err)
		}
	}
	ph, err := os.Open(filepath.Join(dir, "002.png"))
	if err != nil {
		t.Fatalf("placeholder 002.png missing: %v", err)
	}
	defer ph.Close()
	if _, err := png.Decode(ph); err != nil {
		t.Fatalf("placeholder is not a PNG: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "002.jpg")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("no real 002.jpg should be written for a missing image")
	}
}

func TestFetchChapterImages_AllMissingFails(t *testing.T) {
	dir := t.TempDir()
	dns := &net.DNSError{Err: "no such host"}
	f := &scriptedFetcher{chapterHTML: threeImageChapter, failURLs: map[string]error{
		"https://cdn.example/a/1.jpg": dns, "https://cdn.example/a/2.jpg": dns, "https://cdn.example/a/3.jpg": dns,
	}}
	_, err := FetchChapterImages(context.Background(), site.Chapter{Number: "1", URL: "https://x.example/c/1"}, dir, f)
	if err == nil {
		t.Fatal("chapter with every image missing must fail")
	}
}

func TestFetchChapterImages_CloudflareIsFatal(t *testing.T) {
	dir := t.TempDir()
	f := &scriptedFetcher{chapterHTML: threeImageChapter, failURLs: map[string]error{
		"https://cdn.example/a/2.jpg": fetcher.ErrCloudflareExpired,
	}}
	_, err := FetchChapterImages(context.Background(), site.Chapter{Number: "1", URL: "https://x.example/c/1"}, dir, f)
	if !errors.Is(err, fetcher.ErrCloudflareExpired) {
		t.Fatalf("want ErrCloudflareExpired, got %v", err)
	}
	if len(f.got) != 3 { // chapter page + image 1 + image 2; must stop, not continue to image 3
		t.Fatalf("must stop at the Cloudflare error; requests: %v", f.got)
	}
}
