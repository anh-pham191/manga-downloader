# Comments 5-Pages + `--refresh-comments` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Scrape up to 5 comment pages per chapter (was 2) and add a CLI-only `--refresh-comments` flag that re-scrapes and cleanly replaces comments on already-archived chapters without re-downloading images.

**Architecture:** The page-count change lives in the shared `comments.Scrape`, so every scrape path (new-chapter download and comment-only render) gets 5 pages automatically. `--refresh-comments` flips the `SyncComments` planner guard so already-commented chapters are re-rendered, and a staging fix makes the re-rendered comments entry replace (not duplicate) the archived one.

**Tech Stack:** Go, `golang.org/x/net/html`, `archive/zip`, stdlib `flag`/`testing`.

**Spec:** `docs/superpowers/specs/2026-06-02-comments-5-pages-refresh-design.md`

---

## File Structure

- `internal/comments/scraper.go` — MODIFY: `Scrape` loops pages 2..5 with early-stop; add `maxCommentPages` const; update doc comment.
- `internal/comments/scraper_test.go` — MODIFY: add page-aware fetcher + merge/early-stop test.
- `internal/pipeline/plan.go` — MODIFY: `Plan` gains `refresh bool`; `SyncComments` branch honors it.
- `internal/pipeline/plan_test.go` — MODIFY: update existing `Plan(...)` calls to new signature; add refresh test.
- `internal/pipeline/pipeline.go` — MODIFY: add `RefreshComments bool` to `Opts`; thread into all three `Plan(...)` calls.
- `main.go` — MODIFY: add `--refresh-comments` flag, wire into `Opts`, warn when used with non-`sync-comments` modes, update `usage()`.
- `internal/archive/writer.go` — MODIFY: `stageViaGoZip` skips existing entries that a scratch chapter will overwrite.
- `internal/archive/archive_test.go` — MODIFY: add replace-not-duplicate staging test.

---

## Task 1: Scraper — 5-page loop with early-stop

**Files:**
- Modify: `internal/comments/scraper.go:16-57`
- Test: `internal/comments/scraper_test.go`

- [ ] **Step 1: Write the failing test**

Add this to `internal/comments/scraper_test.go` (the `fetcher` and `url` imports already exist):

```go
// pageAwareFetcher serves a different POST body per "page" form value
// and records which pages were requested.
type pageAwareFetcher struct {
	getBody []byte
	pages   map[string][]byte
	posts   []string
}

func (f *pageAwareFetcher) Get(_ context.Context, _ fetcher.Request) (*fetcher.Response, error) {
	return &fetcher.Response{Body: f.getBody, ContentType: "text/html"}, nil
}
func (f *pageAwareFetcher) Post(_ context.Context, _ fetcher.Request, form url.Values) (*fetcher.Response, error) {
	p := form.Get("page")
	f.posts = append(f.posts, p)
	return &fetcher.Response{Body: f.pages[p], ContentType: "text/html"}, nil
}

func commentFrag(name string) []byte {
	return []byte(`<div class="comment_list">
  <article class="info-comment comment-main-level child_1 parent_0">
    <strong class="level name_5">` + name + `</strong>
    <span class="title-user-comment title-member level_5">Cấp 5</span>
    <div class="content-comment">body of ` + name + `</div>
    <span class="total-like-comment">1</span>
  </article>
</div>`)
}

func TestScrape_MergesPagesAndStopsOnEmpty(t *testing.T) {
	page1 := []byte(`<html><body>
  <input id="book_id" value="1"/>
  <input id="episode_id" value="2"/>
  <div id="comment_list">
    <article class="info-comment comment-main-level child_1 parent_0">
      <strong class="level name_5">p1user</strong>
      <span class="title-user-comment title-member level_5">Cấp 5</span>
      <div class="content-comment">page one</div>
      <span class="total-like-comment">1</span>
    </article>
  </div>
