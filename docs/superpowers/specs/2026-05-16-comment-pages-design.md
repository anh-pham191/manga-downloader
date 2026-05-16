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

`./bin/downloader <url>` (with or without `--resume`) gains a new step
after a chapter's images are saved:

1. If `.comments.done` already exists in the chapter folder → skip.
2. Otherwise: scrape page 1 of comments (already in the chapter HTML)
   and page 2 (one extra POST). If ≥1 comment, render
   `zzz-comments.png`. Always write `.comments.done` afterward, even
   when there are zero comments.

`package-cbz.sh` is updated to also exclude `.comments.done` from
the archive. The `zzz-` prefix on the rendered PNG makes it sort
last alphabetically, so any comic reader places it at the end of
the chapter.

New flag: `--comments` (default `true`). `--comments=false` skips
the whole step.

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

A new `internal/comments/` package with three responsibilities:

- **`scraper.go`** — given a chapter URL + a `fetcher.Fetcher`,
  returns `[]Comment` for the first two pages. Reuses the existing
  fetcher (cookies, retries, jitter, Cloudflare 403 handling).
- **`renderer.go`** — given `[]Comment` and an output path, writes a
  PNG. Pure Go; no shell-out.
- **`assets/`** — embedded font (Noto Sans regular + bold) and
  embedded Twemoji 72×72 PNG set, both via `//go:embed`. Keeps the
  binary self-contained per the README.

`internal/downloader/` gains one hook in its per-chapter routine,
called after `.done` is written.

```
internal/
├── downloader/
│   └── … (existing) + new call into comments.Sync per chapter
├── comments/
│   ├── scraper.go
│   ├── scraper_test.go
│   ├── renderer.go
│   ├── renderer_test.go
│   ├── sync.go         // orchestrates scrape → render → sentinel
│   ├── testdata/
│   │   ├── chapter-with-comments.html
│   │   ├── page2-fragment.html
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

```
for each chapter the downloader processes:
    download images (existing)
    write .done (existing)
    if --comments && !.comments.done:
        comments, err := comments.Scrape(ctx, chapterURL, fetcher)
        if err is cloudflareExpiry: surface and exit 1 (existing pattern)
        if err is transient: log, leave .comments.done absent, continue
        if len(comments) > 0:
            render zzz-comments.png
        write .comments.done
```

Backfill is implicit: an old chapter with `.done` but no
`.comments.done` triggers the same branch on the next `--resume`. No
separate code path. Image download is skipped (existing `.done`
check).

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
| Page-2 endpoint returns 5xx after retries | Treat as page-1-only; write `.comments.done`; continue. |
| Page-2 response empty / `<article>` count 0 | Same as above. |
| Render error (font load fail, image decode fail) | Fail the chapter — do NOT write `.comments.done`. Next `--resume` retries. |
| Scrape parses zero comments on page 1 too | Write `.comments.done`, no PNG. |

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
- **End-to-end**: a fake `Fetcher` that returns the fixture HTML
  exercises `comments.Sync` and asserts that `.comments.done` and
  `zzz-comments.png` land in the right place.

## Open / deferred decisions

- **Twemoji bundle size.** Full set is ~4 MB embedded. Acceptable per
  the project's single-binary philosophy. Could be trimmed to the
  most-used ~1000 emoji later if size becomes a concern.
- **Hi-DPI rendering.** Output is 1× for now. If readability is poor
  on hi-DPI displays, bump to 2× — trivial change later.
- **No-comments PNG.** Currently we render nothing for chapters with
  zero comments. Open to changing this if you'd prefer a tiny "No
  comments" marker page for consistency.
