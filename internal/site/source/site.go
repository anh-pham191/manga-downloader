// Package source adapts the example.com family of sites to the
// site.Site interface. All site-specific selectors live here so that
// changes to the source DOM are isolated to one file.
package source

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/anhpham/downloader/internal/site"
)

// chapNumberRE extracts the trailing chapter number from URLs of the
// form `.../<slug>-chap-<number>` where number may be `123` or `12-5`
// (representing 12.5).
var chapNumberRE = regexp.MustCompile(`-chap-([0-9]+(?:-[0-9]+)?)/?$`)

// ParseChapters extracts the chapter list from a manga page's HTML.
// The returned slice is sorted ascending by chapter number; the
// source page lists newest-first.
func ParseChapters(html, mangaURL string) ([]site.Chapter, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse manga html: %w", err)
	}
	base, err := url.Parse(mangaURL)
	if err != nil {
		return nil, fmt.Errorf("parse manga url: %w", err)
	}

	var chapters []site.Chapter
	doc.Find(".works-chapter-item .name-chap a, .works-chapter-list a[href*=\"-chap-\"]").Each(func(_ int, s *goquery.Selection) {
		href, ok := s.Attr("href")
		if !ok {
			return
		}
		abs, err := absURL(base, href)
		if err != nil {
			return
		}
		number := chapNumberFromURL(abs)
		if number == "" {
			return
		}
		chapters = append(chapters, site.Chapter{
			Number: number,
			Title:  strings.TrimSpace(s.Text()),
			URL:    abs,
		})
	})

	chapters = dedupeByURL(chapters)
	sort.Slice(chapters, func(i, j int) bool {
		return chapterLess(chapters[i].Number, chapters[j].Number)
	})

	if len(chapters) == 0 {
		return nil, fmt.Errorf("no chapters found at %s — selectors may need updating", mangaURL)
	}
	return chapters, nil
}

// ParseChapterImages extracts the ordered image URLs from a chapter
// page. Returns an error rather than an empty slice when no images
// are found — see PLAN.md hazard H8.
func ParseChapterImages(html, chapterURL string) ([]site.ImageRef, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse chapter html: %w", err)
	}
	base, err := url.Parse(chapterURL)
	if err != nil {
		return nil, fmt.Errorf("parse chapter url: %w", err)
	}

	var images []site.ImageRef
	doc.Find(".page-chapter img, .reading-detail img").Each(func(_ int, s *goquery.Selection) {
		raw := imageURL(s)
		if raw == "" {
			return
		}
		abs, err := absURL(base, raw)
		if err != nil {
			return
		}
		images = append(images, site.ImageRef{URL: abs, Referer: chapterURL})
	})

	if len(images) == 0 {
		return nil, fmt.Errorf("no images found at %s — selectors may need updating", chapterURL)
	}
	return images, nil
}

// imageURL returns the most likely real URL for a chapter page image,
// preferring lazy-load attributes over `src` (which often holds a
// placeholder when lazy-loading is in play).
func imageURL(s *goquery.Selection) string {
	for _, attr := range []string{"data-original", "data-src", "data-cfsrc"} {
		if v, ok := s.Attr(attr); ok {
			if v = strings.TrimSpace(v); v != "" {
				return v
			}
		}
	}
	if v, ok := s.Attr("src"); ok {
		v = strings.TrimSpace(v)
		if v != "" && !strings.HasPrefix(v, "data:") {
			return v
		}
	}
	return ""
}

func absURL(base *url.URL, ref string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(ref))
	if err != nil {
		return "", err
	}
	return base.ResolveReference(u).String(), nil
}

func chapNumberFromURL(u string) string {
	m := chapNumberRE.FindStringSubmatch(u)
	if m == nil {
		return ""
	}
	// `12-5` in the URL means `12.5` to humans.
	return strings.ReplaceAll(m[1], "-", ".")
}

func dedupeByURL(in []site.Chapter) []site.Chapter {
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, c := range in {
		if _, ok := seen[c.URL]; ok {
			continue
		}
		seen[c.URL] = struct{}{}
		out = append(out, c)
	}
	return out
}

// chapterLess orders chapter numbers numerically ("2" < "10"), with
// fractional chapters slotted between their neighbours ("1" < "1.5"
// < "2"). Falls back to string compare for unparseable values so
// nothing is silently dropped.
func chapterLess(a, b string) bool {
	af, aok := strconv.ParseFloat(a, 64)
	bf, bok := strconv.ParseFloat(b, 64)
	if aok == nil && bok == nil {
		return af < bf
	}
	return a < b
}