</body></html>`)

	f := &pageAwareFetcher{
		getBody: page1,
		pages: map[string][]byte{
			"2": commentFrag("p2user"),
			"3": commentFrag("p3user"),
			"4": []byte(`<div class="comment_list"></div>`), // valid HTML, zero comments → stop
			"5": commentFrag("p5user"),                      // must never be requested
		},
	}

	cs, err := Scrape(context.Background(), "https://example.com/chap-1", f)
	if err != nil {
		t.Fatal(err)
	}

	// page1 + page2 + page3 = 3 comments; page 4 empty stops the loop.
	if len(cs) != 3 {
		t.Fatalf("merged comments = %d, want 3", len(cs))
	}
	// Pages 2,3,4 requested; page 5 never reached (early-stop after empty page 4).
	if len(f.posts) != 3 {
		t.Fatalf("POSTed pages = %v, want exactly [2 3 4]", f.posts)
	}
	if f.posts[len(f.posts)-1] != "4" {
		t.Fatalf("last POSTed page = %q, want 4", f.posts[len(f.posts)-1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/comments/ -run TestScrape_MergesPagesAndStopsOnEmpty -v`
Expected: FAIL — current `Scrape` only POSTs page 2 once, so `f.posts` is `[2]` (length 1, not 3) and `len(cs)` is 2, not 3.

- [ ] **Step 3: Add the `maxCommentPages` const and rewrite the loop**

In `internal/comments/scraper.go`, replace the doc comment + `Scrape` body (current lines 16-57) with:

```go
// maxCommentPages is the highest comment page Scrape will fetch. Page
// 1 is rendered server-side in the chapter HTML; pages 2..maxCommentPages
// are loaded via POST /frontend/comment/list. The loop stops early as
// soon as a page returns zero parent comments.
const maxCommentPages = 5

// Scrape returns the parent-level comments for one chapter: page 1
// (server-rendered in the chapter HTML) plus pages 2..maxCommentPages
// (each a POST to /frontend/comment/list). Replies are ignored. The
// loop stops at the first page with no parent comments.
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

	out := parseComments(doc)

	if bookID != "" && episodeID != "" {
		for p := 2; p <= maxCommentPages; p++ {
			form := url.Values{
				"book_id":    {bookID},
				"parent_id":  {"0"},
				"page":       {strconv.Itoa(p)},
				"episode_id": {episodeID},
				"team_id":    {"0"},
			}
			presp, err := f.Post(ctx, fetcher.Request{
				URL:     "https://truyenqqko.com/frontend/comment/list",
				Referer: chapterURL,
			}, form)
			// Swallow per-page errors (the failure-modes table allows
			// proceeding with whatever pages we already have).
			if err != nil || len(presp.Body) == 0 {
				break
			}
			frag, perr := html.Parse(bytes.NewReader(presp.Body))
			if perr != nil {
				break
			}
			pageComments := parseComments(frag)
			if len(pageComments) == 0 {
				break
			}
			out = append(out, pageComments...)
		}
	}

	return out, nil
}
```

