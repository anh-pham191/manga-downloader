package comments

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"net/url"
	"strconv"
	"strings"

	"github.com/anhpham/downloader/internal/fetcher"
	"golang.org/x/net/html"
	"golang.org/x/text/unicode/norm"
)

// maxCommentPages is the highest comment page Scrape will fetch. Page
// 1 is rendered server-side in the chapter HTML; pages 2..maxCommentPages
// are loaded via POST /frontend/comment/list. The loop stops early as
// soon as a page returns zero parent comments.
const maxCommentPages = 5

// maxReplyPages bounds the reply POSTs per thread. Replies come back
// from the same endpoint with parent_id=<comment id>; whether the
// server pages long threads is unverified, so the loop asks for more
// only while it is short of the expander's "N phản hồi" count and the
// last page brought new reply IDs.
const maxReplyPages = 5

// Scrape returns the parent-level comments for one chapter: page 1
// (server-rendered in the chapter HTML) plus pages 2..maxCommentPages
// (each a POST to /frontend/comment/list). The loop stops at the first
// page with no parent comments. Comments with a "N phản hồi" expander
// get their replies attached (one level: the site threads a reply to a
// reply under the same top-level comment).
func Scrape(ctx context.Context, chapterURL string, f fetcher.Fetcher) ([]Comment, error) {
	resp, err := f.Get(ctx, fetcher.Request{URL: chapterURL, Referer: chapterURL})
	if err != nil {
		return nil, fmt.Errorf("fetch chapter page: %w", err)
	}
	doc, err := html.Parse(bytes.NewReader(resp.Body))
	if err != nil {
		return nil, fmt.Errorf("parse chapter HTML: %w", err)
	}

	bookID, episodeID := extractHiddenIDs(doc)
	listURL := commentListURL(chapterURL)

	out := parseComments(doc)

	if bookID != "" && episodeID != "" {
		for p := 2; p <= maxCommentPages; p++ {
			form := url.Values{
				"book_id":    {bookID},
				"parent_id":  {"0"},
				"page":       {strconv.Itoa(p)},
				"episode_id": {episodeID},
				"team_id":    {"0"},
			}
			presp, err := f.Post(ctx, fetcher.Request{
				URL:     listURL,
				Referer: chapterURL,
			}, form)
			// Swallow per-page errors (the failure-modes table allows
			// proceeding with whatever pages we already have).
			if err != nil || presp == nil || len(presp.Body) == 0 {
				break
			}
			frag, perr := html.Parse(bytes.NewReader(presp.Body))
			if perr != nil {
				break
			}
			pageComments := parseComments(frag)
			if len(pageComments) == 0 {
				break
			}
			out = append(out, pageComments...)
		}
	}

	if bookID != "" {
		for i := range out {
			if out[i].id != "" && out[i].replyCount > 0 {
				out[i].Replies = scrapeReplies(ctx, f, listURL, chapterURL, bookID, out[i])
			}
		}
	}

	resolveEmotes(ctx, out, f)
	return out, nil
}

// commentListURL is the comment endpoint on the chapter's own host, so
// a domain rebrand doesn't leave comments pointed at the old one.
func commentListURL(chapterURL string) string {
	u, err := url.Parse(chapterURL)
	if err != nil || u.Host == "" {
		return chapterURL
	}
	return u.Scheme + "://" + u.Host + "/frontend/comment/list"
}

// scrapeReplies mirrors the browser's loadReply(id). Errors end the
// thread with whatever arrived, same as comment pages: replies are
// best-effort and never fail the chapter.
func scrapeReplies(ctx context.Context, f fetcher.Fetcher, listURL, chapterURL, bookID string, parent Comment) []Comment {
	var out []Comment
	seen := map[string]bool{}
	for p := 1; p <= maxReplyPages; p++ {
		form := url.Values{
			"book_id":   {bookID},
			"parent_id": {parent.id},
			"team_id":   {"0"},
		}
		if p > 1 {
			form.Set("page", strconv.Itoa(p))
		}
		resp, err := f.Post(ctx, fetcher.Request{URL: listURL, Referer: chapterURL}, form)
		if err != nil || resp == nil || len(resp.Body) == 0 {
			break
		}
		frag, err := html.Parse(bytes.NewReader(resp.Body))
		if err != nil {
			break
		}
		added := 0
		for _, r := range parseComments(frag) {
			if r.id != "" && seen[r.id] {
				continue
			}
			seen[r.id] = true
			out = append(out, r)
			added++
		}
		if added == 0 || len(out) >= parent.replyCount {
			break
		}
	}
	return out
}

func resolveEmotes(ctx context.Context, cs []Comment, f fetcher.Fetcher) {
	for i := range cs {
		c := &cs[i]
		if len(c.emoteURLs) > 0 {
			c.Emotes = make([]image.Image, len(c.emoteURLs))
			for j, u := range c.emoteURLs {
				c.Emotes[j] = loadEmote(ctx, u, f)
			}
			c.emoteURLs = nil
		}
		resolveEmotes(ctx, c.Replies, f)
	}
}

