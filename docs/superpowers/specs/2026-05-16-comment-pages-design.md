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
2. Lists the existing `<name>.cbz` with `zip -sf` (or equivalent Go
   `archive/zip` reader) to learn which `chap-NNNN/` directories the
   archive contains.
3. For each chapter directory in the archive that does **not**
   already contain a `chap-NNNN/zzz-comments.png` entry:
   - Scrapes comments for that chapter (page 1 + page 2).
   - If ≥1 comment, renders a PNG to a scratch dir.
   - Calls `zip -u <name>.cbz chap-NNNN/zzz-comments.png` from that
     scratch dir to inject the entry into the archive.
4. The presence of `chap-NNNN/zzz-comments.png` inside the archive
   IS the sentinel — no separate `.comments.done` file is needed
   here (and can't be, since the archive isn't tracked by per-file
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

A new `internal/comments/` package with four responsibilities:

- **`scraper.go`** — given a chapter URL + a `fetcher.Fetcher`,
  returns `[]Comment` for the first two pages. Reuses the existing
  fetcher (cookies, retries, jitter, Cloudflare 403 handling).
- **`renderer.go`** — given `[]Comment` and an output path, writes a
  PNG. Pure Go; no shell-out.
- **`folder_sync.go`** — scenario A: scrape → render → write into
  `chap-NNNN/zzz-comments.png` + `.comments.done` sentinel.
- **`archive_sync.go`** — scenario B: list existing `.cbz`,
  re-fetch chapter list, scrape + render per missing chapter, inject
  into the archive via `zip -u`. Uses Go's `archive/zip` reader to
  list entries; shells out to `zip -u` for the in-place append (the
  same tool `package-cbz.sh` already uses).
- **`assets/`** — embedded font (Noto Sans regular + bold) and
  embedded Twemoji 72×72 PNG set, both via `//go:embed`. Keeps the
  binary self-contained per the README.

`internal/downloader/` gains one hook in its per-chapter routine,
called after `.done` is written (this is the scenario-A entry point).

`main.go` gains the `--comments-only` flag dispatch that calls
`comments.SyncArchive` instead of running the normal download flow.

```
internal/
├── downloader/
│   └── … (existing) + new call into comments.SyncFolder per chapter
├── comments/
│   ├── scraper.go
│   ├── scraper_test.go
│   ├── renderer.go
│   ├── renderer_test.go
│   ├── folder_sync.go        // scenario A: filesystem + sentinel
│   ├── folder_sync_test.go
│   ├── archive_sync.go       // scenario B: inject into existing CBZ
│   ├── archive_sync_test.go
│   ├── testdata/
│   │   ├── chapter-with-comments.html
│   │   ├── page2-fragment.html
│   │   ├── sample.cbz        // small fixture for archive tests
│   │   └── golden-render.png
│   └── assets/
│       ├── NotoSans-Regular.ttf
│       ├── NotoSans-Bold.ttf
│       └── twemoji/<codepoint>.png  // bundled set
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
existing := archive.ListChapterDirs(<name>.cbz)             // chap-XXXX entries
done := archive.EntriesEndingWith(<name>.cbz, "/zzz-comments.png")

for each chapter in existing:
    if chapter in done: skip
    url := chapters[chapter]  // map chapter number → URL
    if url not found:
        log "no URL match for chap-NNNN, skipping"; continue
    comments, err := comments.Scrape(ctx, url, fetcher)
    if err is cloudflareExpiry: surface and exit 1
    if err is transient: log, continue (will retry next run)
    if len(comments) == 0: continue
    render to scratch dir at chap-NNNN/zzz-comments.png
    exec: zip -u <name>.cbz chap-NNNN/zzz-comments.png  (cwd = scratch)
```

The "no URL match" case can happen if the manga's chapter URLs have
been renumbered or partial chapters (chap-32-5) are now indexed
differently. Logged and skipped — never silently wrong.

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
- Fonts: Noto Sans regular + bold, embedded.
- Emoji: walk each text run codepoint by codepoint. If the codepoint
  (or codepoint sequence) is in the bundled Twemoji set, draw the
  matching PNG inline at line-height. Otherwise draw the glyph via
  freetype.

The emoji approach is the technically risky bit. To de-risk it, the
plan should include a small standalone test that renders a fixture
string with mixed Vietnamese + emoji to a golden PNG before wiring
the renderer into the downloader.

## Concurrency

Reuse the existing chapter worker pool. The comments step is one or
two HTTP requests + a CPU-bound render; it slots into the same
goroutine that already handles a chapter's images.

## Failure modes

| Failure | Handling |
|---|---|
| Cloudflare 403 on chapter page or AJAX endpoint | Surface "refresh cf_clearance + --resume" error (existing pattern), exit 1. |
| Page-2 endpoint returns 5xx after retries | Treat as page-1-only; write `.comments.done` (A) or inject what we have (B); continue. |
| Page-2 response empty / `<article>` count 0 | Same as above. |
| Render error (font load fail, image decode fail) | Fail the chapter — do NOT write `.comments.done` (A) / do NOT inject into archive (B). Next run retries. |
| Scrape parses zero comments on page 1 too | A: write `.comments.done`, no PNG. B: do nothing — no PNG injected, and on next run we'll re-scrape (cheap). |
| Scenario B: chapter exists in archive but not in re-fetched chapter list | Log "no URL match for chap-NNNN", skip. Never silently wrong. |
| Scenario B: `zip -u` exits non-zero | Surface as a chapter-level error, continue with remaining chapters, exit 1 at end. |

## CBZ packaging

`package-cbz.sh` uses `zip -u` already, so re-running it after
backfill appends new `zzz-comments.png` files to existing archives
in seconds. One change to the script: add `.comments.done` to the
exclude list (alongside `.done`, `.chapters.json`, `.DS_Store`) so
the sentinel doesn't leak into the archive.

## Testing

- **Scraper unit tests**: checked-in HTML fixtures
  (`chapter-with-comments.html` for page 1, `page2-fragment.html` for
  the AJAX response). Verify field extraction, emote stripping, and
  the page-2 empty-response branch.
- **Renderer unit tests**: render a small fixture comment list and
  diff against a golden PNG. A separate fixture covers mixed
  Vietnamese + emoji content to exercise the font/emoji fallback
  path.
- **Folder-sync end-to-end**: a fake `Fetcher` returning the fixture
  HTML exercises `comments.SyncFolder` and asserts `.comments.done`
  and `zzz-comments.png` land in the right place.
- **Archive-sync end-to-end**: build a small `sample.cbz` containing
  two `chap-NNNN/` folders, run `comments.SyncArchive` with a fake
  fetcher and a stub `zip` (or call real `zip` in a tempdir), and
  assert: (a) the resulting archive contains the new
  `chap-NNNN/zzz-comments.png` entries at the expected paths;
  (b) re-running is a no-op (skips entries already present in
  archive); (c) the original images in the archive are untouched.

## Open / deferred decisions

- **Twemoji bundle size.** Full set is ~4 MB embedded. Acceptable per
  the project's single-binary philosophy. Could be trimmed to the
  most-used ~1000 emoji later if size becomes a concern.
- **Hi-DPI rendering.** Output is 1× for now. If readability is poor
  on hi-DPI displays, bump to 2× — trivial change later.
- **No-comments PNG.** Currently we render nothing for chapters with
  zero comments. Open to changing this if you'd prefer a tiny "No
  comments" marker page for consistency.