(The `strconv` import already exists at `scraper.go:8`; no import changes needed.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/comments/ -v`
Expected: PASS for `TestScrape_MergesPagesAndStopsOnEmpty`, `TestScrape_PullsPage2`, `TestScrape_StripsEmoteImages`, `TestScrape_Page1FromChapterHTML`.

- [ ] **Step 5: Commit**

```bash
git add internal/comments/scraper.go internal/comments/scraper_test.go
git commit -m "comments: scrape up to 5 pages with early-stop on empty page"
```

---

## Task 2: Planner refresh param + Opts field + thread call sites

**Files:**
- Modify: `internal/pipeline/plan.go:33-58`
- Modify: `internal/pipeline/pipeline.go:24-35` (Opts), `:87`, `:94`, `:102` (Plan calls)
- Test: `internal/pipeline/plan_test.go`

This task changes `Plan`'s signature, so all callers and existing tests update together to keep the build green.

- [ ] **Step 1: Write the failing test and update existing Plan calls**

In `internal/pipeline/plan_test.go`, update the existing call at the end of `TestPlan_Matrix` (currently `tasks := Plan(c.mode, chapters, insp, 4 /*width*/)`) to:

```go
				tasks := Plan(c.mode, chapters, insp, 4 /*width*/, false /*refresh*/)
```

Update the call in `TestPlan_WidthStabilityFromArchive` (currently `tasks := Plan(SyncManga, chapters, insp, 5)`) to:

```go
	tasks := Plan(SyncManga, chapters, insp, 5, false)
```

Then add this new test:

```go
func TestPlan_RefreshRendersExistingComments(t *testing.T) {
	chapters := []site.Chapter{chap("1"), chap("2"), chap("3")}
	have := map[string]bool{"chap-0001": true, "chap-0002": true}
	haveComments := map[string]bool{"chap-0001": true} // chap-0001 already has comments
	insp := archive.Inspection{Have: have, HaveComments: haveComments}

	// refresh=false: only chap-0002 (in archive, missing comments) → Render.
	noRefresh := Plan(SyncComments, chapters, insp, 4, false)
	if len(noRefresh) != 1 || noRefresh[0].Folder != "chap-0002" {
		t.Fatalf("refresh=false: got %v, want [chap-0002]", noRefresh)
	}

	// refresh=true: both archived chapters re-render, incl. chap-0001
	// which already has comments. chap-0003 is not in the archive, so
	// sync-comments never touches it.
	got := map[string]TaskKind{}
	for _, tk := range Plan(SyncComments, chapters, insp, 4, true) {
		got[tk.Folder] = tk.Kind
	}
	want := map[string]TaskKind{"chap-0001": Render, "chap-0002": Render}
	if len(got) != len(want) {
		t.Fatalf("refresh=true: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("folder %q: got %v, want %v", k, got[k], v)
		}
	}
	if _, ok := got["chap-0003"]; ok {
		t.Error("chap-0003 not in archive; should not be planned even with refresh")
	}
}
```

- [ ] **Step 2: Run to verify it fails (build error)**

Run: `go build ./... && go test ./internal/pipeline/ -run TestPlan -v`
Expected: FAIL — build error `too many arguments in call to Plan` (signature is still 4 params). This is the expected failing state.

- [ ] **Step 3: Change the `Plan` signature and `SyncComments` branch**

In `internal/pipeline/plan.go`, change the signature (line 33) and the `SyncComments` case (lines 41-44):

```go
// Plan converts (mode, source chapter list, archive inspection,
// effective width, refresh) into the work list for the run. When
// refresh is true, SyncComments re-renders comments for every
// archived chapter, even those that already have a comments page.
func Plan(mode Mode, chapters []site.Chapter, insp archive.Inspection, width int, refresh bool) []Task {
	var out []Task
	for _, c := range chapters {
		folder := layout.Folder("", c.Number, width)
		in := insp.Have[folder]
		hasComments := insp.HaveComments[folder]

		switch mode {
		case SyncComments:
			if in && (refresh || !hasComments) {
				out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Render})
			}
		case Resume:
			if !in {
				out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Both})
			}
		case SyncManga:
			if !in {
				out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Both})
			} else if !hasComments {
				out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Render})
			}
		}
	}
	return out
}
```

- [ ] **Step 4: Add `RefreshComments` to `Opts` and thread it into all three `Plan` calls**

In `internal/pipeline/pipeline.go`, add the field to the `Opts` struct (after `Verbose bool` at line 33):

```go
	Verbose         bool
	RefreshComments bool
	Logger          *log.Logger
```

Then update the three `Plan(...)` call sites:

Line 87 (existing path):
```go
		existing := Plan(opts.Mode, chapters, insp, archiveWidth, opts.RefreshComments)
```

Line 94 (novel path):
```go
		novel := Plan(opts.Mode, chapters, empty, sourceWidth, opts.RefreshComments)
```

Line 102 (normal path):
```go
		tasks = Plan(opts.Mode, chapters, insp, effectiveWidth, opts.RefreshComments)
```

- [ ] **Step 5: Run to verify build + tests pass**

Run: `go build ./... && go test ./internal/pipeline/ -v`
Expected: PASS — all `TestPlan_*` tests, including `TestPlan_RefreshRendersExistingComments`.

- [ ] **Step 6: Commit**

```bash
git add internal/pipeline/plan.go internal/pipeline/plan_test.go internal/pipeline/pipeline.go
git commit -m "pipeline: add refresh flag so sync-comments re-renders existing comments"
```

---

## Task 3: CLI `--refresh-comments` flag + warning + usage

**Files:**
- Modify: `main.go:40-95` (flag block + Opts), `:130-145` (usage)

No new unit test — this is CLI wiring; verified by build + manual run.

- [ ] **Step 1: Add the flag**

In `main.go`, add to the flag block (after the `name` flag at line 47):

```go
	name := fs.String("name", "", "archive name (defaults to URL slug)")
	refreshComments := fs.Bool("refresh-comments", false, "re-scrape & replace comments on already-archived chapters (sync-comments only)")
```

- [ ] **Step 2: Warn when the flag is used with a mode that ignores it**

In `main.go`, immediately after `mangaURL := fs.Arg(0)` (line 55), add:

```go
	if *refreshComments && mode != pipeline.SyncComments {
		fmt.Fprintln(os.Stderr, "warning: --refresh-comments only applies to `sync-comments`; ignoring it for this mode")
	}
