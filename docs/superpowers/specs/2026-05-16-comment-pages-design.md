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
  comments for every chapter in the source list).
- `resume` → also behaves like a fresh full download (nothing
  already in archive ⇒ everything is "new").
- `sync-comments` → no-op + warning ("no archive to backfill at
  `<path>`; run `sync-manga` first"); exit 0.

If `<root>/<name>.cbz` exists, modes behave per the matrix above.

The CBZ filename uses the existing slug/`--name` rules — there is
exactly one archive per manga.

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
POST https://truyenqqko.com/frontend/comment/list
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
│   ├── folder.go              // Width([]site.Chapter) int;
│   │                           // Folder(root, number, width) string
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
│   ├── reader.go               // ListChapterDirs, HasComments,
│   │                           // CopyRawTo (CreateRaw + OpenRaw)
│   ├── writer.go               // StageAndRename
│   └── archive_test.go
└── downloader/
    └── …                       // Refactored to feed scratch dir
                                // + delegate bundling to archive/.
```

### Single per-run pipeline

```
1. List source chapters:
       chapters := site.ListChapters(ctx, mangaURL)
       width := layout.Width(chapters)

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
   All outputs land in a scratch directory:
       <scratchRoot>/<folder>/001.jpg, 002.jpg, …
       <scratchRoot>/<folder>/zzz-comments.png

5. Stage and rename:
       archive.StageAndRename(<name>.cbz, scratchRoot)
       // copies existing entries via CreateRaw + OpenRaw,
       // appends every file under scratchRoot,
       // verifies via unzip -t, then os.Rename.

6. Delete scratchRoot.
```

If the archive does not exist before step 5, `StageAndRename` writes
a fresh `<name>.cbz` directly (no source entries to copy forward).

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
    Have         map[string]bool // folder names with ≥1 image entry
    HaveComments map[string]bool // subset that also has zzz-comments.png
}

func Inspect(cbzPath string) (Inspection, error)

// StageAndRename atomically merges `scratchRoot/**` into cbzPath.
// If cbzPath does not exist, a fresh archive is written from
// scratch. Existing entries are preserved byte-for-byte via the
// raw-copy mechanism (CreateRaw + OpenRaw — see "Safe archive
// update"). The tmp file is created alongside cbzPath so os.Rename
// is atomic.
func StageAndRename(cbzPath, scratchRoot string) error
```

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
5. Exec `unzip -t <name>.cbz.tmp` as a cheap verification (returns 0
   on a healthy archive).
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
| `unzip -t` on tmp fails | Delete tmp, log, exit 1. Original untouched. |
| SIGKILL between step (1) and step (6) | Tmp orphaned, original untouched. On next run, `os.Create` overwrites the orphan. (Optional: start-of-run sweep removes `*.cbz.tmp` older than 1 hour.) |
| `sync-comments` invoked but `.cbz` does not exist | Print "no archive to backfill at `<path>`; run `sync-manga` first"; exit 0. |

## Migration from current state

Existing on-disk artefacts:

- `~/Documents/Manga/<name>.cbz` files — these are the input to the
  new design. Untouched until the first run; updated via
  stage-and-rename on the first applicable run.
- `~/Documents/Manga/<name>/chap-*/` folders (if any survive) —
  obsoleted. The new tool does not look at them. Users may delete
  them manually after confirming their corresponding `.cbz` is
  intact. We won't write a migration script — the user has already
  consolidated to CBZ-only.
- `~/Documents/Manga/<name>/.chapters.json` — gone with the
  folders. The new tool re-fetches the chapter list per run.

`package-cbz.sh` is no longer the bundling path. It is kept in the
repo for backwards compatibility with any user who has a chapter
folder they want to convert manually, but it is no longer invoked
by any documented workflow. The README is updated accordingly.

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
- **Mode dispatch tests** (lightweight): fake `Site`, fake
  `Fetcher`, fake archive Inspection. For each mode, assert the
  task list contains exactly the expected work for a manga whose
  chapter list and archive state are configured to exercise the
  matrix (some new chapters, some existing chapters with comments,
  some existing chapters without).

## Open / deferred decisions

- **Twemoji bundle size.** Full set is ~4 MB embedded. Acceptable
  per the project's single-binary philosophy.
- **Hi-DPI rendering.** Output is 1× for now. Trivial to bump to 2×
  later.
- **No-comments PNG.** Chapters with zero comments get no PNG, so
  `sync-comments` will re-scrape them every run (~1 GET per
  chapter, no AJAX POST). For ~15 manga × hundreds of chapters
  that's bounded. Could add a manga-level sidecar of "checked,
  empty" markers later if the cost becomes noticeable.
- **`sync-manga` semantics when archive exists but is partial /
  damaged.** Currently `sync-manga` does not re-fetch chapters whose
  image folders are already in the archive (even if they contain
  zero images, which shouldn't happen but…). A future "repair"
  mode could detect a chapter folder with fewer images than the
  source page advertises and refetch. Out of scope for v1.
