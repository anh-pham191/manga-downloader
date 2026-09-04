package pipeline

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/registry"
	"github.com/anhpham/downloader/internal/site"
)

// scriptedSite returns a per-host error or chapter list for ListChapters.
type scriptedSite struct {
	errByHost map[string]error
	chapters  []site.Chapter
	calls     []string
}

func (s *scriptedSite) ListChapters(_ context.Context, mangaURL string) ([]site.Chapter, error) {
	s.calls = append(s.calls, mangaURL)
	u, _ := url.Parse(mangaURL)
	if err, ok := s.errByHost[u.Host]; ok && err != nil {
		return nil, err
	}
	return s.chapters, nil
}
func (s *scriptedSite) ChapterImages(_ context.Context, _ site.Chapter) ([]site.ImageRef, error) {
	return nil, nil
}
func (s *scriptedSite) Search(_ context.Context, _, _ string) ([]site.SearchHit, error) {
	return nil, nil
}

type nopFetcher struct{}

func (nopFetcher) Get(_ context.Context, _ fetcher.Request) (*fetcher.Response, error) {
	return &fetcher.Response{}, nil
}
func (nopFetcher) Post(_ context.Context, _ fetcher.Request, _ url.Values) (*fetcher.Response, error) {
	return &fetcher.Response{}, nil
}

func newReg(t *testing.T, urls map[string]string) *registry.Registry {
	r, err := registry.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for n, u := range urls {
		r.Upsert(n, u)
	}
	return r
}

func TestUpdateAll_HappyPath(t *testing.T) {
	reg := newReg(t, map[string]string{
		"A": "https://old.example/truyen-tranh/a-1",
		"B": "https://old.example/truyen-tranh/b-2",
	})
	var ran []string
	res, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root:     reg.Root(),
		Registry: reg,
		Site:     &scriptedSite{chapters: []site.Chapter{{Number: "1", URL: "x"}}},
		Fetcher:  nopFetcher{},
		Runner: func(_ context.Context, o Opts) (Result, error) {
			ran = append(ran, o.Name)
			if o.Mode != Resume {
				t.Fatalf("mode must be Resume, got %v", o.Mode)
			}
			return Result{NewChapters: 3}, nil
		},
		Now: func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 2 || ran[0] != "A" || ran[1] != "B" {
		t.Fatalf("ran %v", ran)
	}
	if res.Outcomes[0].NewChapters != 3 || res.Outcomes[0].Status != "ok" {
		t.Fatalf("outcome %+v", res.Outcomes[0])
	}
	e, _ := reg.Get("A")
	if e.LastSynced.IsZero() {
		t.Fatal("LastSynced not touched")
	}
	if res.DomainMoved {
		t.Fatal("no domain move expected")
	}
}

func TestUpdateAll_CloudflareStopsImmediately(t *testing.T) {
	reg := newReg(t, map[string]string{"A": "https://old.example/a", "B": "https://old.example/b"})
	asked := false
	_, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root:      reg.Root(),
		Registry:  reg,
		Site:      &scriptedSite{errByHost: map[string]error{"old.example": fetcher.ErrCloudflareExpired}},
		Fetcher:   nopFetcher{},
		AskDomain: func(string, error) string { asked = true; return "new.example" },
		Runner:    func(context.Context, Opts) (Result, error) { t.Fatal("runner must not run"); return Result{}, nil },
	})
	if !errors.Is(err, fetcher.ErrCloudflareExpired) {
		t.Fatalf("want ErrCloudflareExpired, got %v", err)
	}
	if asked {
		t.Fatal("must never ask for a domain on a Cloudflare error")
	}
}

