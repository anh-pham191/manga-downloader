package source

import (
	"context"
	"net/url"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
)

func TestParseSearch(t *testing.T) {
	hits, err := ParseSearch(loadFixture(t, "search.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Title != "Gintama" || hits[0].URL != "https://truyenqqko.com/truyen-tranh/gintama-216" {
		t.Fatalf("hit[0] = %+v", hits[0])
	}
	if hits[1].Title != "Gintama: 3-nen Z-gumi Ginpachi-sensei Tuuuunnn!!" {
		t.Fatalf("hit[1] title = %q", hits[1].Title)
	}
}

func TestParseSearch_Empty(t *testing.T) {
	hits, err := ParseSearch("\n  \n")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected no hits, got %d", len(hits))
	}
}

type recordingFetcher struct {
	gotURL     string
	gotReferer string
	gotForm    url.Values
	body       string
}

func (r *recordingFetcher) Get(_ context.Context, _ fetcher.Request) (*fetcher.Response, error) {
	return nil, nil
}
func (r *recordingFetcher) Post(_ context.Context, req fetcher.Request, form url.Values) (*fetcher.Response, error) {
	r.gotURL, r.gotReferer, r.gotForm = req.URL, req.Referer, form
	return &fetcher.Response{Body: []byte(r.body), ContentType: "text/html"}, nil
}

func TestSite_Search_PostsToEndpoint(t *testing.T) {
	rf := &recordingFetcher{body: loadFixture(t, "search.html")}
	s := &Site{Fetcher: rf}
	hits, err := s.Search(context.Background(), "https://truyenqqko.com/truyen-tranh/anything-1", "gintama")
	if err != nil {
		t.Fatal(err)
	}
	if rf.gotURL != "https://truyenqqko.com/frontend/search/search" {
		t.Fatalf("posted to %q", rf.gotURL)
	}
	if rf.gotReferer != "https://truyenqqko.com/" {
		t.Fatalf("referer %q", rf.gotReferer)
	}
	if rf.gotForm.Get("search") != "gintama" || rf.gotForm.Get("type") != "0" {
		t.Fatalf("form %v", rf.gotForm)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
}

type nilFetcher struct{}

func (nilFetcher) Get(_ context.Context, _ fetcher.Request) (*fetcher.Response, error) {
	return nil, nil
}
func (nilFetcher) Post(_ context.Context, _ fetcher.Request, _ url.Values) (*fetcher.Response, error) {
	return nil, nil
}

func TestSite_Search_NilResponse(t *testing.T) {
	s := &Site{Fetcher: nilFetcher{}}
	hits, err := s.Search(context.Background(), "https://truyenqqko.com/truyen-tranh/anything-1", "gintama")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected no hits, got %d", len(hits))
	}
}
