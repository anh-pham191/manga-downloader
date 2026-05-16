# Manga + Comments Sync — Design Spec

**Date:** 2026-05-16 (v4 — CBZ-only redesign)
**Branch:** `feature/comment-pages`
**Status:** Design — pending implementation

---

## Goal

Make the downloader operate against `.cbz` archives as its single
source of truth. Add reader-comment pages as the final page of each
chapter. Three explicit modes describe what to do:

| Mode | Download images for NEW chapters | Render comments for NEW chapters | Backfill comments on chapters ALREADY in CBZ |
|---|---|---|---|
| `sync-comments` | ❌ | ❌ | ✅ |
| `resume` | ✅ | ✅ | ❌ |
| `sync-manga` | ✅ | ✅ | ✅ |

"New chapter" = present in the source site's chapter list but not
yet present in the `.cbz`.

This subsumes and replaces the previous folder-based layout (one
chapter per folder, `.done` sentinel, `package-cbz.sh` post-step).
Chapter folders only exist as ephemeral scratch during a run.

## Non-goals

- Preserving the old folder-on-disk workflow as a first-class path.
  Existing user folders (if any survive on disk) are bundled once
  and then discarded; the long-term state is the `.cbz`.
- Re-downloading existing chapters in `sync-manga`. The mode does
  *backfill* of missing comments and *forward* of new chapters; it
  does not re-fetch already-archived images. A "force re-download"
  mode is explicitly out of scope.
- Capturing every comment ever posted. We capture the first two
  pages of comments per chapter.
- Avatar rendering.
- Per-reply threading.
- Refreshing comments over time. Once a chapter has a
  `zzz-comments.png` inside the `.cbz`, its comments are frozen.

## User-visible behaviour

Three subcommands on the existing binary:

```
./bin/downloader sync-manga    [flags] <manga-url>
./bin/downloader resume        [flags] <manga-url>
./bin/downloader sync-comments [flags] <manga-url>
```

