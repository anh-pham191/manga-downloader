package comments

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/url"
	"strings"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
)

// replyFetcher serves the chapter page on GET, reply fragments on POST
// keyed by "parent_id/page", and emote images on GET by URL. It records
// every POST so tests can assert which threads were (not) requested.
type replyFetcher struct {
	chapter []byte
	replies map[string][]byte // "111/1" → fragment
	failFor map[string]bool   // parent_id → POST errors
	images  map[string][]byte
	posts   []string
}

func (f *replyFetcher) Get(_ context.Context, r fetcher.Request) (*fetcher.Response, error) {
	if b, ok := f.images[r.URL]; ok {
		return &fetcher.Response{Body: b}, nil
	}
	return &fetcher.Response{Body: f.chapter}, nil
}

func (f *replyFetcher) Post(_ context.Context, _ fetcher.Request, form url.Values) (*fetcher.Response, error) {
	pid, page := form.Get("parent_id"), form.Get("page")
	if page == "" {
		page = "1"
	}
	key := pid + "/" + page
	f.posts = append(f.posts, key)
	if f.failFor[pid] {
		return nil, errors.New("unexpected status 500")
	}
	return &fetcher.Response{Body: f.replies[key]}, nil
}

// topComment mirrors the real top-level article: id in child_<id>, an
// empty <strong> mention slot, and a "N phản hồi" expander only when
// the comment has replies.
func topComment(id int, name, body string, replies int) string {
	link := ""
	if replies > 0 {
		link = fmt.Sprintf(`<div class="text-list-reply load_child_%d"><a href="javascript:loadReply(%d)"><i class="fa fa-long-arrow-alt-right"></i> %d phản hồi</a></div>`, id, id, replies)
	}
	return fmt.Sprintf(`<article class="info-comment child_%d parent_0 comment-main-level">
  <strong class="level name_5">%s</strong><span class="title-user-comment title-member level_5">Cấp 5</span>
  <div class="content-comment"><strong></strong> %s</div>
  <div class="action-comment"><span class="total-like-comment">0</span>%s</div>
</article>`, id, name, body, link)
}

// replyArticle mirrors a reply from POST parent_id=<id>: same markup,
// parent_<id>, and the replied-to name in the content's <strong>.
func replyArticle(id, parent int, name, to, body string) string {
	return fmt.Sprintf(`<article class="info-comment child_%d parent_%d comment-main-level">
  <strong class="level name_3">%s</strong><span class="title-user-comment title-member level_3">Cấp 3</span>
  <div class="content-comment"><strong>%s</strong> %s</div>
  <div class="action-comment"><span class="total-like-comment">2</span></div>
</article>`, id, parent, name, to, body)
}

func chapterHTML(articles ...string) []byte {
	return []byte(`<html><body><input id="book_id" value="1"/><input id="episode_id" value="2"/><input id="team_id" value="0"/>
<div class="list-comment">` + strings.Join(articles, "\n") + `</div></body></html>`)
}

