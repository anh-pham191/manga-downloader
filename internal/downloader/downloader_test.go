package downloader

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/site"
)

// fakeSite returns canned chapters and images. Lets tests bypass the
// network and the real selectors.
type fakeSite struct {
	chapters []site.Chapter
	images   map[string][]site.ImageRef // keyed by chapter URL
}

func (f *fakeSite) ListChapters(_ context.Context, _ string) ([]site.Chapter, error) {
	return f.chapters, nil
}

func (f *fakeSite) ChapterImages(_ context.Context, c site.Chapter) ([]site.ImageRef, error) {
	imgs, ok := f.images[c.URL]
	if !ok {
		return nil, errors.New("no images for chapter")
	}
	return imgs, nil
}

// fakeFetcher returns deterministic responses keyed by URL. Each
// entry is a small queue so tests can script "fail twice, then
// succeed" patterns.
type fakeFetcher struct {
	mu        sync.Mutex
	responses map[string][]fakeResp
	calls     map[string]int
}

type fakeResp struct {
	body []byte
	err  error
}

func (f *fakeFetcher) Get(_ context.Context, req fetcher.Request) (*fetcher.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[req.URL]++
	queue := f.responses[req.URL]
	if len(queue) == 0 {
		return nil, errors.New("no scripted response")
	}
	r := queue[0]
	if len(queue) > 1 {
		f.responses[req.URL] = queue[1:]
	}
	if r.err != nil {
		return nil, r.err
	}
	return &fetcher.Response{Body: r.body, ContentType: "image/jpeg"}, nil
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{
		responses: map[string][]fakeResp{},
		calls:     map[string]int{},
	}
}

func twoChapterPlan(t *testing.T) (*fakeSite, []site.Chapter) {
	t.Helper()
	chapters := []site.Chapter{
		{Number: "1", URL: "https://example.com/m/chap-1"},
		{Number: "2", URL: "https://example.com/m/chap-2"},
	}
	images := map[string][]site.ImageRef{
		"https://example.com/m/chap-1": {
			{URL: "https://cdn.example.com/1/01.jpg", Referer: "https://example.com/m/chap-1"},
			{URL: "https://cdn.example.com/1/02.jpg", Referer: "https://example.com/m/chap-1"},
		},
		"https://example.com/m/chap-2": {
			{URL: "https://cdn.example.com/2/01.jpg", Referer: "https://example.com/m/chap-2"},
		},
	}
	return &fakeSite{chapters: chapters, images: images}, chapters
}

func TestRun_HappyPath(t *testing.T) {
	out := t.TempDir()
	s, _ := twoChapterPlan(t)
	f := newFakeFetcher()
	f.responses["https://cdn.example.com/1/01.jpg"] = []fakeResp{{body: []byte("img1a")}}
	f.responses["https://cdn.example.com/1/02.jpg"] = []fakeResp{{body: []byte("img1b")}}
	f.responses["https://cdn.example.com/2/01.jpg"] = []fakeResp{{body: []byte("img2a")}}

	d := &Downloader{Site: s, Fetcher: f, OutDir: out, MangaSlug: "m", Concurrency: 1}
	res, err := d.Run(context.Background(), "https://example.com/m")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Failed != 0 {
		t.Errorf("Failed = %d, want 0", res.Failed)
	}
	if res.Completed != 2 {
		t.Errorf("Completed = %d, want 2", res.Completed)
	}

	for _, p := range []string{
		filepath.Join(out, "m", "chap-0001", "001.jpg"),
		filepath.Join(out, "m", "chap-0001", "002.jpg"),
		filepath.Join(out, "m", "chap-0001", ".done"),
		filepath.Join(out, "m", "chap-0002", "001.jpg"),
		filepath.Join(out, "m", "chap-0002", ".done"),
	} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("missing expected file %s: %v", p, err)
		}
	}

	// .part files must have been cleaned up via rename.
	matches, _ := filepath.Glob(filepath.Join(out, "m", "*", "*.part"))
	if len(matches) != 0 {
		t.Errorf("leftover .part files: %v", matches)
	}
}

