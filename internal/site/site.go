package site

import "context"

// Chapter is one published chapter of a manga.
type Chapter struct {
	Number string // as published — "1", "227", "227.5"
	Title  string // optional
	URL    string
}

// ImageRef is one image to download, plus the referer the source
// site requires for hot-link protection.
type ImageRef struct {
	URL     string
	Referer string
}

// Site is a per-source-site adapter: it knows the URL conventions
// and HTML selectors of one site, and nothing else.
type Site interface {
	ListChapters(ctx context.Context, mangaURL string) ([]Chapter, error)
	ChapterImages(ctx context.Context, chapter Chapter) ([]ImageRef, error)
}
