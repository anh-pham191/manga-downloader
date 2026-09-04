# Registry + `update-all` design

Date: 2026-09-05. Status: approved in chat by Anh Pham.

## Goal

Remember where every archived manga came from, so a single command
(`update-all`) pulls the newest chapters for every manga, and a source
domain change can be applied to every stored URL at once.

## Non-goals

- No scheduler/daemon. `update-all` is run by hand (or via MCP).
- No support for a second source site; the registry stores full URLs
  and assumes the single existing `site/source` adapter.
- No backfill of comments on old chapters (`update-all` uses `resume`).

## 1. Registry

File: `<root>/.registry.json` (root defaults to `~/Documents/Manga`).

```json
{
  "version": 1,
  "manga": {
    "Witch Watch": {
      "url": "https://truyenqqko.com/truyen-tranh/ke-bao-ve-phu-thuy-la-mot-orge-10954",
      "added": "2026-09-05T09:47:00+12:00",
      "last_synced": "2026-09-05T10:20:00+12:00"
    }
  }
}
```

- Key is the archive name (the `.cbz` basename without suffix).
- Package: `internal/registry` with `Load(root)`, `Save`, `Upsert(name, url)`,
  `Touch(name)` (sets `last_synced`), `RewriteHost(newHost)`.
- Writes are atomic (temp file + rename) and hold the same file lock
  pattern as the pipeline (`.registry.json.lock`) so concurrent runs
  don't clobber each other.
- **Auto-record**: after `pipeline.Run` returns nil for `sync-manga` or
  `resume`, `main.go` (and the MCP sync tools) upsert the entry and set
  `last_synced`. A run that fails does not touch the registry, except
  that a first-time `sync-manga` records the URL before running so a
  partially downloaded manga can still be resumed via `update-all`.
- CLI: `downloader register <name> <url>` adds or fixes an entry;
  `downloader list` prints name, url, last_synced.
- MCP: `list_manga` output gains `url` and `last_synced` fields.

## 2. Backfill: `discover`

`downloader discover [--out root]`:

1. For every `*.cbz` in root without a registry entry, call the new
   adapter method `Site.Search(ctx, query) ([]site.SearchHit, error)`
   where `SearchHit{Title, URL}`. The selector for the site's search
   results page lives in `internal/site/source/search.go` with a
   fixture-based test, mirroring `ParseChapters`.
2. Print one block per archive: the archive name, then up to 3
   candidate hits numbered. Nothing is written automatically.
3. The operator confirms in chat; confirmed pairs are written with
   `downloader register`. Titles with no hits are listed at the end so
   a URL can be pasted.

`discover` never modifies the registry itself; it only reports.

## 3. `update-all`

`downloader update-all [--out root] [--cookies path] [--domain host] [--concurrency n]`

1. Load the registry. Empty registry → print hint to run `discover`, exit 0.
2. **Preflight** with the first entry, in this order:
   - `fetcher.HealUserAgent` as today.
   - `Site.ListChapters(url)`. Classify any error:
     - `fetcher.ErrCloudflareExpired` (403 / challenge page) → print
       existing refresh instructions, exit 1. **Never** ask for a domain.
     - `net.DNSError`, connection refused/reset, TLS handshake error,
       or a final redirect to a different host → **domain moved**.
       Print the evidence (error text, old host). If `--domain` was
       given, use it; otherwise prompt on stdin for the new host.
       Validate by re-running `ListChapters` against the rewritten
       URL; on success `RewriteHost` across the whole registry and
       save. On failure print the new error and exit 1.
     - HTTP 200 with zero chapters → the title page changed or was
       removed; record as failed for this manga and continue preflight
       with the next entry (up to 3 entries) before concluding.
     - Anything else → print and exit 1.
3. **Loop** sequentially over entries (registry order = name-sorted).
   For each: `pipeline.Run` with `Mode: Resume`, `From/To: 0`. Capture
   the count of new chapters (pipeline returns a `Result{New int}` —
   a small additive change to `Run`'s signature via a new `RunResult`
   function; existing `Run` stays as a wrapper). On success `Touch`.
   - `ErrCloudflareExpired` mid-loop → stop, print summary so far plus
     refresh instructions, exit 1.
   - `ErrNoArchive` (registry entry but `.cbz` deleted) → log, skip.
   - `ErrAnotherInstance` → log, skip.
   - Other error → log, continue.
4. **Summary** table to stdout: `name | new chapters | status`, then a
   line with totals. Exit code 0 if all ok, 1 if any failed.

Host-rewrite rule: only the scheme+host of each URL is replaced; path
and query are untouched.

## Error classification helper

`internal/fetcher/classify.go`: `Classify(err) Kind` returning
`KindCloudflare`, `KindHostUnreachable`, `KindOther`. Unit-tested with
constructed `*net.DNSError`, `*net.OpError`, `x509` errors, and a
wrapped `ErrCloudflareExpired`.

## Testing

- `registry`: round-trip, upsert, touch, rewrite-host, atomic save,
  corrupt file → clear error.
- `source.ParseSearch`: fixture from the live search page.
- `update-all`: pipeline-level test with fake site + fake fetcher covering
  the three failure classes and the summary output.
- `main.go` stays thin; command wiring for the new verbs mirrors
  `parseMode`.

## Files touched

- new `internal/registry/{registry.go,registry_test.go}`
- new `internal/fetcher/{classify.go,classify_test.go}`
- new `internal/site/source/{search.go,search_test.go}` + fixture; `site.Site`
  gains `Search`
- new `internal/pipeline/{updateall.go,updateall_test.go}`
- `main.go`: verbs `register`, `list`, `discover`, `update-all`; auto-record
- `internal/mcp`: `list_manga` fields, `update_all` tool
- `README.md`, `AGENTS.md`: document registry and new verbs