func TestScrape_FetchesRepliesOnlyForThreadsWithReplies(t *testing.T) {
	resetEmoteCache()
	f := &replyFetcher{
		chapter: chapterHTML(
			topComment(111, "alice", "first", 2),
			topComment(222, "bob", "second", 0),
		),
		replies: map[string][]byte{
			"111/1": []byte(replyArticle(901, 111, "carol", "alice", "agreed") + replyArticle(902, 111, "dave", "carol", "me too")),
		},
	}
	cs, err := Scrape(context.Background(), chapURL, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 {
		t.Fatalf("top-level = %d, want 2 (replies must not leak into the top level)", len(cs))
	}
	r := cs[0].Replies
	if len(r) != 2 {
		t.Fatalf("alice replies = %d, want 2", len(r))
	}
	if r[0].Name != "carol" || r[0].ReplyTo != "alice" || r[0].Body != "agreed" || r[0].LikeCount != 2 {
		t.Errorf("reply[0] = %+v", r[0])
	}
	if r[1].ReplyTo != "carol" {
		t.Errorf("reply[1].ReplyTo = %q, want carol", r[1].ReplyTo)
	}
	if cs[0].ReplyTo != "" || cs[0].Body != "first" {
		t.Errorf("top-level mention/body = %q / %q", cs[0].ReplyTo, cs[0].Body)
	}
	if len(cs[1].Replies) != 0 {
		t.Errorf("bob has no expander but got replies")
	}
	for _, p := range f.posts {
		if strings.HasPrefix(p, "222/") {
			t.Errorf("POSTed replies for a comment with no expander: %v", f.posts)
		}
	}
}

func TestScrape_ReplyPagesFollowedUntilCountOrNoNewIDs(t *testing.T) {
	resetEmoteCache()
	f := &replyFetcher{
		chapter: chapterHTML(topComment(111, "alice", "x", 3), topComment(333, "erin", "y", 5)),
		replies: map[string][]byte{
			// alice: 2 on page 1, the 3rd on page 2 → stop at the count.
			"111/1": []byte(replyArticle(1, 111, "a", "alice", "1") + replyArticle(2, 111, "b", "alice", "2")),
			"111/2": []byte(replyArticle(3, 111, "c", "alice", "3")),
			// erin: server ignores page and repeats page 1 → stop, no dupes.
			"333/1": []byte(replyArticle(7, 333, "g", "erin", "7") + replyArticle(8, 333, "h", "erin", "8")),
			"333/2": []byte(replyArticle(7, 333, "g", "erin", "7") + replyArticle(8, 333, "h", "erin", "8")),
		},
	}
	cs, err := Scrape(context.Background(), chapURL, f)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(cs[0].Replies); n != 3 {
		t.Errorf("alice replies = %d, want 3", n)
	}
	if n := len(cs[1].Replies); n != 2 {
		t.Errorf("erin replies = %d, want 2 (deduped)", n)
	}
	for _, p := range f.posts {
		if p == "111/3" || p == "333/3" {
			t.Errorf("kept paging past the stop condition: %v", f.posts)
		}
	}
}

func TestScrape_ReplyFetchFailureKeepsParent(t *testing.T) {
	resetEmoteCache()
	f := &replyFetcher{
		chapter: chapterHTML(topComment(111, "alice", "x", 1)),
		failFor: map[string]bool{"111": true},
	}
	cs, err := Scrape(context.Background(), chapURL, f)
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || len(cs[0].Replies) != 0 {
		t.Fatalf("got %+v, want parent kept with no replies", cs)
	}
}

func TestScrape_RepliesGetEmotes(t *testing.T) {
	resetEmoteCache()
	f := &replyFetcher{
		chapter: chapterHTML(topComment(111, "alice", "x", 1)),
		replies: map[string][]byte{
			"111/1": []byte(replyArticle(1, 111, "a", "alice", `lol <img class="lazy-image" data-src="https://img.example.com/r.gif" alt="emo"/>`)),
		},
		images: map[string][]byte{"https://img.example.com/r.gif": solidGIF(t, color.Black, 8, 8)},
	}
	cs, err := Scrape(context.Background(), chapURL, f)
	if err != nil {
		t.Fatal(err)
	}
	r := cs[0].Replies
	if len(r) != 1 || len(r[0].Emotes) != 1 || r[0].Emotes[0] == nil {
		t.Fatalf("reply emotes not loaded: %+v", r)
	}
}

func TestRender_RepliesIndentedUnderParent(t *testing.T) {
	parent := Comment{Name: "alice", Body: "first"}
	withReplies := parent
	withReplies.Replies = []Comment{
		{Name: "carol", ReplyTo: "alice", Body: "agreed"},
		{Name: "dave", ReplyTo: "carol", Body: "me too"},
	}
	a, b := renderPNG(t, []Comment{parent}), renderPNG(t, []Comment{withReplies})
	if b.Bounds().Dy() <= a.Bounds().Dy() {
		t.Fatalf("replies did not add height: %d vs %d", b.Bounds().Dy(), a.Bounds().Dy())
	}
	// Reply text starts at the indent, so the strip between the page
	// margin and the reply bar must stay blank in the reply rows...
	replyTop := a.Bounds().Dy() - 1 // first reply row starts where the bare parent ended
	if hasNonWhitePixels(b, sideMargin+1, replyTop+4, sideMargin+replyIndent/2-2, b.Bounds().Dy()-4) {
		t.Error("reply rows draw inside the left indent")
	}
	// ...and the thread bar must be drawn in the indent.
	if !hasNonWhitePixels(b, sideMargin+replyIndent/2-2, replyTop+4, sideMargin+replyIndent/2+4, b.Bounds().Dy()-4) {
		t.Error("no thread bar in the reply indent")
	}
}

func renderPNG(t *testing.T, cs []Comment) image.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := Render(cs, &buf); err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(&buf)
	if err != nil {
		t.Fatal(err)
	}
	return img
}