func TestRun_PermanentFailure_NoDone(t *testing.T) {
	out := t.TempDir()
	s, _ := twoChapterPlan(t)
	f := newFakeFetcher()
	f.responses["https://cdn.example.com/1/01.jpg"] = []fakeResp{{body: []byte("img1a")}}
	f.responses["https://cdn.example.com/1/02.jpg"] = []fakeResp{{err: errors.New("boom")}}
	f.responses["https://cdn.example.com/2/01.jpg"] = []fakeResp{{body: []byte("img2a")}}

	d := &Downloader{Site: s, Fetcher: f, OutDir: out, MangaSlug: "m", Concurrency: 1}
	res, _ := d.Run(context.Background(), "https://example.com/m")

	if res.Failed != 1 {
		t.Errorf("Failed = %d, want 1", res.Failed)
	}
	if res.Completed != 1 {
		t.Errorf("Completed = %d, want 1", res.Completed)
	}

	if _, err := os.Stat(filepath.Join(out, "m", "chap-0001", ".done")); !os.IsNotExist(err) {
		t.Errorf(".done unexpectedly present for failed chapter: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "m", "chap-0002", ".done")); err != nil {
		t.Errorf(".done missing for succeeded chapter: %v", err)
	}
}

func TestRun_Resume_SkipsCompleted(t *testing.T) {
	out := t.TempDir()
	s, _ := twoChapterPlan(t)
	f := newFakeFetcher()
	// Only chapter 2 has scripted responses; if Resume doesn't skip
	// chapter 1 the test will explode on the missing scripted response.
	f.responses["https://cdn.example.com/2/01.jpg"] = []fakeResp{{body: []byte("img2a")}}

	// Pre-populate chapter 1 as if a previous run completed it.
	if err := os.MkdirAll(filepath.Join(out, "m", "chap-0001"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "m", "chap-0001", ".done"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	d := &Downloader{Site: s, Fetcher: f, OutDir: out, MangaSlug: "m", Concurrency: 1, Resume: true}
	res, err := d.Run(context.Background(), "https://example.com/m")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", res.Skipped)
	}
	if res.Completed != 1 {
		t.Errorf("Completed = %d, want 1", res.Completed)
	}
	if f.calls["https://cdn.example.com/1/01.jpg"] != 0 {
		t.Errorf("resumed chapter still issued requests: %d", f.calls["https://cdn.example.com/1/01.jpg"])
	}
}

func TestRun_RangeFilter(t *testing.T) {
	out := t.TempDir()
	chapters := []site.Chapter{
		{Number: "1", URL: "https://example.com/m/chap-1"},
		{Number: "2", URL: "https://example.com/m/chap-2"},
		{Number: "3", URL: "https://example.com/m/chap-3"},
	}
	images := map[string][]site.ImageRef{
		"https://example.com/m/chap-2": {{URL: "https://cdn.example.com/2/01.jpg", Referer: "https://example.com/m/chap-2"}},
	}
	s := &fakeSite{chapters: chapters, images: images}
	f := newFakeFetcher()
	f.responses["https://cdn.example.com/2/01.jpg"] = []fakeResp{{body: []byte("img2a")}}

	d := &Downloader{Site: s, Fetcher: f, OutDir: out, MangaSlug: "m", Concurrency: 1, From: 2, To: 2}
	res, err := d.Run(context.Background(), "https://example.com/m")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Completed != 1 {
		t.Errorf("Completed = %d, want 1 (only chap-2)", res.Completed)
	}
	if _, err := os.Stat(filepath.Join(out, "m", "chap-0001")); !os.IsNotExist(err) {
		t.Error("chap-0001 was created despite --from 2")
	}
}
