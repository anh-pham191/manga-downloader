# Comment Pages — Design Spec

**Date:** 2026-05-16
**Branch:** `feature/comment-pages`
**Status:** Design — pending implementation

---

## Goal

After each downloaded chapter, append a rendered PNG of that chapter's
reader comments as the last page of the chapter folder, so it shows up
as the final page when the manga is read via CBZ in any comic reader.
The same flow doubles as a backfill: re-running `--resume` on a manga
that finished before this feature shipped fills in comment pages for
every existing chapter.

## Non-goals

- Capturing every comment ever posted. We capture the first two pages
  only.
- Refreshing comments over time. Each chapter's comments are scraped
  once; subsequent runs skip chapters that already have a
  `.comments.done` sentinel.
- Avatar rendering.
- Per-reply threading. The first two pages of the comment list endpoint
  return parent comments; replies are not captured.

## User-visible behaviour

There are two backfill scenarios, both driven by the same `downloader`
binary:

**A. Chapter folders still on disk** (the normal `--resume` path):
`./bin/downloader <url>` (with or without `--resume`) gains a new step
after a chapter's images are saved:

1. If `.comments.done` already exists in the chapter folder → skip.
2. Otherwise: scrape page 1 of comments (already in the chapter HTML)
   and page 2 (one extra POST). If ≥1 comment, render
   `zzz-comments.png`. Always write `.comments.done` afterward, even
   when there are zero comments.

**B. CBZ-only, chapter folders deleted** (the real state of
`~/Documents/Manga/` today — every entry is a `.cbz`, the source
folders are gone): a new mode injects comment pages directly into
the existing archive without a full unzip/rezip.

```
./bin/downloader --comments-only <url>
```

This mode:

1. Re-fetches the manga's chapter list from `<url>` so we have every
   chapter's URL (the on-disk `.chapters.json` was excluded from the
   CBZ, so it's gone).
2. Reads the existing `<name>.cbz` central directory via Go stdlib
   `archive/zip` (no shell-out) to learn which `chap-NNNN[-K]/`
   directories the archive contains.
3. Builds the folder-name → chapter-URL map by calling the **exact
   same** `chapterFolder` / `folderWidth` helpers the downloader
   uses (see "Folder-naming correctness" below). This is the join
   key.
4. Collects all chapters whose `chap-NNNN[-K]/zzz-comments.png` is
   *missing* from the archive, scrapes each, renders each PNG to a
   per-manga scratch directory.
5. **Stages all updates in one tmp archive then atomically renames**
   (see "Safe archive update" below). Does NOT do per-chapter
   `zip -u` against the live `.cbz`.

The presence of `chap-NNNN[-K]/zzz-comments.png` inside the archive
IS the sentinel — no separate `.comments.done` file is needed here
(and can't be, since the archive isn't tracked by per-file
sentinels).

Both paths share the same scraper and renderer; only the persistence
layer differs (filesystem sentinel + folder vs. archive-as-state).

`package-cbz.sh` is updated to exclude `.comments.done` from new
archives so the sentinel never leaks in. The `zzz-` prefix on the
rendered PNG makes it sort last alphabetically, so any comic reader
places it at the end of the chapter.

Flags:

- `--comments` (default `true`) — toggles the comments step on the
  normal download/resume path (scenario A).
- `--comments-only` — runs the scenario-B injector and exits.
  Implies `--resume`-style "skip already-done" behaviour, but
  driven by the archive contents rather than filesystem sentinels.

## Source data

The site (`truyenqqko.com`) renders the first page of comments
server-side inside `<div id="comment_list">`. Each parent comment is
one `<article class="info-comment child_<ID> parent_0 ...">` element
containing:

- `<strong class="level name_<N>">` — username
- `<span class="title-user-comment title-member level_<N>">` — user
  level chip (e.g. "Giới Chủ", "Cấp 7")
- `<div class="content-comment">` — body text. May contain legacy
  emote `<img class="lazy-image" alt="emo">` tags that we strip.
- `<span class="total-like-comment">` — like count

Hidden inputs on the chapter page give us the IDs we need for
pagination:

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

The response is an HTML fragment containing the same
`<article class="info-comment ...">` blocks the chapter page renders
for page 1, suitable to parse with the same selectors.

## Architecture

A new `internal/comments/` package, plus a small reorganisation of
the existing downloader to share its folder-naming logic:

