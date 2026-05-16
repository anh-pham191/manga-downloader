package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/gofrs/flock"

	"github.com/anhpham/downloader/internal/archive"
	"github.com/anhpham/downloader/internal/comments"
	"github.com/anhpham/downloader/internal/downloader"
	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/layout"
	"github.com/anhpham/downloader/internal/site"
)

type Opts struct {
	Mode        Mode
	MangaURL    string
	Root        string
	Name        string
	From, To    int
	Concurrency int
	Site        site.Site
	Fetcher     fetcher.Fetcher
	Verbose     bool
	Logger      *log.Logger
}

var (
	ErrAnotherInstance = errors.New("another downloader is running")
	ErrNoArchive       = errors.New("no archive to operate on")
)

// Run executes one mode end-to-end.
func Run(ctx context.Context, opts Opts) error {
	cbzPath := filepath.Join(opts.Root, opts.Name+".cbz")
	lockPath := filepath.Join(opts.Root, opts.Name+".cbz.lock")
	if err := os.MkdirAll(opts.Root, 0o755); err != nil {
		return err
	}
	lock := flock.New(lockPath)
	got, err := lock.TryLock()
	if err != nil {
		return err
	}
	if !got {
		return ErrAnotherInstance
	}
	defer lock.Unlock()

	insp, err := archive.Inspect(cbzPath)
	if err != nil {
		return fmt.Errorf("inspect: %w", err)
	}
	if len(insp.Have) == 0 && opts.Mode != SyncManga {
		return ErrNoArchive
	}

	chapters, err := opts.Site.ListChapters(ctx, opts.MangaURL)
	if err != nil {
		return fmt.Errorf("list chapters: %w", err)
	}

	sourceWidth := layout.Width(chapters)
	archiveWidth := insp.InferredWidth()
	effectiveWidth := sourceWidth
	if archiveWidth > effectiveWidth {
		effectiveWidth = archiveWidth
	}

	// Honour --from / --to AFTER width is captured (filterRange aliases input).
	chapters = filterRange(chapters, opts.From, opts.To)

	var tasks []Task
	if archiveWidth > 0 && archiveWidth < sourceWidth {
		existing := Plan(opts.Mode, chapters, insp, archiveWidth)
		for _, t := range existing {
			if insp.Have[t.Folder] {
				tasks = append(tasks, t)
			}
		}
		empty := archive.Inspection{Have: map[string]bool{}, HaveComments: map[string]bool{}}
		novel := Plan(opts.Mode, chapters, empty, sourceWidth)
		for _, t := range novel {
			if insp.Have[layout.Folder("", t.Number, archiveWidth)] {
				continue
			}
			tasks = append(tasks, t)
		}
	} else {
		tasks = Plan(opts.Mode, chapters, insp, effectiveWidth)
	}

	if len(tasks) == 0 {
		if opts.Logger != nil {
			opts.Logger.Println("nothing to do")
		}
		return nil
	}

	scratchRoot := filepath.Join(opts.Root, "."+opts.Name+".scratch")
	if err := os.MkdirAll(scratchRoot, 0o755); err != nil {
		return err
	}

	if err := runTasks(ctx, tasks, scratchRoot, opts); err != nil {
		return err
	}

	if err := archive.StageAndRename(cbzPath, scratchRoot); err != nil {
		return fmt.Errorf("stage: %w", err)
	}

	return os.RemoveAll(scratchRoot)
}

func runTasks(ctx context.Context, tasks []Task, scratchRoot string, opts Opts) error {
	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var firstErr error
	var mu sync.Mutex

	for _, t := range tasks {
		t := t
		if _, err := os.Stat(filepath.Join(scratchRoot, t.Folder, ".ok")); err == nil {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := executeTask(ctx, t, scratchRoot, opts); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func executeTask(ctx context.Context, t Task, scratchRoot string, opts Opts) error {
	chDir := filepath.Join(scratchRoot, t.Folder)
	if err := os.RemoveAll(chDir); err != nil {
		return err
	}
	if err := os.MkdirAll(chDir, 0o755); err != nil {
		return err
	}

	if t.Kind == Both {
		ch := site.Chapter{Number: t.Number, URL: t.URL}
		if err := downloader.FetchChapterImages(ctx, ch, chDir, opts.Fetcher); err != nil {
			return fmt.Errorf("fetch images %s: %w", t.Folder, err)
		}
	}

	cs, err := comments.Scrape(ctx, t.URL, opts.Fetcher)
	if err != nil {
		return fmt.Errorf("scrape %s: %w", t.Folder, err)
	}
	if len(cs) > 0 {
		f, err := os.Create(filepath.Join(chDir, layout.CommentsFilename))
		if err != nil {
			return err
		}
		if err := comments.Render(cs, f); err != nil {
			f.Close()
			return fmt.Errorf("render %s: %w", t.Folder, err)
		}
		if err := f.Close(); err != nil {
			return err
		}
	}

	// .ok marker LAST.
	return os.WriteFile(filepath.Join(chDir, ".ok"), nil, 0o644)
}

func filterRange(in []site.Chapter, from, to int) []site.Chapter {
	if from == 0 && to == 0 {
		return in
	}
	out := make([]site.Chapter, 0, len(in))
	for _, c := range in {
		n := parseChapterNumber(c.Number)
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

// parseChapterNumber turns "12" → 12.0, "12.5" → 12.5, "12-5" → 12.5.
// Returns 0 (and the chapter is excluded by --from/--to filtering)
// for unparseable strings.
func parseChapterNumber(s string) float64 {
	s = strings.ReplaceAll(s, "-", ".")
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return n
}
