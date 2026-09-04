package source

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/site"
)

const searchPath = "/frontend/search/search"

// ParseSearch parses the HTML fragment returned by the site's
// autocomplete endpoint: a list of <li><a href><p class="name">.
func ParseSearch(html string) ([]site.SearchHit, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse search html: %w", err)
	}
	var hits []site.SearchHit
	doc.Find("li a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		title := strings.TrimSpace(a.Find("p.name").First().Text())
		if href == "" || title == "" {
			return
		}
		hits = append(hits, site.SearchHit{Title: title, URL: href})
	})
	return hits, nil
}

// Search POSTs the query to the autocomplete endpoint on the same
// host as baseURL.
func (s *Site) Search(ctx context.Context, baseURL, query string) ([]site.SearchHit, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("search: invalid base url %q", baseURL)
	}
	origin := u.Scheme + "://" + u.Host
	form := url.Values{"search": {query}, "type": {"0"}}
	resp, err := s.Fetcher.Post(ctx, fetcher.Request{URL: origin + searchPath, Referer: origin + "/"}, form)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}
	if resp == nil {
		return nil, nil
	}
	return ParseSearch(string(resp.Body))
}
