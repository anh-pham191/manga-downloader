# AGENTS.md

Context for AI agents (Claude, Copilot, Cursor, etc.) and human
contributors picking up this project. Keep it short; surface only
what is non-obvious from reading the code.

## What this is

A single-binary Go CLI that mirrors a manga from a Cloudflare-
protected source site to local disk. See [`README.md`](./README.md)
for end-user docs and [`PLAN.md`](./PLAN.md) for the design +
acceptance criteria the v1 implementation was built against.

## What's already settled (don't relitigate)

- **No headless browser.** We tried `chromedp` (both headless and
  visible). Cloudflare's Turnstile detects automation flags and
  loops the "verify you are human" checkbox forever. The project
  uses **manually pasted `cf_clearance` cookies + the matching
  User-Agent** instead. This is in `internal/fetcher/cookies.go`.
  Don't re-add chromedp; it doesn't work.
- **Cookie + UA must match.** Cloudflare binds `cf_clearance` to
  the exact User-Agent that solved the challenge. A mismatched UA
  causes a 403 immediately. The cookie file format requires both.
- **`.done` sentinel** is the single source of truth for "is this
  chapter complete?" — written only after every image in the
  chapter has been atomically renamed into place. `--resume` looks
  for this file, never at image counts.
- **403 = Cloudflare expiry.** The fetcher surfaces it as
  `fetcher.ErrCloudflareExpired`. Don't retry it; surface the
  refresh instructions.

## Architecture seams

- **`site.Site`** (`internal/site/site.go`) — one method per page
  type (`ListChapters`, `ChapterImages`). Selectors live behind it
  so a DOM change is isolated to one package.
- **`fetcher.Fetcher`** — `Get` and `Post` (the latter drives comment
  pagination, see below). Real impl uses `net/http` with a cookie jar;
  tests inject a fake. Retries, jitter, and the cf-expiry detection
  live in the real impl, not in callers.
- **`downloader.Downloader`** — pure orchestration. Knows nothing
  about HTTP or selectors. Tested with `fakeSite` + `fakeFetcher`
  in `downloader_test.go`.

If you're adding a new source site, your work is almost entirely
inside `internal/site/<sitename>/`. Don't reach into `downloader`
or `fetcher`.

## Comments

Each chapter's reader comments are scraped and rendered into a single
`zzz-comments.png` page inside the chapter folder (the `zzz-` prefix
sorts it last in the `.cbz`). Logic lives in `internal/comments/`.

- **Only top-level (parent) comments are scraped.** `Scrape` posts
  `parent_id=0` and the parser matches `comment-main-level` articles
  only. Nested replies — the "N phản hồi" expanders loaded in the
  browser via `loadReply(<id>)` — are **deliberately not fetched** and
  are not in the archive. Don't assume replies are covered.
- **Up to 5 pages per chapter.** Page 1 is server-rendered in the
  chapter HTML; pages 2..`maxCommentPages` (=5) are POSTed to
  `/frontend/comment/list`. The loop stops early at the first page
  that yields zero comments (`internal/comments/scraper.go`). There is
  no cross-page dedup — correctness past the last real page relies on
  the server returning an empty page.
- **`--refresh-comments`** (CLI flag, `sync-comments` mode only)
  re-scrapes and **replaces** comments on chapters that *already* have
  a comments page. Without it, `sync-comments` only backfills chapters
  that are *missing* comments. The flag is warned-and-ignored in
  `resume`/`sync-manga`, and is intentionally not exposed over MCP.
  New chapters pulled by `sync-manga`/`resume` already get 5 pages with
  no flag, because `executeTask` calls `comments.Scrape` for every
  task regardless of kind.
- **Staging replaces, never duplicates.** `archive.stageViaGoZip`
  skips copying any existing archive entry a scratch chapter is about
  to write (keyed `chap-XXXX/<file>`), so a re-rendered
  `zzz-comments.png` overwrites the old one instead of producing a
  duplicate zip entry. The large-archive `stageViaOSZip` path relies
  on `zip` freshening same-named entries for the same effect. A refresh
  that scrapes zero comments writes no file and leaves the old comments
  intact — it never deletes.

## Testing conventions

- **TDD**: parser tests are written first against checked-in HTML
  fixtures, then the parser is implemented until green.
- **Fixtures are synthetic.** They mirror the DOM contract of the
  real site (`.works-chapter-list`, `.page-chapter > img`,
  lazy-load via `data-original`). If you change selectors, update
  the fixture in the same commit.
- **Never modify a test to match a regression.** Tests are the
  spec. If a test fails, fix the implementation or update the test
  *and* the fixture *and* explain why.
- `go test ./... && go vet ./... && gofmt -l .` must be clean
  before any commit.

## Code style

- **Idiomatic Go.** Small interfaces, `errors.Is/As`, `%w`-wrapped
  errors, table-driven tests. No factories, no DI containers.
- **Comments explain *why*, not *what*.** The "why" tends to be a
  Cloudflare or filesystem hazard — see the comments in
  `internal/fetcher/http.go` and `internal/downloader/downloader.go`
  for the pattern.
- **Hazards are documented inline.** When you mitigate a real-world
  failure mode (hot-link `Referer`, partial download corruption,
  zero-image silent success), reference the hazard ID from
  [`PLAN.md`](./PLAN.md) §4 in the comment.

## Operating rules for agents

- **No git commits without explicit human approval.** Even if all
  tests pass.
- **No destructive ops without explicit approval.** Wiping
  `downloads/` or `~/Documents/Manga/<title>/` requires a "yes,
  delete" from the user.
- **Don't run the full download against a real site as a test.**
  Use `--to 3` or smaller for smoke tests.
- **Don't paste secrets into the repo.** The cookie file lives at
  `~/Library/Application Support/downloader/cookies.json`. It must
  never be committed. `.gitignore` is configured but check before
  staging.
- **Public-facing materials must avoid naming the specific source
  site or specific manga titles.** The repo is public. Use generic
  placeholders ("the source site", "<manga-url>", etc.) in README,
  AGENTS, PLAN, code comments, and commit messages. If you need to
  test, use `example.com` and "Sample Manga" in fixtures.

## Post-download packaging

After every download (including resumes that pick up new chapters),
the standard step is `./package-cbz.sh` to bundle each manga folder
into a `.cbz` next to it. **Do this automatically when the user
finishes adding a manga unless they say otherwise.**

For ongoing mangas that get new chapters later, the same
`package-cbz.sh` run handles the append: `zip -u` adds only the new
chapter folders to the existing `.cbz` without rebuilding the
archive. Do **not** unzip + rezip — it's wasteful and the user
already considered it.

The script lives in this repo at `package-cbz.sh`.

## Domain rebrands

The source site has been observed to rebrand (changing TLD or full
domain) periodically. Symptoms look identical to a Cloudflare
expiry — every request 403s — but cookie refreshes don't help. To
diagnose: `curl -sIL https://<old-domain>/` and watch for a
redirect to a new host. Fix: update the URL passed to the binary,
change `domain` in `cookies.json`, and `sed`-rewrite the URLs in
every `<manga-root>/.chapters.json` to the new host.

## Known limitations

- One source site (the one whose selectors live in
  `internal/site/source/`).
- Reader **replies are not scraped** — only top-level comments, up to
  5 pages (see [Comments](#comments)).
- Cookie expires mid-run on very long mangas; the user has to
  refresh and re-invoke with `--resume`. There is no automated
  refresh path because re-solving Turnstile requires a human.
- No CBZ packaging *during* download (use `package-cbz.sh`
  afterwards). No `ComicInfo.xml`. No watch mode.
