# Comments: 5 pages + `--refresh-comments` recrawl

**Date:** 2026-06-02
**Status:** Approved (pending user review of this spec)

## Requirement

Scrape up to 5 pages of reader comments per chapter (was 2), then recrawl
the existing library so already-downloaded chapters pick up the extra pages.
New chapters downloaded after this change must also get 5 pages
automatically.

## Summary

The page-count change lives in the shared scraper, so **every** scrape path
(new-chapter download and comment-only render) gets 5 pages for free. A new
CLI-only `--refresh-comments` flag is added purely as a **one-time migration
tool** to re-scrape and cleanly *replace* comments on chapters already in the
archive — without re-downloading images. Once the back catalogue has been
refreshed once, the flag is never needed again.

## How the responsibilities split

| Scenario | Handled by | Flag needed? |
|---|---|---|
| Brand-new manga (first `sync-manga`) | scraper change (#1) | No |
| New chapters appended to existing manga | scraper change (#1) | No |
| Already-archived chapters (old 2-page back catalogue) | `--refresh-comments` (#2–#4) | Yes |

This works because `executeTask` (`internal/pipeline/pipeline.go:194-217`)
calls `comments.Scrape` **unconditionally for every task** — the
`if t.Kind == Both` guard at `:194` only gates image download, not the scrape
at `:201`. So a new chapter (`Kind: Both`) and a refresh (`Kind: Render`)
both funnel through the same 5-page scraper.

## Changes

### 1. Scraper — `internal/comments/scraper.go`

Replace the single hardcoded page-2 POST (current `:34-54`) with a loop over
pages `2..maxCommentPages`:

- Add package const `maxCommentPages = 5`.
- For each page `p` in `2..maxCommentPages`: POST to
  `/frontend/comment/list` with `"page": {strconv.Itoa(p)}`, parse the
  fragment, append the parsed comments.
- **Early-stop:** if a page yields **zero** parent comments
  (`parseComments` returns only `comment-main-level` parents), `break` —
  no point POSTing further pages.
- Page-1 handling (server-rendered, `:32`) and the existing
  error-swallowing semantics are unchanged: a failed or empty POST stops the
  loop, and page 1 still returns.
- Update the stale doc comment at `:16-19` ("page 2 … at most ~12 comments")
  to describe the 5-page loop.

Note: `parseComments` does no dedup (`:96` is an unconditional append).
Correctness past the last real page relies on the server returning an empty
page, which the early-stop handles.

### 2. Planner — `internal/pipeline/plan.go`

`Plan` gains a `refresh bool` parameter. **Only** the `SyncComments` branch
(`:41-44`) changes:

```go
case SyncComments:
    if in && (refresh || !hasComments) {   // refresh re-renders even when comments exist
        out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Render})
    }
```

The `Resume` and `SyncManga` branches are **untouched** — refresh applies to
`sync-comments` mode only (see decision D3).

### 3. Pipeline + CLI — `internal/pipeline/pipeline.go`, `main.go`

- Add `RefreshComments bool` to `pipeline.Opts` (`pipeline.go:24-35`).
- Thread it into **all three** `Plan()` call sites:
  - `:87` — `existing` (split-width path)
  - `:94` — `novel` (split-width path)
  - `:102` — normal path
  Missing any one silently drops refresh on that path.
- `main.go`: add `refreshComments := fs.Bool("refresh-comments", false, …)`
  to the flag block (`:40-48`), pass into `Opts`, and update `usage()`
  (`:130-145`) to document that the flag applies to `sync-comments` only.
- `main.go`: if `*refreshComments` is true and `mode != SyncComments`, print
  a stderr warning that the flag is ignored in this mode (per D3), before
  calling `pipeline.Run`.

### 4. Staging — replace, not duplicate — `internal/archive/writer.go`

**Bug being fixed:** `stageViaGoZip` (`:62-158`) raw-copies **every**
existing archive entry (`:73-91`) and then appends scratch entries
(`:104-136`). A re-rendered `zzz-comments.png` would therefore produce a
**duplicate** entry of the same name. `verifyArchive` (`:259-275`) does not
detect duplicate names, so it would not catch this.

**Fix:** before the copy loop, build a set of the full entry names the
`.ok`-marked scratch chapters will write — keyed exactly as the writer names
them at `:124`, i.e. `ch + "/" + e.Name()`, over the same chapter set
`readMarkedChapters` returns at `:98`. During the raw-copy loop, skip copying
any existing entry whose name is in that set (the scratch version supersedes
it).

`stageViaOSZip` (`:165-216`) already replaces same-named entries: the
Info-ZIP `zip -X -0` invocation (`:198`) freshens an existing entry rather
than duplicating it. No change needed there; both paths converge on replace
semantics.

**Known limitation (by design):** if a refresh scrape returns **zero**
comments, `executeTask` writes no `zzz-comments.png` but still writes `.ok`
(`pipeline.go:205, :220`). The skip-set is built from names the scratch dir
actually writes, so an empty scratch dir writes nothing and the **old**
`zzz-comments.png` is copied through and survives. Refresh therefore
*replaces* when the re-scrape yields ≥1 comment and *leaves intact*
otherwise — it never deletes comments. This matches the intent.

## Testing

- **Scraper** (`internal/comments/scraper_test.go`): the existing
  `TestScrape_PullsPage2` uses a `fixedFetcher` whose `Post` **ignores the
  `page` form value** and returns the same body every call — under a 2→5 loop
  it would parse page 2 four times and never exercise early-stop. Add a
  **page-aware** fake fetcher returning distinct bodies per page plus one
  empty page, asserting (a) comments from >2 distinct pages merge in order,
  and (b) early-stop fires on the empty page (no further POSTs, no phantom
  comments).
- **Planner** (`plan_test.go`): add a `refresh=true` case asserting an
  already-commented archived chapter (`in && hasComments`) is planned as a
  `Render` task; and a `refresh=false` case asserting it is **not**.
- **Writer** (`archive` tests): stage a scratch chapter containing a new
  `zzz-comments.png` over an archive that already has one for that chapter;
  assert the resulting archive has **exactly one** entry of that name
  (no duplicate) and it is the scratch version.

## Decisions

- **D1 — Refresh trigger:** `--refresh-comments` flag (not a new mode, not
  delete-then-rebuild).
- **D2 — Page depth:** hardcoded `5`, with early-stop on the first empty
  page (no configurable flag).
- **D3 — Refresh scope:** `sync-comments` mode **only**. If
  `--refresh-comments` is passed with `resume` or `sync-manga`, print a
  stderr warning that it is ignored (the flag is parsed by the shared
  `FlagSet` for all modes but only affects `sync-comments`).
- **D4 — MCP:** out of scope. The MCP `sync_comments` tool flows through
  `SyncExecutor.Run` → `pipeline.Run` with `RefreshComments` defaulting to
  `false`, so it stays backfill-only. CLI-only for now.

## Out of scope

- MCP exposure of refresh.
- Configurable page count.
- Refresh behavior in `resume` / `sync-manga` (warned-and-ignored per D3).
