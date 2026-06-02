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

// pageAwareFetcher serves a different POST body per "page" form value
// and records which pages were requested.
type pageAwareFetcher struct {
	getBody []byte
	pages   map[string][]byte
	posts   []string
}

func (f *pageAwareFetcher) Get(_ context.Context, _ fetcher.Request) (*fetcher.Response, error) {
	return &fetcher.Response{Body: f.getBody, ContentType: "text/html"}, nil
}
func (f *pageAwareFetcher) Post(_ context.Context, _ fetcher.Request, form url.Values) (*fetcher.Response, error) {
	p := form.Get("page")
	f.posts = append(f.posts, p)
	return &fetcher.Response{Body: f.pages[p], ContentType: "text/html"}, nil
}

func commentFrag(name string) []byte {
	return []byte(`<div class="comment_list">
  <article class="info-comment comment-main-level child_1 parent_0">
    <strong class="level name_5">` + name + `</strong>
    <span class="title-user-comment title-member level_5">Cấp 5</span>
    <div class="content-comment">body of ` + name + `</div>
    <span class="total-like-comment">1</span>
  </article>
</div>`)
}

func TestScrape_MergesPagesAndStopsOnEmpty(t *testing.T) {
	page1 := []byte(`<html><body>
  <input id="book_id" value="1"/>
  <input id="episode_id" value="2"/>
  <div id="comment_list">
    <article class="info-comment comment-main-level child_1 parent_0">
      <strong class="level name_5">p1user</strong>
      <span class="title-user-comment title-member level_5">Cấp 5</span>
      <div class="content-comment">page one</div>
      <span class="total-like-comment">1</span>
    </article>
  </div>
</body></html>`)

	f := &pageAwareFetcher{
		getBody: page1,
		pages: map[string][]byte{
			"2": commentFrag("p2user"),
			"3": commentFrag("p3user"),
			"4": []byte(`<div class="comment_list"></div>`), // valid HTML, zero comments → stop
			"5": commentFrag("p5user"),                      // must never be requested
		},
	}

	cs, err := Scrape(context.Background(), "https://example.com/chap-1", f)
	if err != nil {
		t.Fatal(err)
	}

	// page1 + page2 + page3 = 3 comments; page 4 empty stops the loop.
	if len(cs) != 3 {
		t.Fatalf("merged comments = %d, want 3", len(cs))
	}
	// Pages 2,3,4 requested; page 5 never reached (early-stop after empty page 4).
	if len(f.posts) != 3 {
		t.Fatalf("POSTed pages = %v, want exactly [2 3 4]", f.posts)
	}
	if f.posts[len(f.posts)-1] != "4" {
		t.Fatalf("last POSTed page = %q, want 4", f.posts[len(f.posts)-1])
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
