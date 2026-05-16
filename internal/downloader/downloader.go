// Package downloader orchestrates fetching every chapter of a manga
// to disk. It is deliberately ignorant of HTTP and selectors — those
// live behind the Site and Fetcher interfaces — so it can be tested
// with fakes and reasoned about as pure orchestration.
package downloader

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/layout"
	"github.com/anhpham/downloader/internal/site"
)

// chapterCacheFile is the filename, inside the manga root, where the
// chapter list is cached after the first fetch. On --resume we read
// this instead of re-fetching the manga page, saving one Cloudflare-
// protected request per resume cycle.
const chapterCacheFile = ".chapters.json"

// Downloader is configured once and run once. The zero value is not
// usable — Site, Fetcher, OutDir, and MangaSlug are required.
type Downloader struct {
	Site        site.Site
	Fetcher     fetcher.Fetcher
	OutDir      string
	MangaSlug   string
	Concurrency int  // chapters in flight at once. Defaults to 1 if <=0.
	From        int  // chapter number lower bound, inclusive. 0 = no bound.
	To          int  // chapter number upper bound, inclusive. 0 = no bound.
	Resume      bool // skip chapters whose folder already contains .done
	Logger      *log.Logger
}

// Result is the per-run summary, suitable for the CLI exit code and
// a final stderr line.
type Result struct {
	Completed int
	Skipped   int
	Failed    int
	Failures  []ChapterFailure
}

// ChapterFailure describes one chapter that did not finish; folder
// names use ChapterFailure.Folder so the user can re-target it.
type ChapterFailure struct {
	Number string
	Folder string
	Err    error
}

// Run executes the plan: list chapters, filter by From/To, then
// download each one with bounded concurrency.
func (d *Downloader) Run(ctx context.Context, mangaURL string) (Result, error) {
	if d.Logger == nil {
		d.Logger = log.New(io.Discard, "", 0)
	}
	concurrency := d.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	mangaRoot := filepath.Join(d.OutDir, d.MangaSlug)
	if err := os.MkdirAll(mangaRoot, 0o755); err != nil {
		return Result{}, fmt.Errorf("create manga dir: %w", err)
	}

	chapters, err := d.loadOrFetchChapters(ctx, mangaURL, mangaRoot)
	if err != nil {
		return Result{}, fmt.Errorf("list chapters: %w", err)
	}
	chapters = filterRange(chapters, d.From, d.To)
	if len(chapters) == 0 {
		return Result{}, errors.New("no chapters match the requested range")
	}

	width := layout.Width(chapters)

	var (
		mu       sync.Mutex
		res      Result
		sem      = make(chan struct{}, concurrency)
		wg       sync.WaitGroup
		stopOnce sync.Once
		stopped  atomic.Bool
	)

	// Pre-filter completed chapters before the dispatch loop. Cheaper
	// than dispatching a goroutine per skip, and lets us print one
	// summary line instead of N noise lines on long mangas.
	type job struct {
		chapter site.Chapter
		folder  string
	}
	var pending []job
	for _, c := range chapters {
		folder := layout.Folder(mangaRoot, c.Number, width)
		if d.Resume && hasDoneSentinel(folder) {
			res.Skipped++
			continue
		}
		pending = append(pending, job{chapter: c, folder: folder})
	}
	if res.Skipped > 0 {
		d.Logger.Printf("skipping %d already-completed chapters; %d to download", res.Skipped, len(pending))
	}

	for _, j := range pending {
		if stopped.Load() {
			break
		}
		c := j.chapter
		folder := j.folder

		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			if err := d.runChapter(ctx, c, folder); err != nil {
				mu.Lock()
				res.Failed++
				res.Failures = append(res.Failures, ChapterFailure{Number: c.Number, Folder: folder, Err: err})
				mu.Unlock()
				d.Logger.Printf("[chap %s] FAIL: %v", c.Number, err)
				if errors.Is(err, context.Canceled) {
					stopOnce.Do(func() { stopped.Store(true) })
				}
				return
			}
			mu.Lock()
			res.Completed++
			mu.Unlock()
			d.Logger.Printf("[chap %s] done", c.Number)
		}()
	}

	wg.Wait()
	return res, nil
}