```

- [ ] **Step 3: Pass the flag into Opts**

In `main.go`, add to the `pipeline.Opts{...}` literal (after `Verbose: *verbose,` at line 93):

```go
		Verbose:         *verbose,
		RefreshComments: *refreshComments,
		Logger:          log.New(os.Stderr, "", 0),
```

- [ ] **Step 4: Document the flag in usage()**

In `main.go`, update the `sync-comments` usage line (line 134) and add a flag line. Replace:

```go
  downloader sync-comments [flags] <manga-url>   backfill comments on existing archive (no new chapters)
```
with:
```go
  downloader sync-comments [flags] <manga-url>   backfill comments on existing archive (no new chapters; add --refresh-comments to re-scrape ALL chapters)
```

And add this line to the `flags:` block (after `--to int`, line 142):

```go
  --refresh-comments     re-scrape & replace comments on all archived chapters (sync-comments only)
```

- [ ] **Step 5: Verify build and help output**

Run: `go build ./... && go run . sync-comments --help 2>&1 | grep -A1 refresh-comments`
Expected: build succeeds; help text shows the `--refresh-comments` flag line.

- [ ] **Step 6: Commit**

```bash
git add main.go
git commit -m "cli: add --refresh-comments flag (sync-comments only) with warning + usage"
```

---

## Task 4: Staging — replace, not duplicate

**Files:**
- Modify: `internal/archive/writer.go:70-136` (`stageViaGoZip` body, from `zw := zip.NewWriter(tmp)` onward — leave the `tmpPath`/`tmp`/`cleanup` setup at `:62-68` and the `zw.Close()`/`verifyArchive`/`os.Rename` tail at `:137-158` untouched)
- Test: `internal/archive/archive_test.go`

- [ ] **Step 1: Write the failing test**

Add this to `internal/archive/archive_test.go` (imports `archive/zip`, `io`, `io/ioutil`, `os`, `path/filepath`, `testing` already present):

```go
func TestStageAndRename_ReplacesExistingComments(t *testing.T) {
	// Archive already has an image AND an old comments page for chap-0001.
	p := buildCBZ(t, map[string][]byte{
		"chap-0001/001.jpg":          []byte("AAA"),
		"chap-0001/zzz-comments.png": []byte("OLD"),
	})

	// A refresh re-renders only the comments page (no images) into scratch.
	scratch := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(scratch, "chap-0001"), 0o755))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", "zzz-comments.png"), []byte("NEW"), 0o644))
	must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", ".ok"), nil, 0o644))

	if err := StageAndRename(p, scratch); err != nil {
		t.Fatal(err)
	}

	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()

	var commentEntries int
	var commentBody string
	var haveImage bool
	for _, f := range zr.File {
		switch f.Name {
		case "chap-0001/zzz-comments.png":
			commentEntries++
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			rc.Close()
			commentBody = string(b)
		case "chap-0001/001.jpg":
			haveImage = true
		}
	}
	if commentEntries != 1 {
		t.Fatalf("zzz-comments.png entries = %d, want 1 (replaced, not duplicated)", commentEntries)
	}
	if commentBody != "NEW" {
		t.Errorf("comment body = %q, want NEW (scratch should win)", commentBody)
	}
	if !haveImage {
		t.Error("chap-0001/001.jpg should be preserved untouched")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/archive/ -run TestStageAndRename_ReplacesExistingComments -v`
Expected: FAIL — current `stageViaGoZip` copies the OLD `zzz-comments.png` then appends the NEW one, so `commentEntries == 2` (duplicate).

- [ ] **Step 3: Restructure `stageViaGoZip` to skip overwritten names**

In `internal/archive/writer.go`, replace the body of `stageViaGoZip` from the `zw := zip.NewWriter(tmp)` line through the end of the scratch-append loop (current lines 70-136) with:

```go
	zw := zip.NewWriter(tmp)

	// Read the .ok-marked scratch chapters first and compute the set of
	// entry names they will write, so existing archive entries of the
	// same name are dropped (replaced) rather than duplicated.
	chapters, err := readMarkedChapters(scratchRoot)
	if err != nil {
		cleanup()
		return err
	}
	sort.Strings(chapters)

	scratchNames := map[string]bool{}
	for _, ch := range chapters {
		ents, err := os.ReadDir(filepath.Join(scratchRoot, ch))
		if err != nil {
			cleanup()
			return err
		}
		for _, e := range ents {
			if e.Name() == ".ok" || e.IsDir() {
				continue
			}
			scratchNames[ch+"/"+e.Name()] = true
		}
	}

	// 1. Copy existing entries from cbzPath (if it exists) via raw,
	//    skipping any name a scratch chapter will overwrite.
	if zr, err := zip.OpenReader(cbzPath); err == nil {
		defer zr.Close()
		for _, f := range zr.File {
			if scratchNames[f.Name] {
				continue
			}
			rc, err := f.OpenRaw()
			if err != nil {
				cleanup()
				return fmt.Errorf("openraw %s: %w", f.Name, err)
			}
			hdr := f.FileHeader
			w, err := zw.CreateRaw(&hdr)
			if err != nil {
				cleanup()
				return fmt.Errorf("createraw %s: %w", f.Name, err)
			}
			if _, err := io.Copy(w, rc); err != nil {
				cleanup()
				return fmt.Errorf("copy %s: %w", f.Name, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		cleanup()
		return fmt.Errorf("open existing: %w", err)
	}

	// 2. Append scratch entries (already gathered above).
	for _, ch := range chapters {
		chDir := filepath.Join(scratchRoot, ch)
		ents, err := os.ReadDir(chDir)
		if err != nil {
			cleanup()
			return err
		}
		// Stable order inside each chapter.
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
		for _, e := range ents {
			if e.Name() == ".ok" || e.IsDir() {
				continue
			}
			srcPath := filepath.Join(chDir, e.Name())
			raw, err := os.ReadFile(srcPath)
			if err != nil {
				cleanup()
				return err
			}
			w, err := zw.CreateHeader(&zip.FileHeader{
				Name:   ch + "/" + e.Name(),
				Method: zip.Store,
			})
			if err != nil {
				cleanup()
				return err
			}
			if _, err := w.Write(raw); err != nil {
				cleanup()
				return err
			}
		}
	}
```

This replaces the old block that did `readMarkedChapters` at line 98 (now hoisted above the copy loop). The `zw.Close()`/`tmp.Close()`/`verifyArchive`/`os.Rename` tail (current lines 137-158) stays unchanged.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/archive/ -v`
Expected: PASS — `TestStageAndRename_ReplacesExistingComments` plus the existing `TestStageAndRename_PreservesOriginalAndAppends`, `TestStageAndRename_LargeArchiveTakesOSZipPath`, `TestStageAndRename_CreatesFreshIfTargetMissing`, `TestInspect_*`.

- [ ] **Step 5: Commit**

```bash
git add internal/archive/writer.go internal/archive/archive_test.go
git commit -m "archive: staging replaces same-named entries instead of duplicating"
```

---

## Task 5: Full verification

**Files:** none (verification only)

- [ ] **Step 1: Build and run the whole test suite**

Run: `go build ./... && go test ./...`
Expected: PASS across all packages.

- [ ] **Step 2: Confirm `go vet` is clean**

Run: `go vet ./...`
Expected: no output (clean).

- [ ] **Step 3 (optional, manual end-to-end): refresh one archive**

With valid cookies and an existing `<name>.cbz`, run:
`go run . sync-comments --refresh-comments --name <name> <manga-url>`
Expected: chapters re-render comments (5-page); the `.cbz` keeps one `zzz-comments.png` per chapter (no duplicates) and images are untouched. Per project convention, run `./package-cbz.sh` afterward if a download/render occurred.

---

## Notes for the implementer

- **Domain:** this downloads manga chapters + reader comments into `.cbz` (zip) archives. `comments.Scrape` fetches comments; the `pipeline` plans which chapters to (re)process; `archive` stages a scratch dir into the `.cbz`.
- **Why the staging fix is mandatory:** without Task 4, a refresh produces two `zzz-comments.png` entries per chapter (zip allows duplicate names; `verifyArchive` does not catch it). Task 4 is not optional polish.
- **Scope guardrails:** do NOT touch the MCP layer (`internal/mcp/*`), do NOT make the page count configurable, and do NOT change the `Resume`/`SyncManga` planner branches — all explicitly out of scope per the spec.
- **Empty re-scrape:** if a refresh scrape yields zero comments, `executeTask` writes no `zzz-comments.png` to scratch, so the skip-set won't contain it and the old comments page is preserved. Refresh replaces only when there is something new; it never deletes. This is intended.
