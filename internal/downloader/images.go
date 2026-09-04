// internal/downloader/images.go
//
// Package downloader fetches a chapter's image list and writes the
// images to a local directory. Manga-level orchestration moved to
// internal/pipeline as of this commit.
package downloader

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/anhpham/downloader/internal/comments"
	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/layout"
	"github.com/anhpham/downloader/internal/site"
	sourcesite "github.com/anhpham/downloader/internal/site/source"
)

// MissingImage records one image that could not be fetched and was
// replaced by a placeholder page in the chapter folder.
type MissingImage struct {
	Index int // 1-based position within the chapter
	URL   string
	Err   error
}

// FetchChapterImages downloads every image referenced by the chapter
// page into destDir. Each image lands as a .part file and is
// atomically renamed once its bytes are on disk.
//
// A single image that cannot be fetched (dead host, bad certificate,
// 404, exhausted retries) does not fail the chapter: a placeholder PNG
// is written in its slot and the image is reported in the returned
// slice. Two cases remain fatal: a Cloudflare expiry (retrying later
// will succeed, so the chapter must not be marked complete) and a
// cancelled context. If every image fails, the chapter fails too —
// that means the page itself is broken, not one link.
func FetchChapterImages(ctx context.Context, chapter site.Chapter, destDir string, f fetcher.Fetcher) ([]MissingImage, error) {
	s := &sourcesite.Site{Fetcher: f}
	refs, err := s.ChapterImages(ctx, chapter)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}

	var missing []MissingImage
	for i, ref := range refs {
		idx := i + 1
		ext := extFromURL(ref.URL)
		dst := filepath.Join(destDir, layout.ImageName(idx, ext))
		err := fetchOne(ctx, ref, dst, f)
		if err == nil {
			continue
		}
		if errors.Is(err, fetcher.ErrCloudflareExpired) || ctx.Err() != nil {
			return missing, fmt.Errorf("image %d: %w", idx, err)
		}
		missing = append(missing, MissingImage{Index: idx, URL: ref.URL, Err: err})
		if perr := writePlaceholder(destDir, idx, ref.URL, err); perr != nil {
			return missing, fmt.Errorf("image %d: placeholder: %w", idx, perr)
		}
	}
	if len(refs) > 0 && len(missing) == len(refs) {
		return missing, fmt.Errorf("all %d images failed; first: %w", len(refs), missing[0].Err)
	}
	return missing, nil
}

func fetchOne(ctx context.Context, ref site.ImageRef, dst string, f fetcher.Fetcher) error {
	resp, err := f.Get(ctx, fetcher.Request{URL: ref.URL, Referer: ref.Referer})
	if err != nil {
		return err
	}
	if resp == nil {
		return errors.New("empty response")
	}
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, resp.Body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// writePlaceholder renders a notice page into the slot of a missing
// image so readers show something in the right position.
func writePlaceholder(destDir string, idx int, rawURL string, cause error) error {
	host := rawURL
	if u, err := url.Parse(rawURL); err == nil && u.Host != "" {
		host = u.Host
	}
	lines := []string{
		fmt.Sprintf("Image %d missing", idx),
		"The source did not serve this page when the chapter was archived.",
		"host: " + host,
		"reason: " + cause.Error(),
	}
	dst := filepath.Join(destDir, layout.ImageName(idx, "png"))
	tmp := dst + ".part"
	fh, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := comments.RenderNotice(lines, fh); err != nil {
		fh.Close()
		os.Remove(tmp)
		return err
	}
	if err := fh.Close(); err != nil {
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
