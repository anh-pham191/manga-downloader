package comments

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/anhpham/downloader/internal/fetcher"
	"golang.org/x/net/html"
	"golang.org/x/text/unicode/norm"
)

// Scrape returns the parent-level comments for one chapter, taken
// from page 1 (rendered server-side in the chapter HTML) and
// page 2 (loaded via POST /frontend/comment/list). Replies are
// ignored. Returns at most ~12 comments.
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

	page1 := parseComments(doc)

	var page2 []Comment
	if bookID != "" && episodeID != "" {
		form := url.Values{
			"book_id":    {bookID},
			"parent_id":  {"0"},
			"page":       {"2"},
			"episode_id": {episodeID},
			"team_id":    {"0"},
		}
		p2resp, err := f.Post(ctx, fetcher.Request{
			URL:     "https://truyenqqko.com/frontend/comment/list",
			Referer: chapterURL,
		}, form)
		if err == nil && len(p2resp.Body) > 0 {
			if frag, perr := html.Parse(bytes.NewReader(p2resp.Body)); perr == nil {
				page2 = parseComments(frag)
			}
		}
		// Swallow page-2 errors (the failure-modes table allows
		// proceeding with page 1 only).
	}

	return append(page1, page2...), nil
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
		c := Comment{}
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
			c.Body = norm.NFC.String(strings.TrimSpace(textOfStrippingEmoteImages(body)))
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

func textOfStrippingEmoteImages(n *html.Node) string {
	var sb strings.Builder
	var rec func(*html.Node)
	rec = func(x *html.Node) {
		if x.Type == html.ElementNode && x.Data == "img" {
			return
		}
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			rec(c)
		}
	}
	rec(n)
	return sb.String()
}
