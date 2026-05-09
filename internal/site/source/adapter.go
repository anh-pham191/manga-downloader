package source

import (
	"context"
	"fmt"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/site"
)

// Site implements site.Site for example.com by combining a
// generic Fetcher with the parsers in this package.
type Site struct {
	Fetcher fetcher.Fetcher
}

func (s *Site) ListChapters(ctx context.Context, mangaURL string) ([]site.Chapter, error) {
	resp, err := s.Fetcher.Get(ctx, fetcher.Request{URL: mangaURL})
	if err != nil {
		return nil, fmt.Errorf("fetch manga page: %w", err)
	}
	return ParseChapters(string(resp.Body), mangaURL)
}

func (s *Site) ChapterImages(ctx context.Context, c site.Chapter) ([]site.ImageRef, error) {
	resp, err := s.Fetcher.Get(ctx, fetcher.Request{URL: c.URL, Referer: c.URL})
	if err != nil {
		return nil, fmt.Errorf("fetch chapter page: %w", err)
	}
	return ParseChapterImages(string(resp.Body), c.URL)
}
