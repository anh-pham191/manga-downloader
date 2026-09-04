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
	Mode            Mode
	MangaURL        string
	Root            string
	Name            string
	From, To        int
	Concurrency     int
	Site            site.Site
	Fetcher         fetcher.Fetcher
	Verbose         bool
	RefreshComments bool
	Logger          *log.Logger
}

var (
	ErrAnotherInstance = errors.New("another downloader is running")
	ErrNoArchive       = errors.New("no archive to operate on")
)

// Result summarises one run for callers that aggregate (update-all).
type Result struct {
	NewChapters   int // Kind==Both tasks that succeeded
	Failed        int
	MissingImages int // images replaced by placeholder pages across all new chapters
}

// Run executes one mode end-to-end.
func Run(ctx context.Context, opts Opts) error {
	_, err := RunResult(ctx, opts)
	return err
}

// RunResult is Run plus a count of what changed.
func RunResult(ctx context.Context, opts Opts) (Result, error) {
	var res Result
	cbzPath := filepath.Join(opts.Root, opts.Name+".cbz")
	lockPath := filepath.Join(opts.Root, opts.Name+".cbz.lock")
	if err := os.MkdirAll(opts.Root, 0o755); err != nil {
		return res, err
	}
	lock := flock.New(lockPath)
	got, err := lock.TryLock()
	if err != nil {
		return res, err
	}
	if !got {
		return res, ErrAnotherInstance
	}
	defer func() {
		lock.Unlock()
		os.Remove(lockPath)
	}()

	insp, err := archive.Inspect(cbzPath)
	if err != nil {
		return res, fmt.Errorf("inspect: %w", err)
	}
	if len(insp.Have) == 0 && opts.Mode != SyncManga {
		return res, ErrNoArchive
	}

	chapters, err := opts.Site.ListChapters(ctx, opts.MangaURL)
	if err != nil {
		return res, fmt.Errorf("list chapters: %w", err)
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
		existing := Plan(opts.Mode, chapters, insp, archiveWidth, opts.RefreshComments)
		for _, t := range existing {
			if insp.Have[t.Folder] {
				tasks = append(tasks, t)
			}
		}
		empty := archive.Inspection{Have: map[string]bool{}, HaveComments: map[string]bool{}}
		novel := Plan(opts.Mode, chapters, empty, sourceWidth, opts.RefreshComments)
		for _, t := range novel {
			if insp.Have[layout.Folder("", t.Number, archiveWidth)] {
				continue
			}
			tasks = append(tasks, t)
		}
	} else {
		tasks = Plan(opts.Mode, chapters, insp, effectiveWidth, opts.RefreshComments)
	}

	if len(tasks) == 0 {
		if opts.Logger != nil {
			opts.Logger.Println("nothing to do")
		}
		return res, nil
	}

	scratchRoot := filepath.Join(opts.Root, "."+opts.Name+".scratch")
	if err := os.MkdirAll(scratchRoot, 0o755); err != nil {
		return res, err
	}

	failed, missing := runTasks(ctx, tasks, scratchRoot, opts)
	res.MissingImages = missing
	if len(failed) > 0 && opts.Logger != nil {
		opts.Logger.Printf("warning: %d chapter task(s) failed; staging the remainder", len(failed))
		for _, f := range failed {
			opts.Logger.Printf("  %s: %v", f.Folder, f.Err)
		}
	}
	res.Failed = len(failed)
	failedSet := map[string]bool{}
	for _, f := range failed {
		failedSet[f.Folder] = true
	}
	for _, t := range tasks {
		if t.Kind == Both && !failedSet[t.Folder] {
			res.NewChapters++
		}
	}

	if err := archive.StageAndRename(cbzPath, scratchRoot); err != nil {
		return Result{}, fmt.Errorf("stage: %w", err)
	}

	// Always remove scratch after a successful stage — the .cbz is
	// now the source of truth. A subsequent run re-plans against
	// the archive's contents and re-fetches anything still missing
	// from a clean slate (the partial chapter dirs left behind by
	// failed attempts weren't reusable anyway: executeTask nukes
	// each chapter dir before retrying).
	if err := os.RemoveAll(scratchRoot); err != nil {
		return res, err
	}
	if len(failed) > 0 {
		return res, fmt.Errorf("%d chapter(s) failed; re-run to retry", len(failed))
	}
	return res, nil
}

// TaskFailure records a single chapter task that didn't complete.
type TaskFailure struct {
	Folder string
	Err    error
}

// runTasks dispatches every task to a bounded worker pool and
// returns the list of failures (empty on full success). Individual
// task errors are NEVER fatal — partial progress is preserved via
// `.ok` markers and the caller decides whether to stage what's done.
func runTasks(ctx context.Context, tasks []Task, scratchRoot string, opts Opts) (failures []TaskFailure, missing int) {
	concurrency := opts.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
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
			n, err := executeTask(ctx, t, scratchRoot, opts)
			mu.Lock()
			missing += n
			if err != nil {
				failures = append(failures, TaskFailure{Folder: t.Folder, Err: err})
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	return failures, missing
}

// executeTask downloads one chapter into the scratch dir. It returns
// the number of images that were replaced by placeholder pages (the
// chapter still counts as complete) alongside any fatal error.
func executeTask(ctx context.Context, t Task, scratchRoot string, opts Opts) (int, error) {
	chDir := filepath.Join(scratchRoot, t.Folder)
	if err := os.RemoveAll(chDir); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(chDir, 0o755); err != nil {
		return 0, err
	}

	missing := 0
	if t.Kind == Both {
		ch := site.Chapter{Number: t.Number, URL: t.URL}
		miss, err := downloader.FetchChapterImages(ctx, ch, chDir, opts.Fetcher)
		if err != nil {
			return 0, fmt.Errorf("fetch images %s: %w", t.Folder, err)
		}
		missing = len(miss)
		if opts.Logger != nil {
			for _, m := range miss {
				opts.Logger.Printf("warning: %s image %d missing, placeholder written: %s: %v", t.Folder, m.Index, m.URL, m.Err)
			}
		}
	}

	cs, err := comments.Scrape(ctx, t.URL, opts.Fetcher)
	if err != nil {
		return 0, fmt.Errorf("scrape %s: %w", t.Folder, err)
	}
	if len(cs) > 0 {
		f, err := os.Create(filepath.Join(chDir, layout.CommentsFilename))
		if err != nil {
			return 0, err
		}
		if err := comments.Render(cs, f); err != nil {
			f.Close()
			return 0, fmt.Errorf("render %s: %w", t.Folder, err)
		}
		if err := f.Close(); err != nil {
			return 0, err
		}
	}

	// .ok marker LAST.
	return missing, os.WriteFile(filepath.Join(chDir, ".ok"), nil, 0o644)
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
