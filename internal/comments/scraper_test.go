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

func TestScrape_PullsPage2(t *testing.T) {
	p1, err := ioutil.ReadFile("testdata/chapter-with-comments.html")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := ioutil.ReadFile("testdata/page2-fragment.html")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixedFetcher{getBody: p1, postBody: p2}

	cs, err := Scrape(context.Background(), "https://example.com/chap-1", f)
	if err != nil {
		t.Fatal(err)
	}

	p1only := &fixedFetcher{getBody: p1, postBody: nil}
	just1, err := Scrape(context.Background(), "https://example.com/chap-1", p1only)
	if err != nil {
		t.Fatal(err)
	}

	if len(cs) <= len(just1) {
		t.Fatalf("page-2 added 0 comments: %d vs %d", len(cs), len(just1))
	}
	t.Logf("page-2 merge: %d vs %d", len(cs), len(just1))
}

func TestScrape_StripsEmoteImages(t *testing.T) {
	fixture := []byte(`
<html><body>
  <input id="book_id" value="1"/>
  <input id="episode_id" value="2"/>
  <div id="comment_list">
    <article class="info-comment comment-main-level child_1 parent_0">
      <strong class="level name_5">user</strong>
      <span class="title-user-comment title-member level_5">Cấp 5</span>
      <div class="content-comment">hello <img class="lazy-image" alt="emo" data-src="x"/> world</div>
      <span class="total-like-comment">3</span>
    </article>
  </div>
</body></html>`)
	f := &fixedFetcher{getBody: fixture}
	cs, err := Scrape(context.Background(), "https://x/", f)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 {
		t.Fatalf("len = %d", len(cs))
	}
	if cs[0].Body != "hello  world" {
		t.Errorf("Body = %q", cs[0].Body)
	}
	if cs[0].LikeCount != 3 {
		t.Errorf("LikeCount = %d", cs[0].LikeCount)
	}
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