- **`internal/layout/`** (new) — extracted from
  `internal/downloader/downloader.go`. Holds exported `Folder(num
  string, maxNum string) string` and `FolderWidth(maxNum string) int`
  helpers, so the downloader and the archive injector both use the
  identical algorithm for mapping a chapter to its `chap-NNNN[-K]/`
  folder name. The current `chapterFolder` becomes a thin wrapper.
- **`internal/comments/scraper.go`** — given a chapter URL + a
  `fetcher.Fetcher`, returns `[]Comment` for the first two pages.
  Reuses the existing fetcher (cookies, retries, jitter, Cloudflare
  403 handling), but requires a new POST method on the fetcher (see
  "Fetcher changes" below).
- **`internal/comments/renderer.go`** — given `[]Comment` and an
  output `io.Writer`, writes a PNG. See "Rendering details" for the
  Vietnamese + emoji shaping story and the de-risk gate.
- **`internal/comments/folder_sync.go`** — scenario A: scrape →
  render → write into `chap-NNNN[-K]/zzz-comments.png` +
  `.comments.done` sentinel.
- **`internal/comments/archive_sync.go`** — scenario B: list
  existing `.cbz` via stdlib `archive/zip`, re-fetch chapter list,
  scrape + render per missing chapter, stage all PNGs into one
  rebuilt tmp archive, verify with `unzip -t`, atomic rename over
  the original.
- **`internal/comments/assets/`** — embedded font (Noto Sans regular
  + bold) and embedded Twemoji 72×72 PNG set (named by ZWJ-joined
  codepoint sequence, e.g. `1f468-200d-1f469-200d-1f467.png`), both
  via `//go:embed`.

`internal/downloader/` gains one hook in its per-chapter routine,
called after `.done` is written (the scenario-A entry point).

`main.go` gains the `--comments-only` flag dispatch that calls
`comments.SyncArchive` instead of running the normal download flow.

### Fetcher changes

`internal/fetcher/fetcher.go` currently exposes a GET-only interface.
The page-2 comments endpoint is `POST application/x-www-form-urlencoded`,
so the interface gains a sibling:

```go
Post(ctx context.Context, url string, form url.Values) (io.ReadCloser, error)
```

The HTTP implementation mirrors the existing `Get` (cookie jar,
User-Agent, 3× retries on 429/5xx/dial, 200–500 ms jitter, and the
`cf_clearance`-expiry 403 handling). The test fake gains the
matching method.

**Pre-implementation spike:** before merging, verify with one curl
call that `POST /frontend/comment/list` with `cf_clearance` cookie
returns 200, not a Cloudflare challenge. If POST is challenged
differently from GET, scenario B is blocked and we need to revisit.

```
internal/
├── downloader/
│   └── … (existing) + new call into comments.SyncFolder per chapter
├── fetcher/
│   └── … (existing) + new Post() method on the interface & impls
├── layout/                   // NEW — shared chapter-folder naming
│   ├── folder.go             // Folder(num, max) string; FolderWidth(max) int
│   └── folder_test.go
├── comments/
│   ├── scraper.go
│   ├── scraper_test.go
│   ├── renderer.go
│   ├── renderer_test.go
│   ├── folder_sync.go        // scenario A: filesystem + sentinel
│   ├── folder_sync_test.go
│   ├── archive_sync.go       // scenario B: stage-and-rename CBZ
│   ├── archive_sync_test.go
│   ├── testdata/
│   │   ├── chapter-with-comments.html
│   │   ├── page2-fragment.html
│   │   ├── sample.cbz        // small fixture for archive tests
│   │   ├── golden-render.png
│   │   └── shaping-fixture.png  // ZWJ + skin-tone + NFD Vietnamese
│   └── assets/
│       ├── NotoSans-Regular.ttf
│       ├── NotoSans-Bold.ttf
│       └── twemoji/<seq>.png  // names like 1f468-200d-1f469-200d-1f467.png
```

### Data type

```go
type Comment struct {
    Name      string  // "sukuna"
    Level     string  // "Giới Chủ" (the chip text); may be empty
    Body      string  // plain text, emote <img> stripped
    LikeCount int
}
```

No timestamp field — the site renders relative times client-side
("3 ngày trước") via JS, so the timestamp isn't in the server HTML
we scrape. Acceptable to omit.

### Per-chapter file layout

```
chap-NNNN/
├── 001.jpg … NNN.jpg     // existing
├── .done                  // existing — images complete
├── zzz-comments.png       // new — only if ≥1 comment
└── .comments.done         // new — sentinel: comments synced
```

## Flow

**Scenario A — folder + sentinel (normal download/resume):**