Shared flags (carried over from today's CLI):

| Flag | Default | Notes |
|---|---|---|
| `--out` | `~/Documents/Manga` | Root folder. Each manga is `<root>/<name>.cbz`. |
| `--name` | URL slug | Pass `--name "Friendly Title"` to control the archive filename. |
| `--concurrency` | `4` | Chapters in flight. |
| `--from N` / `--to M` | none | Inclusive chapter-number range filter. Applies to image download in `resume` / `sync-manga`; ignored by `sync-comments` (which works on what's already in the archive). |
| `--cookies` | platform default | Same `cf_clearance` JSON as today. |
| `--verbose` | off | Per-chapter progress to stderr. |

If `<root>/<name>.cbz` does not exist:

- `sync-manga` → behaves like a fresh full download (images +
  comments for every chapter in the source list). Only this mode
  bootstraps an archive from nothing.
- `resume` → no-op + warning ("no archive at `<path>`; run
  `sync-manga` first to bootstrap"); exit 0. **Resume only adds to
  existing state.** This prevents a typo in `--name` from silently
  starting a multi-GB fresh mirror.
- `sync-comments` → no-op + warning ("no archive to backfill at
  `<path>`; run `sync-manga` first"); exit 0.

If `<root>/<name>.cbz` exists, modes behave per the matrix above.

The CBZ filename uses the existing slug/`--name` rules — there is
exactly one archive per manga.

**CLI migration from the current binary:**

The current `main.go` exposes a `--resume` boolean flag (and prints
"re-run with --resume" in two error messages). Those collide with
the new `resume` subcommand. The migration:

- `--resume` boolean flag is **removed**.
- The three subcommands replace it. There is no default — invoking
  `./bin/downloader <url>` with no mode prints usage and exits 2.
- The two error messages currently saying "re-run with --resume"
  change to "re-run with `resume` or `sync-manga`."
- `--from` / `--to` / `--concurrency` / `--cookies` / `--name` /
  `--verbose` / `--out` are all preserved.

## Source data — chapters and comments

The source-site adapter (`internal/site/source`) already returns a
sorted, deduplicated `[]site.Chapter{Number, URL}` for a manga URL.
That existing call is the canonical list of chapters available
upstream. No change needed there.

The site renders the first page of comments server-side inside
`<div id="comment_list">`. Each parent comment is one
`<article class="info-comment child_<ID> parent_0 ...">` element
containing:

- `<strong class="level name_<N>">` — username
- `<span class="title-user-comment title-member level_<N>">` — user
  level chip (e.g. "Giới Chủ", "Cấp 7")
- `<div class="content-comment">` — body text. May contain legacy
  emote `<img class="lazy-image" alt="emo">` tags that we strip.
- `<span class="total-like-comment">` — like count

Hidden inputs on the chapter page give us the IDs for pagination:

```html
<input id="book_id" value="13680" />
<input id="episode_id" value="738316" />
<input id="team_id" value="0" />
```

Page 2 (and beyond) loads via:

```
POST https://<source-site>/frontend/comment/list
Content-Type: application/x-www-form-urlencoded

book_id=<book_id>&parent_id=0&page=2&episode_id=<episode_id>&team_id=0
```

The response is an HTML fragment of the same `<article>` blocks the
chapter page renders for page 1.

## Archive layout (state model)

Inside `<name>.cbz`:

```
chap-NNNN[-K]/
├── 001.jpg … MMM.jpg     // chapter images
└── zzz-comments.png      // optional: rendered comments page
```

- `chap-NNNN[-K]/` is the existing folder-name format
  (`internal/downloader/downloader.go:chapterFolder` — dynamic
  zero-padding, `-K` suffix for fractional chapters).
- Presence of *any* image in `chap-NNNN/` ⇒ chapter images are
  considered "in archive."
- Presence of `chap-NNNN/zzz-comments.png` ⇒ comments are in
  archive.
- The `zzz-` prefix sorts last alphabetically, so comic readers
  show it as the final page of the chapter.

**No `.done` / `.comments.done` sentinels.** The archive contents
are the source of truth. This is intentional: the old sentinels
were tied to chapter folders on disk, which no longer exist.

## Architecture

```
internal/
├── layout/                    // NEW — shared chapter-folder naming
│   ├── folder.go              // Width, Folder, InferredWidth,
│   │                           // ImageName(idx) string,
│   │                           // IsImageEntry(name) bool,
│   │                           // CommentsFilename const
│   └── folder_test.go
├── fetcher/
│   └── …                       // Existing GET, plus new Post (below).
├── site/source/
│   └── …                       // Unchanged.
├── comments/
│   ├── scraper.go              // page-1 from chapter HTML +
│   │                           // page-2 POST → []Comment
│   ├── scraper_test.go
│   ├── renderer.go             // []Comment → PNG (pure Go)
│   ├── renderer_test.go
│   ├── testdata/
│   │   ├── chapter-with-comments.html
│   │   ├── page2-fragment.html
│   │   └── shaping-fixture.png // ZWJ + skin-tone + NFD Vietnamese
│   └── assets/
│       ├── NotoSans-Regular.ttf
│       ├── NotoSans-Bold.ttf
│       └── twemoji/<seq>.png   // 1f468-200d-1f469-200d-1f467.png
├── archive/                   // NEW — read .cbz + safe stage-and-rename
│   ├── reader.go               // Inspect, InferredWidth (delegates
│   │                           // to layout.InferredWidth)
│   ├── writer.go               // StageAndRename
│   └── archive_test.go
├── pipeline/                  // NEW — wires the per-run flow
│   ├── pipeline.go             // Run(ctx, Opts) error
│   ├── plan.go                 // Plan(mode, chapters, inspection) []Task
│   ├── plan_test.go            // mode-matrix dispatch tests
│   └── pipeline_test.go
└── downloader/                // Shrinks: see "downloader residuals"
    ├── images.go               // FetchChapterImages(ctx, chapURL,
    │                           //   dest, layout.ImageName) error
    └── images_test.go
```

The orchestration entry point:

```go
// Package pipeline runs one manga sync end-to-end.
package pipeline

type Mode int
const (
    SyncComments Mode = iota
    Resume
    SyncManga
)

type Opts struct {
    Mode         Mode
    MangaURL     string
    Root         string         // <root> (e.g. ~/Documents/Manga)
    Name         string         // <name>; archive lives at <root>/<name>.cbz
    From, To     int            // 0 = unbounded
    Concurrency  int            // default 4
    Site         site.Site      // injected (testable)
    Fetcher      fetcher.Fetcher
    Renderer     comments.Renderer // injected
    Verbose      bool
    Logger       *log.Logger
}

// Run executes one of the three modes. Returns nil on success;
// non-nil errors are user-presentable (Cloudflare expiry, lock
// contention, verification failure, etc.).
func Run(ctx context.Context, opts Opts) error
```

`Plan(mode, chapters, inspection) []Task` is the pure-function
core of mode dispatch — the lightweight matrix tests target this
directly.

### Image-name format lives in one place

The downloader writes images as `001.jpg`, `002.jpg`, … (zero-
padded to 3 digits). The archive reader matches the same pattern
to populate `Inspect.Have`. To prevent the two from drifting, both
go through `internal/layout/`:

```go
// ImageName returns the filename for the N-th image in a chapter
// (1-indexed): "001.jpg", "002.jpg", …
func ImageName(index int, ext string) string

// IsImageEntry reports whether a zip entry name matches the
// downloader's image-name pattern inside a chapter folder, e.g.
// "chap-0001/001.jpg". The comments PNG ("zzz-comments.png") is
// not an image entry.
func IsImageEntry(zipEntryName string) bool

// CommentsFilename is the fixed name used for the rendered
// comments PNG inside a chapter folder.
const CommentsFilename = "zzz-comments.png"
```

The downloader calls `ImageName`; the archive reader calls
`IsImageEntry`; the renderer writes to `CommentsFilename`. One
source of truth.

### Single per-run pipeline

```
0a. Acquire an exclusive file lock to keep concurrent invocations
    from clobbering each other:
        lockPath := <root>/<name>.cbz.lock
        lock, err := flock.NewLock(lockPath, flock.NonBlocking)
        if err: exit 2 with "another downloader is running for
                            <name> — release the lock or wait"
    The lock file is created if missing; it's the lock itself (an
    OS advisory lock on its file descriptor), so unlinking on exit
    is optional. Use github.com/gofrs/flock or syscall.Flock.

0b. Pick scratch root:
       scratchRoot := filepath.Join(<root>, "." + <name> + ".scratch")
   // Co-located with <name>.cbz (NOT $TMPDIR) so a half-finished
   // run is obvious to the user. Deleted at the end of a clean
   // run. On a re-run after a kill, per-chapter subdirs WITH a
   // `.ok` marker are kept (the work is complete); subdirs
   // WITHOUT a `.ok` marker are RemoveAll'd and recreated before
   // their worker starts.

1. List source chapters:
       chapters := site.ListChapters(ctx, mangaURL)
       width := layout.Width(chapters)   // BEFORE any --from/--to
                                          // filter; see "Width must
                                          // see the full list".
       chapters = filterRange(chapters, *from, *to)

2. Inspect existing archive (if any):
       have, haveComments := archive.Inspect(<name>.cbz)
       // have: set of "chap-NNNN[-K]" folder names with ≥1 image entry.
       // haveComments: subset of `have` that also has zzz-comments.png.

3. Decide work per mode:
       for each c in chapters:
           folder := layout.Folder("", c.Number, width)
           in := have.Contains(folder)
           switch mode:
               sync-comments:
                   if in && !haveComments.Contains(folder):
                       tasks += RenderComments(c)
               resume:
                   if !in:
                       tasks += DownloadImages(c) + RenderComments(c)
               sync-manga:
                   if !in:
                       tasks += DownloadImages(c) + RenderComments(c)
                   else if !haveComments.Contains(folder):
                       tasks += RenderComments(c)

4. Execute tasks (worker pool, default concurrency 4).
   All outputs land in the scratch directory:
       <scratchRoot>/<folder>/001.jpg, 002.jpg, …
       <scratchRoot>/<folder>/zzz-comments.png
       <scratchRoot>/<folder>/.ok           // marker, see below
   Each worker writes its chapter's images first, then
   zzz-comments.png (if applicable), then `.ok` LAST. The `.ok`
   file is the per-chapter "fully done" signal.

5. Stage and rename:
       archive.StageAndRename(<name>.cbz, scratchRoot)
       // copies existing archive entries via CreateRaw + OpenRaw;
       // walks scratchRoot/* — for each chapter subdir with a .ok
       // marker, appends all files EXCEPT .ok; chapter subdirs
       // without .ok are skipped (their work failed mid-run);
       // verifies the rewritten archive (see "Verification"),
       // then os.Rename.

6. Delete scratchRoot on clean exit.
```

If the archive does not exist before step 5, `StageAndRename` writes
a fresh `<name>.cbz` directly (no source entries to copy forward).

### Width: full list AND archive-stable

Two distinct width hazards have to be addressed together:

1. **Filter-then-width** corrupts folder names. `filterRange` in
   the current `internal/downloader/downloader.go` aliases its
   input slice (`in[:0:0]` shares the underlying array), so width
   *must* be computed on the unfiltered list — otherwise a
   `--from 100 --to 110` run on a 500-chapter manga picks width 3,
   produces `chap-100/...` instead of `chap-0100/...`, and diverges
   from the archive's existing names.
2. **Mid-life growth** silently invalidates the archive. A manga
   that goes 9,999 → 10,000 chapters upstream would otherwise jump
   width 4 → 5, making *every* existing `chap-NNNN/` in the archive
   un-match the new 5-wide keys. `have.Contains(folder)` would
   return false for every existing chapter and `sync-manga` /
   `resume` would re-download the entire back catalogue.

The fix is one rule: **the effective width is the maximum of the
width the source list needs and the width already used in the
archive.** Once an archive has settled on 4-wide names, all future
runs honour 4 — even if the source now wants 5. New chapters past
the boundary still fit because the existing `Folder` logic
zero-pads to the width given; a 6-digit chapter under a 4-width
archive would just emit `chap-100000/` (still wider than the
existing names, still lexicographically sortable after them).

Pseudocode (step 1 of the pipeline, replacing the naked
`width := layout.Width(chapters)`):

```
sourceWidth := layout.Width(chapters)               // full, unfiltered list
archiveWidth := archive.InferredWidth(have)         // 0 if archive empty
width := max(sourceWidth, archiveWidth)
```

`archive.InferredWidth` returns the consistent zero-padding width
observed across the archive's `chap-NNNN/` entries (it's the count
of leading `0`+digit chars in the longest run-length consistent
prefix; 0 if no chapters are present). If the archive has mixed
widths from past bugs, log a warning and pick the maximum.

### Fetcher changes

`internal/fetcher/fetcher.go` currently exposes a GET-only interface
shaped around a `Request{URL, Referer}` / `*Response{Body,
ContentType}` pair. The page-2 comments endpoint is `POST
application/x-www-form-urlencoded`, so the interface gains a sibling
that mirrors the existing shape:

```go
type Fetcher interface {
    Get(ctx context.Context, req Request) (*Response, error)
    Post(ctx context.Context, req Request, form url.Values) (*Response, error)
}
```

`Request.Referer` is reused — the AJAX endpoint's hot-link guard
will likely reject requests with no Referer. The HTTP implementation
mirrors the existing `Get` (cookie jar, User-Agent, 3× retries on
429/5xx/dial, 200–500 ms jitter, `cf_clearance`-expiry 403
handling). The test fake gains the matching method.

**Pre-implementation spike:** before merging, verify with one curl
call that `POST /frontend/comment/list` with `cf_clearance` returns
200, not a Cloudflare challenge. If POST is challenged differently
from GET, the design needs revisiting.

### Layout package

Extracted from `internal/downloader/downloader.go` with signatures
that match the existing implementations exactly — a straight move,
not a redesign:

```go
// Width returns the zero-padding width wide enough for the largest
// chapter number in the list, with a floor of 4.
func Width(chapters []site.Chapter) int

// Folder turns a published number like "227.5" into a
// filesystem-friendly, lexicographically-sortable name like
// <root>/chap-0227-5.
func Folder(root, number string, width int) string
```

The downloader's existing `folderWidth` / `chapterFolder` become
one-line delegates. The archive injector and the comments renderer
both call `layout.Folder("", num, w)` for join keys.

### Comments package

```go
type Comment struct {
    Name      string  // "sukuna"
    Level     string  // "Giới Chủ" — may be empty
    Body      string  // plain text, emote <img> stripped, NFC-normalised
    LikeCount int
}

// Scrape fetches page 1 from the chapter HTML and page 2 from the
// AJAX endpoint. Returns at most ~6 comments × 2 pages.
func Scrape(ctx context.Context, chapterURL string, f fetcher.Fetcher) ([]Comment, error)

// Render writes a PNG of the comment list to w.
func Render(cs []Comment, w io.Writer) error
```

No timestamp field — the site renders relative times client-side
("3 ngày trước") so the timestamp is not in the server HTML.

### Archive package

```go
type Inspection struct {
    Have         map[string]bool // chap-NNNN[-K] folders with ≥1 image entry
    HaveComments map[string]bool // subset that also has zzz-comments.png
}

func Inspect(cbzPath string) (Inspection, error)

// StageAndRename atomically merges `scratchRoot/<chap-*>/**` into
// cbzPath. Only chapter subdirs that contain a `.ok` marker are
// included; the marker itself is not written into the archive.
// If cbzPath does not exist, a fresh archive is written. Existing
// entries are preserved byte-for-byte via the raw-copy mechanism
// (CreateRaw + OpenRaw — see "Safe archive update"). The tmp file
// is created alongside cbzPath so os.Rename is atomic.
func StageAndRename(cbzPath, scratchRoot string) error
```

**Pinning `Have` / `HaveComments` semantics.** The folder-name key
is exactly the string `layout.Folder("", number, width)` returns —
no trailing slash, no `./` prefix. `Inspect` derives keys from zip
entries by taking the substring before the first `/` of each
entry's name (zip stores forward-slashes regardless of OS).

An entry counts as an **image entry** if its name matches:

```
^chap-\d+(-\d+)?/[^/]+\.(jpg|jpeg|png|webp)$
```

…AND the filename portion is not `zzz-comments.png`. That avoids
counting the comments PNG as an image, and ignores stray files
like `.DS_Store` or `.chapters.json` if any leaked into an older
archive.

The folder counts as **having comments** if any entry matches:

```
^chap-\d+(-\d+)?/zzz-comments\.png$
```

`layout.Folder("", number, width)` never emits a trailing slash —
`Inspect` keys match it exactly.

## Rendering details

- Canvas: 1000 px wide; height grows with content.
- Header band (~80 px): "Bình Luận (M)" left-aligned, divider line
  below.
- Per-comment block:
  - Bold username (24 px) + grey level chip + 👍 like count right
  - Body text (20 px) word-wrapped to canvas width minus margins,
    line spacing 1.4
  - Thin grey separator below
- Background: white. Text: near-black (#222). Chip & meta text:
  grey (#888).

### Text shaping

Two complications that "codepoint by codepoint" hides:

1. **Vietnamese mark positioning.** Bare freetype +
   `golang.org/x/image/font` does not do OpenType mark positioning,
   so combining diacritics (`e` + U+0302 + U+0301 for `ế`) render as
   stacked-wrong. The body text needs a real shaper:
   **`github.com/go-text/typesetting`** for shaping plus
   `golang.org/x/image/font/opentype` for glyph rasterisation.
   Defensively, the scraper normalises body text to NFC so most
   Vietnamese arrives precomposed.
2. **Emoji grapheme clusters.** ZWJ sequences (`👨‍👩‍👧` =
   `1F468 200D 1F469 200D 1F467`) and skin-tone modifiers
   (`👍🏽` = `1F44D 1F3FD`) are one visual glyph but multiple
   codepoints. Iteration uses grapheme-cluster segmentation via
   **`github.com/rivo/uniseg`**. Lookup order per cluster: NFC body
   text first (NFC preserves FE0F), then for each cluster strip
   FE0F variation selectors and build a `-`-joined hex codepoint
   filename for the Twemoji bundle. Twemoji filenames don't include
   FE0F, so the strip is required at lookup time.

### De-risk gate (Vietnamese required; emoji best-effort)

Before wiring the renderer into the sync pipeline, the plan builds
one test:

```
TestRenderFixture_ShapingAndEmoji
```

It renders one fixture containing:

- Vietnamese in both NFC and NFD forms (`ế` vs `ế`)
- A ZWJ family emoji (`👨‍👩‍👧`)
- A skin-tone-modified emoji (`👍🏽`)
- A plain BMP emoji (`😀`)
- Mixed Latin + Vietnamese in one line

Assertions are **structural, not byte-exact** (antialiasing drifts
across builds), and split into hard and soft tiers:

**Required (gate):**
- The output is a non-empty, valid PNG.
- Vietnamese composed and decomposed forms render correctly — no
  stacked-mark signature (a heuristic for broken diacritics).
- The renderer does not crash or hang on any of the emoji inputs.

**Best-effort (warn, don't fail):**
- ZWJ family and skin-tone emoji render as one cluster.
- Twemoji-sourced regions are colour (saturation above a threshold)
  while text regions are near-monochrome.

If pure Go can't make these pass, **we accept degraded emoji
output**. Vietnamese MUST render correctly; emoji are decoration.
No headless-browser fallback — the project stays a single pure-Go
binary.

### Bidi (deferred)

Vietnamese is LTR Latin, so RTL bidi is moot in practice. If a
commenter pastes Arabic/Hebrew we accept visual mojibake —
explicitly out of scope.

## Concurrency

A single chapter worker pool (default size 4) processes the task
list. Tasks are independent — each writes its outputs into a
chapter-specific subdirectory of the scratch root, so workers do
not contend on files.

The archive write at the end of the run is a single serialised
step. Multiple workers never mutate the `.cbz`.

Across mangas, runs are serial (one `<url>` per invocation). The
existing CLI doesn't bulk-process and this design doesn't add that.

## Safe archive update

The naive plan (per-chapter `zip -u <name>.cbz chap-NNNN/...`) has
two problems:

- Concurrent writes corrupt the central directory; the pool would
  have to be serialised regardless.
- A SIGKILL or power loss during the central-directory rewrite
  leaves payload bytes on disk but an unreadable archive. Worse,
  any "PNG presence is the sentinel" logic would silently retry
  against an already-corrupt archive on the next run.

The implementation uses a **stage-and-rename** pattern, once per
run, not per chapter:

1. Create `<name>.cbz.tmp` **in the same directory as `<name>.cbz`**
   (i.e. alongside the manga, not in `$TMPDIR`). `os.Rename` is only
   atomic within a filesystem; co-locating guarantees that property.
   If an existing `<name>.cbz.tmp` is present (orphaned from a prior
   killed run), `os.Create` overwrites it.
2. If `<name>.cbz` exists, stream every entry from it into the tmp
   with no decompression cycle:

   ```
   for _, f := range zr.File:
       rc, _ := f.OpenRaw()                  // undecompressed bytes
       hdr := f.FileHeader                   // preserves method,
                                              // CRC32, sizes, name
       w, _ := zw.CreateRaw(&hdr)            // raw mode
       io.Copy(w, rc)
   ```

   `CreateRaw` requires `CRC32`, `CompressedSize64`, and
   `UncompressedSize64` to already be populated on the header —
   `f.FileHeader` already has them. Using `Open()` + `Create()`
   would decompress and re-deflate every entry, turning seconds
   into minutes on multi-GB archives.

3. Walk `scratchRoot/**` and add each file with `Method =
   zip.Store` (compression level 0) to match the existing store-only
   archive convention. Image bytes (JPEG/WebP) and PNG comment
   pages don't benefit from deflate.
4. Close the writer (this writes the central directory).
5. Verify the tmp archive in pure Go: `zip.OpenReader(tmp)`,
   iterate every entry, and for each call `f.OpenRaw()` +
   `io.Copy(io.Discard, ...)` so the reader walks the compressed
   payload and surfaces any CRC mismatch or truncated entry. No
   external `unzip` dependency — the project stays single-binary.
6. `os.Rename(<name>.cbz.tmp, <name>.cbz)` — atomic on the same
   filesystem.
7. On any failure between (1) and (6), delete the tmp and exit with
   a clear error. The original `<name>.cbz` is never mutated.

This costs one extra archive-sized write per run (so up to ~7 GB
once for the biggest archives) but the original is *never* in a
half-rewritten state.

**Zip64.** Go's `archive/zip.Writer` automatically emits Zip64
entries when sizes/counts cross the 4 GB / 65535 thresholds.
Archives that were created without Zip64 will be re-emitted with
Zip64 after the rebuild if they cross the threshold. Modern comic
readers handle this.

## Failure modes

| Failure | Handling |
|---|---|
| Cloudflare 403 on chapter page or AJAX endpoint | Surface the existing "refresh `cf_clearance` and re-run" error, exit 1. |
| Page-2 endpoint returns 5xx after retries | Treat as page-1-only; render with what we have; continue. |
| Page-2 response empty / `<article>` count 0 | Same as above. |
| Render error for a chapter (font load fail, image decode fail) | Skip that chapter's comment PNG for this run; continue. Next run retries. |
| Image fetch fails for a chapter mid-run | Chapter's scratch dir incomplete — exclude that chapter from the stage step. Next run retries. |
| Scrape parses zero comments | No `zzz-comments.png` is written. On future `sync-comments` / `sync-manga` runs we'll re-scrape (cheap, ~1 GET). Accepted cost. |
| Chapter exists in `.cbz` but not in fresh chapter list (renumbered) | Log "no source match for `<folder>`", leave existing entry as-is. Never silently delete. |
| Chapter renamed upstream (same number, new URL slug) | `have.Contains(folder)` is true (same number ⇒ same folder), so all modes skip image re-download. Stale URL silently retained. Accepted — a future "repair" mode could re-fetch on URL change. |
| `sync-manga`: archived chapter has fewer images than the source page advertises (truncated/broken chapter from an older run) | Log a `WARN` with the folder name and the image-count delta. Do NOT re-fetch in v1 — full repair is out of scope. The warning surfaces broken state so the user can decide. |
| Pure-Go verification of tmp fails (CRC mismatch / truncated) | Delete tmp, log, exit 1. Original untouched. |
| SIGKILL between step (1) and step (6) | Tmp orphaned, original untouched. On next run, `os.Create` overwrites the orphan. (Optional: start-of-run sweep removes `*.cbz.tmp` older than 1 hour.) |
| `sync-comments` invoked but `.cbz` does not exist | Print "no archive to backfill at `<path>`; run `sync-manga` first"; exit 0. |
| `resume` invoked but `.cbz` does not exist | Print "no archive at `<path>`; run `sync-manga` first"; exit 0. |
| Lock acquisition fails (another downloader is running) | Print "another downloader is running for `<name>`"; exit 2. |
| Killed mid-run, then re-invoked | Scratch subdirs with `.ok` are reused (no re-fetch / re-render); subdirs without `.ok` are wiped and re-attempted. This survives cookie-refresh kills cleanly — already-completed chapters don't pay bandwidth twice. |

## Migration from current state

Existing on-disk artefacts:

- `~/Documents/Manga/<name>.cbz` files — these are the input to the
  new design. Untouched until the first run; updated via
  stage-and-rename on the first applicable run.
- `~/Documents/Manga/<name>/chap-*/` folders (if any survive) —
  obsoleted. The new tool does not look at them. Users may delete
  them manually after confirming their corresponding `.cbz` is
  intact. No migration script — the user has already consolidated
  to CBZ-only.
- `~/Documents/Manga/<name>/.chapters.json` — gone with the
  folders. The new tool re-fetches the chapter list per run.

`package-cbz.sh` is no longer the bundling path. It is kept in the
repo for backwards compatibility with any user who has a chapter
folder they want to convert manually, but it is no longer invoked
by any documented workflow.

### `downloader` package residuals

After the layout extraction (→ `internal/layout/`) and the bundling
move (→ `internal/archive/`), `internal/downloader/` shrinks
significantly. What it keeps:

- The per-chapter image-fetch loop: given a chapter URL and a
  destination directory, fetch the chapter's image list and write
  zero-padded files (`001.jpg`, `002.jpg`, …) into the directory.
  The pipeline calls this once per "needs images" task.
- The atomic-write helper (`.part` → rename).
- Existing retries and the Cloudflare-403-as-fatal handling.

What moves out:

- Folder-naming → `internal/layout/`
- Bundling/archive I/O → `internal/archive/`
- `.done` / `.chapters.json` sentinel writing → deleted (archive is
  the truth now)
- Manga-level orchestration → a new tiny `internal/pipeline/`
  package (or kept in `main.go` if it stays short)

### Doc/work items not in code

- Update `README.md` to describe the three subcommands and CBZ-only
  workflow; remove the chapter-folder layout diagram.
- Update `PLAN.md` (the design notes for the old approach).
- The user's auto-memory at `feedback_downloader_workflow.md`
  currently says *"always run ./package-cbz.sh after a successful
  download"*. That guidance is wrong once this ships — the spec
  marks it for refresh as part of the merge.

## Testing

- **Layout tests**: round-trip — `Folder(num, max)` produces the
  same string for freshly-fetched chapter numbers and the names
  already in checked-in fixture archives. Covers `chap-0032-5`,
  dynamic widths, and the boundary at `max="9999"` vs `"10000"`.
- **Scraper unit tests**: HTML fixtures (`chapter-with-comments.html`
  for page 1, `page2-fragment.html` for the AJAX response). Verify
  field extraction, emote stripping, the page-2 empty-response
  branch, and NFC normalisation.
- **Fetcher POST tests**: extend existing tests — retries on 5xx,
  403 surfaces as Cloudflare-expiry, cookies/UA sent, Referer sent.
- **Renderer de-risk test** (gate): `TestRenderFixture_ShapingAndEmoji`
  as described above. Must pass before any sync code is written.
- **Renderer unit tests**: long-body wrapping, very long usernames,
  zero comments (must not be called), like-count formatting.
- **Archive package**:
  - `Inspect` returns the expected `Have` / `HaveComments` sets for
    a checked-in fixture archive containing two chapters (one with
    a `zzz-comments.png`, one without).
  - `StageAndRename` against an existing archive: rewritten archive
    contains all original entries byte-for-byte (compare raw stored
    bytes via `OpenRaw`), plus the new scratch entries.
  - `StageAndRename` against a non-existent target: creates a fresh
    archive containing only the scratch entries.
  - Re-running with an unchanged scratch dir is a no-op-equivalent
    (the new archive contains the same entries as before).
  - Fractional folder `chap-0032-5/` round-trips correctly.
  - Crash-safety: panic injected after tmp is written but before
    rename — assert original untouched and tmp is cleaned up on
    next run.
- **Mode dispatch tests** (lightweight, target `pipeline.Plan`):
  fake `Site`, fake archive `Inspection`. Enumerate every matrix
  cell explicitly, both positive and negative:

  | Mode | Chapter state | Expected |
  |---|---|---|
  | `sync-comments` | in archive, no comments | render task |
  | `sync-comments` | in archive, has comments | no task |
  | `sync-comments` | not in archive | no task |
  | `resume` | in archive, no comments | no task (no backfill) |
  | `resume` | in archive, has comments | no task |
  | `resume` | not in archive | download + render |
  | `sync-manga` | in archive, no comments | render task |
  | `sync-manga` | in archive, has comments | no task |
  | `sync-manga` | not in archive | download + render |

- **Width-stability tests** (target `layout` + `archive.InferredWidth`):
  - Width-before-filter: pass `--from 100 --to 110` against a
    500-chapter list; assert the produced folder names match what
    `Folder("",num,4)` would emit on the full list.
  - Mid-life growth: simulate an archive containing
    `chap-0001/.../chap-9999/`; source list now has 10,000
    chapters. Assert the effective width is 4 (the archive's
    inferred width wins), so existing keys still match.
  - Fresh-bootstrap: empty archive + 10,500-chapter source list ⇒
    width 5.
  - `InferredWidth` on an archive with mixed-width entries (from
    historical bugs): returns the maximum and logs a warning.
- **`.ok`-marker exclusion test** (target `archive.StageAndRename`):
  scratch dir contains two chapter subdirs — one with `.ok`, one
  without. Stage and assert: archive contains the first chapter's
  files, the second chapter's files are not present, and `.ok`
  itself never lands in the archive.
- **File lock test**: spin up two concurrent `pipeline.Run`
  invocations against the same manga; assert the second fails fast
  with a "another downloader is running" error and the first
  completes successfully.

## Dependencies / version pins

- `github.com/go-text/typesetting` — text shaping. Pin to a recent
  tagged release; minimum `v0.2.0`.
- `github.com/rivo/uniseg` — grapheme-cluster segmentation. Minimum
  `v0.4.4` for stable cluster behaviour.
- `github.com/gofrs/flock` (or `golang.org/x/sys/unix` direct) —
  inter-process file lock for the pipeline guard.
- **Twemoji asset edition: 15.1.** Vendored under
  `internal/comments/assets/twemoji/` from the upstream
  `twitter/twemoji` repo at the 15.1 tag. Codepoint coverage and
  ZWJ-sequence filename convention are pinned to that release.
  Updates require a deliberate vendoring step.

## Open / deferred decisions

- **Twemoji bundle size.** Full set at 15.1 is ~4 MB embedded.
  Acceptable per the project's single-binary philosophy.
- **Hi-DPI rendering.** Output is 1× for now. Trivial to bump to 2×
  later.
- **No-comments PNG.** Chapters with zero comments get no PNG, so
  `sync-comments` will re-scrape them every run (~1 GET per
  chapter, no AJAX POST). For ~15 manga × hundreds of chapters
  that's bounded. Could add a manga-level sidecar of "checked,
  empty" markers later if the cost becomes noticeable.
- **`sync-manga` semantics when archive is partial / damaged.**
  `sync-manga` does not re-fetch chapters whose folders are
  already in the archive. The image-count-mismatch warning above
  surfaces the state; a future "repair" mode could re-fetch. Out
  of scope for v1.
- **Write amplification.** Stage-and-rename copies the entire
  archive every run, so a `sync-comments` pass over all archives
  is ~80 GB of writes (cumulative across ~15 archives) to add ~15
  PNGs. Accepted as the price of crash safety. Mitigation later
  could short-circuit when there's no work for an archive (skip
  the rewrite entirely).