func TestUpdateAll_DomainMovedRewritesAndContinues(t *testing.T) {
	reg := newReg(t, map[string]string{
		"A": "https://old.example/truyen-tranh/a-1",
		"B": "https://old.example/truyen-tranh/b-2",
	})
	s := &scriptedSite{
		errByHost: map[string]error{"old.example": &net.DNSError{Err: "no such host", Name: "old.example"}},
		chapters:  []site.Chapter{{Number: "1", URL: "x"}},
	}
	var gotOld string
	var ranURLs []string
	res, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root:      reg.Root(),
		Registry:  reg,
		Site:      s,
		Fetcher:   nopFetcher{},
		AskDomain: func(oldHost string, _ error) string { gotOld = oldHost; return "new.example" },
		Runner: func(_ context.Context, o Opts) (Result, error) {
			ranURLs = append(ranURLs, o.MangaURL)
			return Result{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotOld != "old.example" {
		t.Fatalf("AskDomain got %q", gotOld)
	}
	if !res.DomainMoved || res.NewHost != "new.example" {
		t.Fatalf("result %+v", res)
	}
	for _, u := range ranURLs {
		pu, _ := url.Parse(u)
		if pu.Host != "new.example" {
			t.Fatalf("runner got un-rewritten url %s", u)
		}
	}
	e, _ := reg.Get("B")
	if e.URL != "https://new.example/truyen-tranh/b-2" {
		t.Fatalf("registry not rewritten: %s", e.URL)
	}
	// registry must be persisted
	r2, _ := registry.Load(reg.Root())
	e2, _ := r2.Get("A")
	if e2.URL != "https://new.example/truyen-tranh/a-1" {
		t.Fatalf("rewrite not saved: %s", e2.URL)
	}
	// Preflight must probe only the first registered manga (twice: once
	// against the old host, once to verify the candidate) before the
	// main loop runs anything.
	wantCalls := []string{
		"https://old.example/truyen-tranh/a-1",
		"https://new.example/truyen-tranh/a-1",
	}
	if len(s.calls) != len(wantCalls) {
		t.Fatalf("ListChapters calls = %v, want %v", s.calls, wantCalls)
	}
	for i, want := range wantCalls {
		if s.calls[i] != want {
			t.Fatalf("call %d = %q, want %q", i, s.calls[i], want)
		}
	}
}

func TestUpdateAll_DomainMovedAbortWhenNoAnswer(t *testing.T) {
	reg := newReg(t, map[string]string{"A": "https://old.example/a"})
	_, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root:      reg.Root(),
		Registry:  reg,
		Site:      &scriptedSite{errByHost: map[string]error{"old.example": &net.DNSError{Err: "no such host"}}},
		Fetcher:   nopFetcher{},
		AskDomain: func(string, error) string { return "" },
	})
	if !errors.Is(err, ErrDomainMoved) {
		t.Fatalf("want ErrDomainMoved, got %v", err)
	}
}

func TestUpdateAll_NewHostAlsoFailsIsError(t *testing.T) {
	reg := newReg(t, map[string]string{"A": "https://old.example/a"})
	s := &scriptedSite{errByHost: map[string]error{
		"old.example": &net.DNSError{Err: "no such host"},
		"new.example": &net.DNSError{Err: "no such host"},
	}}
	_, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root: reg.Root(), Registry: reg, Site: s, Fetcher: nopFetcher{},
		AskDomain: func(string, error) string { return "new.example" },
	})
	if !errors.Is(err, ErrDomainMoved) {
		t.Fatalf("want ErrDomainMoved, got %v", err)
	}
	e, _ := reg.Get("A")
	if e.URL != "https://old.example/a" {
		t.Fatal("registry must not be rewritten when the new host also fails")
	}
}

func TestUpdateAll_PerMangaFailuresContinue(t *testing.T) {
	reg := newReg(t, map[string]string{
		"A": "https://h.example/a", "B": "https://h.example/b", "C": "https://h.example/c",
	})
	res, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root: reg.Root(), Registry: reg,
		Site:    &scriptedSite{chapters: []site.Chapter{{Number: "1", URL: "x"}}},
		Fetcher: nopFetcher{},
		Runner: func(_ context.Context, o Opts) (Result, error) {
			switch o.Name {
			case "A":
				return Result{}, ErrNoArchive
			case "B":
				return Result{}, ErrAnotherInstance
			}
			return Result{NewChapters: 1}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "no-archive", "B": "busy", "C": "ok"}
	for _, o := range res.Outcomes {
		if o.Status != want[o.Name] {
			t.Fatalf("%s status %q want %q", o.Name, o.Status, want[o.Name])
		}
	}
}

func TestUpdateAll_CloudflareMidLoopStops(t *testing.T) {
	reg := newReg(t, map[string]string{"A": "https://h.example/a", "B": "https://h.example/b"})
	var ran []string
	res, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root: reg.Root(), Registry: reg,
		Site:    &scriptedSite{chapters: []site.Chapter{{Number: "1", URL: "x"}}},
		Fetcher: nopFetcher{},
		Runner: func(_ context.Context, o Opts) (Result, error) {
			ran = append(ran, o.Name)
			return Result{}, fetcher.ErrCloudflareExpired
		},
	})
	if !errors.Is(err, fetcher.ErrCloudflareExpired) {
		t.Fatalf("want cloudflare error, got %v", err)
	}
	if len(ran) != 1 {
		t.Fatalf("must stop after first cloudflare failure, ran %v", ran)
	}
	if len(res.Outcomes) != 1 || res.Outcomes[0].Status != "failed" {
		t.Fatalf("outcomes %+v", res.Outcomes)
	}
}

func TestUpdateAll_EmptyRegistry(t *testing.T) {
	reg := newReg(t, nil)
	res, err := UpdateAll(context.Background(), UpdateAllOpts{Root: reg.Root(), Registry: reg, Site: &scriptedSite{}, Fetcher: nopFetcher{}})
	if err != nil || len(res.Outcomes) != 0 {
		t.Fatalf("empty registry should be a no-op, got %+v %v", res, err)
	}
}
