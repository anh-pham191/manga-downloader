package comments

import (
	"context"
	"io/ioutil"
	"net/url"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
)

type fixedFetcher struct {
	getBody  []byte
	postBody []byte
}

func (f *fixedFetcher) Get(_ context.Context, _ fetcher.Request) (*fetcher.Response, error) {
	return &fetcher.Response{Body: f.getBody, ContentType: "text/html"}, nil
}
func (f *fixedFetcher) Post(_ context.Context, _ fetcher.Request, _ url.Values) (*fetcher.Response, error) {
	return &fetcher.Response{Body: f.postBody, ContentType: "text/html"}, nil
}

func TestScrape_Page1FromChapterHTML(t *testing.T) {
	raw, err := ioutil.ReadFile("testdata/chapter-with-comments.html")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixedFetcher{getBody: raw, postBody: nil}

	cs, err := Scrape(context.Background(), "https://truyenqqko.com/truyen-tranh/hoc-vien-one-piece-13680-chap-1", f)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("got %d comments", len(cs))
	if len(cs) == 0 {
		t.Fatal("got 0 comments, want >= 1")
	}
	for i, c := range cs {
		if c.Name == "" {
			t.Errorf("comment[%d].Name empty", i)
		}
		// Body OR an emoji-bearing render can be empty if all the
		// content was emote <img> tags. That's acceptable.
	}
}