```
for each chapter the downloader processes:
    download images (existing)
    write .done (existing)
    if --comments && !.comments.done:
        comments, err := comments.Scrape(ctx, chapterURL, fetcher)
        if err is cloudflareExpiry: surface and exit 1 (existing pattern)
        if err is transient: log, leave .comments.done absent, continue
        if len(comments) > 0:
            render chap-NNNN/zzz-comments.png
        write .comments.done
```

Backfill in this scenario is implicit: an old chapter with `.done`
but no `.comments.done` triggers the same branch on the next
`--resume`. Image download is skipped (existing `.done` check).

**Scenario B — `--comments-only`, archive-only state:**

```
chapters, err := site.ListChapters(ctx, mangaURL, fetcher)  // existing
maxNum := chapters[len(chapters)-1].Number
foldersByURL := map[folder]url{}
for _, c := range chapters:
    foldersByURL[layout.Folder(c.Number, maxNum)] = c.URL

zr, _ := zip.OpenReader(<name>.cbz)
chapDirs := uniqueChapDirsFrom(zr)                 // "chap-0001", "chap-0032-5", ...
have := chapDirsContainingCommentsPNG(zr)
zr.Close()

missing := chapDirs - have
for each dir in missing:
    url, ok := foldersByURL[dir]
    if !ok:
        log "no URL match for", dir, "skipping"; continue
    comments, err := comments.Scrape(ctx, url, fetcher)
    if err is cloudflareExpiry: surface and exit 1
    if err is transient: log, skip this chapter (will retry next run)
    if len(comments) == 0: continue
    render PNG to scratch/<dir>/zzz-comments.png

if no PNGs rendered:
    return  // nothing to inject

// Single staged update for the whole manga (see Safe archive update).
buildTmpArchive(<name>.cbz.tmp, <name>.cbz, scratch/)
verify: exec `unzip -t <name>.cbz.tmp` exit 0
os.Rename(<name>.cbz.tmp, <name>.cbz)
```

The "no URL match" case can happen if the manga's chapter URLs have
been renumbered. The folder-name match handles fractional chapters
(`chap-0032-5`) directly because the join key is the folder name,
not an integer.

## Rendering details

- Canvas: 1000 px wide; height grows with content.
- Header band (~80 px): "Bình Luận (M)" left-aligned, divider line
  below.
- Per-comment block (~min 100 px, grows with body text):
  - Bold username (24 px) + grey level chip + 👍 like count right
  - Body text (20 px) word-wrapped to canvas width minus margins,
    line spacing 1.4
  - Thin grey separator below