// loadOrFetchChapters reads the chapter list from the on-disk cache
// when --resume is set, otherwise queries the live site and writes
// the result into the cache. The cache is a deliberately tiny JSON
// snapshot so a stale entry can be deleted with `rm`.
func (d *Downloader) loadOrFetchChapters(ctx context.Context, mangaURL, mangaRoot string) ([]site.Chapter, error) {
	cachePath := filepath.Join(mangaRoot, chapterCacheFile)
	if d.Resume {
		if chs, ok := readChapterCache(cachePath); ok {
			d.Logger.Printf("loaded %d chapters from %s", len(chs), cachePath)
			return chs, nil
		}
	}
	chs, err := d.Site.ListChapters(ctx, mangaURL)
	if err != nil {
		return nil, err
	}
	writeChapterCache(cachePath, chs)
	return chs, nil
}

func readChapterCache(path string) ([]site.Chapter, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var chs []site.Chapter
	if err := json.Unmarshal(b, &chs); err != nil || len(chs) == 0 {
		return nil, false
	}
	return chs, true
}

func writeChapterCache(path string, chs []site.Chapter) {
	b, err := json.MarshalIndent(chs, "", "  ")
	if err != nil {
		return
	}
	// Best-effort: a failed write just means the next run pays the
	// fetch cost again, which is fine.
	_ = os.WriteFile(path, b, 0o644)
}

func (d *Downloader) runChapter(ctx context.Context, c site.Chapter, folder string) error {
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return fmt.Errorf("create chapter dir: %w", err)
	}

	images, err := d.Site.ChapterImages(ctx, c)
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}
	if len(images) == 0 {
		return errors.New("chapter has zero images")
	}

	// Compute padding width for image filenames (separate from chapter
	// folder padding because images have a minimum width of 3).
	imgWidth := 1
	for n := len(images); n > 0; n /= 10 {
		imgWidth++
	}
	if imgWidth < 3 {
		imgWidth = 3
	}
	for i, img := range images {
		filename := fmt.Sprintf("%0*d%s", imgWidth, i+1, imageExt(img.URL))
		dst := filepath.Join(folder, filename)
		if _, err := os.Stat(dst); err == nil {
			// Already on disk from a previous partial run — keep it.
			continue
		}
		if err := d.fetchTo(ctx, img, dst); err != nil {
			return fmt.Errorf("image %d: %w", i+1, err)
		}
	}

	// Sentinel is the last write so a crash mid-chapter is recoverable.
	return os.WriteFile(filepath.Join(folder, ".done"), nil, 0o644)
}

func (d *Downloader) fetchTo(ctx context.Context, img site.ImageRef, dst string) error {
	resp, err := d.Fetcher.Get(ctx, fetcher.Request{URL: img.URL, Referer: img.Referer})
	if err != nil {
		return err
	}
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, resp.Body, 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, dst); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func filterRange(in []site.Chapter, from, to int) []site.Chapter {
	if from == 0 && to == 0 {
		return in
	}
	out := in[:0:0]
	for _, c := range in {
		n, ok := chapterNumeric(c.Number)
		if !ok {
			continue
		}
		if from != 0 && n < float64(from) {
			continue
		}
		if to != 0 && n > float64(to) {
			continue
		}
		out = append(out, c)
	}
	return out
}

func chapterNumeric(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}


func hasDoneSentinel(folder string) bool {
	_, err := os.Stat(filepath.Join(folder, ".done"))
	return err == nil
}

// imageExt extracts the file extension from a URL, dropping any
// query string. Falls back to .jpg when the URL has none.
func imageExt(u string) string {
	if i := strings.IndexAny(u, "?#"); i != -1 {
		u = u[:i]
	}
	ext := strings.ToLower(path.Ext(u))
	if ext == "" {
		return ".jpg"
	}
	return ext
}
