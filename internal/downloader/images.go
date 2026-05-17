// internal/downloader/images.go
//
// Package downloader fetches a chapter's image list and writes the
// images to a local directory. Manga-level orchestration moved to
// internal/pipeline as of this commit.
package downloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/layout"
	"github.com/anhpham/downloader/internal/site"
	sourcesite "github.com/anhpham/downloader/internal/site/source"
)

// FetchChapterImages downloads every image referenced by the chapter
// page into destDir. Each image lands as a .part file and is
// atomically renamed once its bytes are on disk.
func FetchChapterImages(ctx context.Context, chapter site.Chapter, destDir string, f fetcher.Fetcher) error {
	s := &sourcesite.Site{Fetcher: f}
	refs, err := s.ChapterImages(ctx, chapter)
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	for i, ref := range refs {
		ext := extFromURL(ref.URL)
		dst := filepath.Join(destDir, layout.ImageName(i+1, ext))
		if err := fetchOne(ctx, ref, dst, f); err != nil {
			return fmt.Errorf("image %d: %w", i+1, err)
		}
	}
	return nil
}

func fetchOne(ctx context.Context, ref site.ImageRef, dst string, f fetcher.Fetcher) error {
	resp, err := f.Get(ctx, fetcher.Request{URL: ref.URL, Referer: ref.Referer})
	if err != nil {
		return err
	}
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, resp.Body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// extFromURL returns the lowercase extension (without the dot)
// extracted from a URL path, defaulting to "jpg". Strips both the
// query string (`?cache-buster=…`) and the fragment (`#frag`) from
// the basename before scanning, so source URLs like
// `https://cdn.../001.jpg?r=r8645456` correctly yield `jpg` (not
// `jpg?r=r8645456`).
func extFromURL(u string) string {
	base := filepath.Base(u)
	if i := strings.IndexAny(base, "?#"); i != -1 {
		base = base[:i]
	}
	if i := strings.LastIndexByte(base, '.'); i != -1 && i < len(base)-1 {
		return strings.ToLower(base[i+1:])
	}
	return "jpg"
}