- Background: white. Text: near-black (#222). Chip & meta text:
  grey (#888).

### Text shaping is harder than "codepoint by codepoint"

Two real complications that the v1 draft hand-waved:

1. **Vietnamese mark positioning.** Bare freetype +
   `golang.org/x/image/font` does not do OpenType mark positioning,
   so combining diacritics (`e` + U+0302 + U+0301 for `ế`) render as
   stacked-wrong. The body text needs a real shaper. Plan to use
   **`github.com/go-text/typesetting`** for shaping and
   `golang.org/x/image/font/opentype` for glyph rasterisation. As a
   defensive measure the scraper also normalises body text to NFC
   so most Vietnamese arrives precomposed.
2. **Emoji grapheme clusters.** ZWJ sequences (`👨‍👩‍👧` =
   `1F468 200D 1F469 200D 1F467`) and skin-tone modifiers
   (`👍🏽` = `1F44D 1F3FD`) are *one* visual glyph but multiple
   codepoints. Iteration must use grapheme-cluster segmentation via
   **`github.com/rivo/uniseg`**, then longest-match against the
   Twemoji bundle by `-`-joined hex codepoint string. Missing
   variation selectors (FE0F) are stripped before lookup.

### De-risk gate (before adopting pure-Go for real)

Before wiring the renderer into the downloader, the plan builds a
single throwaway test:

```
TestRenderFixture_ShapingAndEmoji_GoldenPNG
```

It renders one fixture comment list containing:

- Vietnamese in both NFC and NFD forms (`ế` vs `ế`)
- A ZWJ family emoji (`👨‍👩‍👧`)
- A skin-tone-modified emoji (`👍🏽`)
- A plain BMP emoji (`😀`)
- Mixed Latin + Vietnamese in one line to exercise the shaper

If this test cannot be made to pass in pure Go within ~one day's
implementation effort, the renderer falls back to **headless Chrome**
(HTML template → `chrome --headless --screenshot`). That is uglier
operationally but renders correctly the first time. This is an
explicit pre-commit gate in the implementation plan.

### Bidi (deferred)

Vietnamese is LTR Latin, so RTL bidi is moot in practice. If a
commenter pastes Arabic/Hebrew we accept visual mojibake — this is
explicitly out of scope and logged in the fallback decision.

## Concurrency

**Scenario A**: reuse the existing chapter worker pool. The comments
step is one or two HTTP requests + a CPU-bound render; it slots into
the same goroutine that already handles a chapter's images. Each
chapter's PNG and sentinel land in a *separate* folder, so workers
do not contend on the same file.

**Scenario B**: scrape + render concurrently (worker pool, same
size as the existing image-download pool), but **the archive write
is a single serialised step at the end** of each manga. We never
have multiple goroutines mutating one `.cbz`. The pseudocode above
reflects this — workers populate the scratch directory in parallel;
the `buildTmpArchive` / rename happens once at the end. Multiple
*mangas* can be processed serially (one at a time) per
`--comments-only` run; cross-manga parallelism is not worth the
complexity for ~15 archives.

## Folder-naming correctness

The downloader's current `chapterFolder` (in
`internal/downloader/downloader.go`) does three things that scenario
B must reproduce exactly:

1. `folderWidth` is dynamic — `max(4, len(maxChapterNumber))` — so a
   manga with chapter 10500 uses 5-digit padding.
2. Fractional chapters are encoded `chap-0032-5`, not `chap-0032.5`.
   The dot-to-dash substitution happens at folder-creation time.
3. The chapter `Number` field is the raw URL token (`12-5`),
   normalised to `12.5` for sorting and reverted for the folder name.

To prevent the archive injector from drifting from this convention,
`chapterFolder` and `folderWidth` move to a new
`internal/layout/folder.go` with exported `Folder` and `FolderWidth`
functions. The downloader's existing call site becomes a one-line
delegate. Tests for the existing folder behaviour move with it.

## Safe archive update (scenario B)

The naive plan (per-chapter `zip -u <name>.cbz chap-NNNN/...`) has
two problems:

- **Concurrent writes** to the same `.cbz` corrupt the central
  directory. The pool would have to be serialised anyway.
- **Crash during CD rewrite** (SIGKILL, power loss) leaves the
  payload bytes on disk but an unreadable archive. Worse, the
  "PNG is the sentinel" idea silently breaks: the next run reads
  the (broken) CD, sees no PNG, retries against an already-corrupt
  archive.

The implementation therefore uses a **stage-and-rename** pattern,
once per manga, not per chapter:

1. Create `<name>.cbz.tmp` as a fresh archive.
2. Stream every entry from the original `.cbz` into the tmp via
   `archive/zip` (`Writer.CreateRaw` + `io.Copy`) — no decompression,
   no re-deflation. Existing entries are byte-identical regions
   copied straight through. The original `.cbz` is store-mode, so
   this is essentially an `io.Copy` of each compressed payload at
   memory-bandwidth speed.
3. Append the newly rendered `chap-NNNN[-K]/zzz-comments.png`
   entries with `Method = zip.Store` (compress level 0) to match
   the rest of the archive.
4. Close the writer (this writes the CD).
5. Exec `unzip -t <name>.cbz.tmp` (returns 0 on a healthy archive)
   as a cheap verification.
6. `os.Rename(<name>.cbz.tmp, <name>.cbz)` — atomic on the same
   filesystem.
7. On any failure between (1) and (6), delete the tmp and exit
   with a clear error. The original `<name>.cbz` is never mutated.

This costs one extra `~size-of-cbz` write per manga (so up to ~7 GB
once for the biggest archives) but the original is *never* in a
half-rewritten state, and we don't need a per-archive mutex — the
write is single-threaded by construction.

**Zip64.** Go's stdlib `archive/zip.Writer` automatically emits
Zip64 entries when sizes/counts cross the 4 GB / 65535 thresholds.
Archives that were created without Zip64 will be re-emitted with
Zip64 after the rebuild if they cross the threshold. Modern comic
readers handle Zip64; this is noted, not a blocker.

## Failure modes

| Failure | Handling |
|---|---|
| Cloudflare 403 on chapter page or AJAX endpoint | Surface "refresh cf_clearance + --resume" error (existing pattern), exit 1. |
| Page-2 endpoint returns 5xx after retries | Treat as page-1-only; write `.comments.done` (A) or render PNG with only page-1 comments (B); continue. |
| Page-2 response empty / `<article>` count 0 | Same as above. |
| Render error (font load fail, image decode fail) | Fail the chapter — do NOT write `.comments.done` (A) / do NOT include this chapter's PNG in the staged archive (B). Next run retries. |
| Scrape parses zero comments on page 1 too | A: write `.comments.done`, no PNG. B: skip this chapter; on next run we'll re-scrape (cheap, ~1 HTTP request). |
| Scenario B: chapter exists in archive but not in re-fetched chapter list | Log "no URL match for `<folder>`", skip. Never silently wrong. |
| Scenario B: `unzip -t` of the tmp archive fails | Delete the tmp, log the error, exit 1. Original `.cbz` untouched. |
| Scenario B: SIGKILL between step (1) and step (6) | Tmp is orphaned, original `.cbz` untouched. Next run starts fresh. (Optional cleanup: at startup, remove any `*.cbz.tmp` files older than 1 hour.) |

## CBZ packaging

`package-cbz.sh` uses `zip -u -0` (store-mode update) already, so
re-running it after a scenario-A backfill appends new
`zzz-comments.png` files to existing archives in seconds. One change
to the script: add `.comments.done` to the exclude list (alongside
`.done`, `.chapters.json`, `.DS_Store`) so the sentinel doesn't leak
into the archive.

Scenario B does *not* go through `package-cbz.sh`; it rebuilds the
archive in Go directly (see "Safe archive update").

## Testing

- **Layout tests**: cover the round-trip — `Folder(num, max)`
  produces the same string for both freshly-fetched chapter numbers
  and the names already in checked-in fixture archives. Cover
  `chap-0032-5`, dynamic widths (`max="10500"` ⇒ 5-digit padding),
  and the boundary at `max="9999"` vs `max="10000"`.
- **Scraper unit tests**: checked-in HTML fixtures
  (`chapter-with-comments.html` for page 1, `page2-fragment.html` for
  the AJAX response). Verify field extraction, emote stripping, the
  page-2 empty-response branch, and NFC normalisation of body text.
- **Fetcher POST tests**: extend the existing fetcher test suite to
  cover the new `Post` method — retries on 5xx, 403 surfaces as
  Cloudflare-expiry, cookies and UA are sent.
- **Renderer de-risk test** (gate): the
  `TestRenderFixture_ShapingAndEmoji_GoldenPNG` test described in
  "De-risk gate" above. Must pass before any of the sync code is
  written.
- **Renderer unit tests**: long-body wrapping, very long usernames,
  zero comments (must not be called), the like-count formatting.
- **Folder-sync end-to-end**: a fake `Fetcher` returning the fixture
  HTML exercises `comments.SyncFolder` and asserts `.comments.done`
  and `zzz-comments.png` land in the right place.
- **Archive-sync end-to-end** (using stdlib `archive/zip` to build
  and inspect a fixture archive, no shell-out):
  - Build a small `sample.cbz` containing two `chap-NNNN/` folders
    of fake JPEGs.
  - Run `comments.SyncArchive` with a fake fetcher.
  - Assert: (a) the rewritten archive contains the new
    `chap-NNNN[-K]/zzz-comments.png` entries; (b) every original
    entry is byte-for-byte identical (compare raw stored bytes,
    not just names); (c) re-running with the same input is a no-op
    (no tmp left behind, original mtime unchanged); (d) a fractional
    chapter folder `chap-0032-5/` is matched correctly to its URL.
  - Crash-safety test: inject a `panic()` after the tmp is written
    but before the rename; assert original is untouched and tmp is
    cleaned up on next run.

## Open / deferred decisions

- **Twemoji bundle size.** Full set is ~4 MB embedded. Acceptable per
  the project's single-binary philosophy. Could be trimmed to the
  most-used ~1000 emoji later if size becomes a concern.
- **Hi-DPI rendering.** Output is 1× for now. If readability is poor
  on hi-DPI displays, bump to 2× — trivial change later.
- **No-comments PNG.** Currently we render nothing for chapters with
  zero comments. Open to changing this if you'd prefer a tiny "No
  comments" marker page for consistency.
- **Headless-Chrome fallback for rendering.** Gated behind the
  de-risk test (see "De-risk gate"). If the pure-Go shaping path
  cannot pass the fixture test within ~one day, swap the renderer
  implementation to `chrome --headless --screenshot` against an
  HTML template. Public API of `renderer.go` (input: `[]Comment`,
  output: PNG bytes) stays the same — only the implementation
  changes — so the rest of the design is unaffected.
