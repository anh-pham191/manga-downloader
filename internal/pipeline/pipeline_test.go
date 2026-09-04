package pipeline

import (
	"context"
	"errors"
	"io/ioutil"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gofrs/flock"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/site"
)

type fakeSite struct{ chs []site.Chapter }

func (f *fakeSite) ListChapters(_ context.Context, _ string) ([]site.Chapter, error) {
	return f.chs, nil
}
func (f *fakeSite) ChapterImages(_ context.Context, _ site.Chapter) ([]site.ImageRef, error) {
	return []site.ImageRef{{URL: "https://example.com/x.jpg", Referer: "https://example.com/"}}, nil
}
func (f *fakeSite) Search(_ context.Context, _, _ string) ([]site.SearchHit, error) {
	return nil, nil
}

type fakeFetcher struct {
	chapterHTML []byte
	imageBytes  []byte
}

func (f *fakeFetcher) Get(_ context.Context, req fetcher.Request) (*fetcher.Response, error) {
	if strings.HasSuffix(req.URL, ".jpg") {
		return &fetcher.Response{Body: f.imageBytes, ContentType: "image/jpeg"}, nil
	}
	return &fetcher.Response{Body: f.chapterHTML, ContentType: "text/html"}, nil
}
func (f *fakeFetcher) Post(_ context.Context, _ fetcher.Request, _ url.Values) (*fetcher.Response, error) {
	return &fetcher.Response{Body: nil, ContentType: "text/html"}, nil
}

func TestRun_FreshSyncManga(t *testing.T) {
	raw, err := ioutil.ReadFile("../comments/testdata/chapter-with-comments.html")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	opts := Opts{
		Mode:        SyncManga,
		MangaURL:    "https://example.com/m",
		Root:        root,
		Name:        "m",
		Concurrency: 2,
		Site:        &fakeSite{chs: []site.Chapter{{Number: "1", URL: "https://example.com/m-chap-1"}}},
		Fetcher:     &fakeFetcher{chapterHTML: raw, imageBytes: []byte("FAKEJPEG")},
		Logger:      log.New(os.Stderr, "", 0),
	}
	if err := Run(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "m.cbz")); err != nil {
		t.Fatal("expected m.cbz to exist:", err)
	}
}

func TestRun_ResumeOnMissingArchiveReturnsErrNoArchive(t *testing.T) {
	root := t.TempDir()
	opts := Opts{
		Mode:        Resume,
		MangaURL:    "x",
		Root:        root,
		Name:        "m",
		Concurrency: 1,
		Site:        &fakeSite{chs: []site.Chapter{{Number: "1", URL: "u"}}},
		Fetcher:     &fakeFetcher{},
		Logger:      log.New(os.Stderr, "", 0),
	}
	err := Run(context.Background(), opts)
	if !errors.Is(err, ErrNoArchive) {
		t.Fatalf("err = %v, want ErrNoArchive", err)
	}
}

func TestRun_ConcurrentInvocationFailsFast(t *testing.T) {
	root := t.TempDir()
	name := "m"
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(root, name+".cbz.lock")
	held := flock.New(lockPath)
	if ok, _ := held.TryLock(); !ok {
		t.Fatal("could not pre-acquire lock")
	}
	defer held.Unlock()

	opts := Opts{
		Mode:        SyncManga,
		MangaURL:    "x",
		Root:        root,
		Name:        name,
		Concurrency: 1,
		Site:        &fakeSite{},
		Fetcher:     &fakeFetcher{},
		Logger:      log.New(os.Stderr, "", 0),
	}
	err := Run(context.Background(), opts)
	if !errors.Is(err, ErrAnotherInstance) {
		t.Fatalf("err = %v, want ErrAnotherInstance", err)
	}
}