func parseComments(n *html.Node) []Comment {
	var out []Comment
	walk(n, func(node *html.Node) {
		if node.Type != html.ElementNode || node.Data != "article" {
			return
		}
		if !hasClass(node, "info-comment") || !hasClass(node, "comment-main-level") {
			return
		}
		c := Comment{id: classSuffix(node, "child_")}
		if name := findFirst(node, func(x *html.Node) bool {
			return x.Type == html.ElementNode && x.Data == "strong" &&
				hasClass(x, "level") && hasClassPrefix(x, "name_")
		}); name != nil {
			c.Name = textOf(name)
		}
		if lvl := findFirst(node, func(x *html.Node) bool {
			return x.Type == html.ElementNode && x.Data == "span" &&
				hasClass(x, "title-user-comment")
		}); lvl != nil {
			c.Level = strings.TrimSpace(textOf(lvl))
		}
		if body := findFirst(node, func(x *html.Node) bool {
			return x.Type == html.ElementNode && x.Data == "div" &&
				hasClass(x, "content-comment")
		}); body != nil {
			// The body opens with a <strong> naming who a reply answers
			// (empty on top-level comments); keep it out of the text.
			var mention *html.Node
			for x := body.FirstChild; x != nil; x = x.NextSibling {
				if x.Type == html.ElementNode {
					if x.Data == "strong" {
						mention = x
					}
					break
				}
			}
			if mention != nil {
				c.ReplyTo = norm.NFC.String(strings.TrimSpace(textOf(mention)))
			}
			text, urls := textWithEmotePlaceholders(body, mention)
			c.Body = norm.NFC.String(strings.TrimSpace(text))
			c.emoteURLs = urls
		}
		if more := findFirst(node, func(x *html.Node) bool {
			return x.Type == html.ElementNode && hasClass(x, "text-list-reply")
		}); more != nil {
			// "2 phản hồi" → 2
			if f := strings.Fields(textOf(more)); len(f) > 0 {
				c.replyCount, _ = strconv.Atoi(f[0])
			}
		}
		if likes := findFirst(node, func(x *html.Node) bool {
			return x.Type == html.ElementNode && x.Data == "span" &&
				hasClass(x, "total-like-comment")
		}); likes != nil {
			if n, err := strconv.Atoi(strings.TrimSpace(textOf(likes))); err == nil {
				c.LikeCount = n
			}
		}
		if c.Name != "" {
			out = append(out, c)
		}
	})
	return out
}

func extractHiddenIDs(n *html.Node) (book, episode string) {
	walk(n, func(node *html.Node) {
		if node.Type != html.ElementNode || node.Data != "input" {
			return
		}
		id, val := "", ""
		for _, a := range node.Attr {
			switch a.Key {
			case "id":
				id = a.Val
			case "value":
				val = a.Val
			}
		}
		switch id {
		case "book_id":
			book = val
		case "episode_id":
			episode = val
		}
	})
	return
}

func walk(n *html.Node, fn func(*html.Node)) {
	fn(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, fn)
	}
}

func findFirst(n *html.Node, match func(*html.Node) bool) *html.Node {
	var found *html.Node
	walk(n, func(x *html.Node) {
		if found == nil && match(x) {
			found = x
		}
	})
	return found
}

func classAttr(n *html.Node) string {
	for _, a := range n.Attr {
		if a.Key == "class" {
			return a.Val
		}
	}
	return ""
}

func hasClass(n *html.Node, want string) bool {
	for _, c := range strings.Fields(classAttr(n)) {
		if c == want {
			return true
		}
	}
	return false
}

// classSuffix returns what follows prefix in the first matching class,
// e.g. "child_123" → "123".
func classSuffix(n *html.Node, prefix string) string {
	for _, c := range strings.Fields(classAttr(n)) {
		if strings.HasPrefix(c, prefix) {
			return strings.TrimPrefix(c, prefix)
		}
	}
	return ""
}

func hasClassPrefix(n *html.Node, prefix string) bool {
	for _, c := range strings.Fields(classAttr(n)) {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func textOf(n *html.Node) string {
	var sb strings.Builder
	walk(n, func(x *html.Node) {
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
		}
	})
	return sb.String()
}

// textWithEmotePlaceholders flattens a comment body to text, swapping
// each emote <img> for emoteRune(i) and returning the image URLs in
// slot order. <img> tags without a fetchable URL are dropped, as are
// slots past the PUA range (a comment with 6400 emotes is spam).
func textWithEmotePlaceholders(n, skip *html.Node) (string, []string) {
	var sb strings.Builder
	var urls []string
	var rec func(*html.Node)
	rec = func(x *html.Node) {
		if x == skip {
			return
		}
		if x.Type == html.ElementNode && x.Data == "img" {
			if u := emoteSrc(x); u != "" && len(urls) < emoteMax {
				sb.WriteRune(emoteRune(len(urls)))
				urls = append(urls, u)
			}
			return
		}
		if x.Type == html.TextNode {
			sb.WriteString(strings.Map(func(r rune) rune {
				if isPUA(r) {
					return -1
				}
				return r
			}, x.Data))
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return sb.String(), urls
}
