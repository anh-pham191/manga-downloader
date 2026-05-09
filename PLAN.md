# Manga Downloader — Implementation Plan

A personal macOS CLI, written in Go, that mirrors a manga from a
configured source site (and, by virtue of its design, future sites)
to local disk for offline reading.

The plan is written so that an implementer (human or agent) can pick it
up and execute it without further clarification, in line with
[`development_rule/knowledge/process/definition-of-ready.md`](../development_rule/knowledge/process/definition-of-ready.md).

---

## 1. Purpose

Given one manga URL on the source site, list every chapter the
source page advertises and download every image of every chapter to
a local folder, so the manga can be read offline in Finder / Preview.

## 2. Scope

**In scope (v1):**

- Single source site (Cloudflare-protected).
- One manga URL per invocation.
- Range, concurrency, output-dir, and resume flags.
- Headless-Chrome (`chromedp`) bypass for the Cloudflare challenge.
- Idempotent re-runs via `.done` sentinel files.

**Out of scope (v1):**

- Other sites (the design admits them; only one adapter ships).
- A GUI.
- CBZ packaging or `ComicInfo.xml` metadata.
- Cross-manga parallelism.
- Auto-update of the chapter list on re-run (the user re-invokes when
  new chapters are released).
- A persistent daemon, watch mode, or scheduling.

## 3. Acceptance Criteria

A v1 release is accepted when **all** of the following hold:

1. `downloader <manga-url>` exits `0` and produces *N* chapter folders
   matching the chapters listed on the source page at run-time.
2. Each chapter folder contains every image referenced on its chapter
   page, in source order, named `001.<ext>`, `002.<ext>`, …, where
   `<ext>` matches the URL extension (`jpg`, `png`, `webp`, …).
3. A `.done` sentinel file exists in each chapter folder where every
   image downloaded successfully, and **does not exist** where the
   chapter is incomplete.
4. Re-running with `--resume` skips chapters whose `.done` is present
   and downloads only the rest.
5. Transient network errors (5xx, 429, dial errors) are retried with
   exponential backoff (3 attempts); on final failure the binary exits
   non-zero with a per-chapter summary on stderr.
6. The parser package has table-driven tests over checked-in HTML
   fixtures; the downloader package has tests using a fake `Fetcher`.
7. `go vet ./...` and `go test ./...` pass; `gofmt -l ./...` produces
   no output.
8. README explains build, run, and how to refresh the Cloudflare path
   if `chromedp` ever fails.

## 4. Hazards & Mitigations

Per DoR §7 (Hazard Assessment). Each hazard names a concrete mitigation
that is reflected in the design or acceptance criteria.

| # | Hazard | Mitigation |
|---|---|---|
| H1 | Cloudflare rotates the challenge → chromedp flow breaks | CF logic isolated in `internal/fetcher`; clear error surfaces to the user; manual-cookie path documented in README as fallback. |
| H2 | Source-site DOM changes (selectors silently break) | Selectors live only in `internal/site/source`; parser tests use fixtures so a DOM change shows up as a red test, not an empty download. |
| H3 | Hotlink protection on images (403 without `Referer`) | Fetcher always sends `Referer: <chapter-url>` for image requests; covered by a downloader test. |
| H4 | Rate limiting / IP ban | Default chapter concurrency = 4; images within a chapter are sequential; 200–500 ms jitter between requests; exponential backoff on 429/5xx. |
| H5 | Partial download corrupts `--resume` | Each image is written to `NNN.ext.part` then renamed only on success. `.done` is written **only** after every image in the chapter is verified on disk. |
| H6 | Disk fills mid-run | Pre-flight check: warn if free space on `--out` filesystem is < 1 GiB; abort if < 100 MiB. |
| H7 | Image extension mismatch (`.jpg` URL serving WebP) | Use the URL extension. Sniff `Content-Type` only as a fallback for extensionless URLs. |
| H8 | Selector returns zero images on a real chapter (silent zero) | Fail the chapter loudly if `len(images) == 0`; never write `.done`. |
| H9 | Too-aggressive scraping breaches site etiquette | Polite defaults (above) plus a `--polite` mode that drops concurrency to 1; tool is for personal use only. |

## 5. Architecture

```
downloader/
├── go.mod
├── main.go                    // flag parsing, wiring, exit codes
├── PLAN.md                    // this file
├── README.md
└── internal/
    ├── fetcher/               // HTTP client, CF bypass, retries, jitter
    │   ├── fetcher.go         // Fetcher interface + net/http impl
    │   ├── chrome.go          // chromedp bootstrap (cookie + UA)
    │   └── fetcher_test.go
    ├── parser/                // HTML → structured data (site-agnostic helpers)
    │   ├── parser.go
    │   └── parser_test.go
    ├── site/
    │   └── source/          // selectors + URL conventions for one site
    │       ├── site.go        // implements site.Site
    │       ├── site_test.go
    │       └── testdata/
    │           ├── manga.html
    │           └── chapter.html
    └── downloader/            // orchestration: list → fetch → save
        ├── downloader.go
        └── downloader_test.go
```

### Key interfaces

```go
// internal/site/site.go
type Site interface {
    ListChapters(ctx context.Context, mangaURL string) ([]Chapter, error)
    ChapterImages(ctx context.Context, chapter Chapter) ([]ImageRef, error)
}

type Chapter struct {
    Number string // "1", "227", "227.5" — as published by the source
    Title  string // optional, may be empty
    URL    string
}

type ImageRef struct {
    URL     string
    Referer string
}

// internal/fetcher/fetcher.go
type Fetcher interface {
    Get(ctx context.Context, req Request) (*Response, error)
}
```

`Fetcher` is the seam that lets the downloader and parser tests run
without ever touching the network. The `chromedp` complexity sits
behind it.

### Output layout

```
<out>/<manga-slug>/
├── chap-001/
│   ├── 001.jpg
│   ├── 002.jpg
│   └── .done
├── chap-002/
│   └── ...
└── chap-410/
    └── ...
```

`<manga-slug>` is the last URL path segment of the manga URL.
Chapter folder names use `chap-<zero-padded number>`; padding width is
`max(3, len(largest chapter number))`.

### CLI

```
downloader <manga-url> \
  [--out ./downloads] \
  [--concurrency 4] \
  [--from N] [--to M] \
  [--resume] \
  [--polite] \
  [--verbose]
```

Exit codes: `0` success, `1` partial failure (some chapters not
`.done`), `2` setup error (CF bypass failed, bad URL, bad flags).

## 6. Milestones

Milestones are sequenced and tracked as tasks (TaskCreate). Each
milestone has a clear "done when" so we can demo it in isolation.

### M0 — Scaffold (≤ 30 min)
- `go mod init github.com/anhpham/downloader` (or local module).
- Create directory layout above with empty files.
- `.gitignore` (`/downloads/`, `*.part`, `bin/`).
- README stub with build instructions.
- **Done when:** `go build ./...` and `go test ./...` both succeed
  (zero tests is fine).

### M1 — Parser + fixtures, TDD (½ day)
- Capture real HTML once via curl-with-CF-cookie or chromedp dump:
  - `internal/site/source/testdata/manga.html` — sample manga page.
  - `internal/site/source/testdata/chapter.html` — chapter 1.
- Write `site_test.go`: table-driven tests for
  `ListChapters` (expect 410 entries, ascending order, well-formed
  URLs) and `ChapterImages` (expect N images, all absolute URLs, all
  carrying the chapter URL as their referer).
- Implement `site.go` with `goquery` until tests are green.
- **Done when:** `go test ./internal/site/source/...` is green and
  `go vet` is clean.

### M2 — Downloader orchestration with fake Fetcher (½ day)
- Define `Fetcher` interface + `Request`/`Response` types.
- Implement `internal/downloader`:
  - `Run(ctx, plan)` orchestrates chapters with bounded concurrency.
  - Per-image: `*.part` write → `os.Rename` on success.
  - Per-chapter: write `.done` only after every image rename succeeds.
  - Retries: 3× exponential backoff on 429/5xx/dial errors.
  - Jitter: 200–500 ms between requests within a chapter.
- Tests use an in-memory `fakeFetcher` covering:
  - Happy path (all images succeed).
  - One image fails twice then succeeds (retry).
  - One image fails permanently (no `.done`, non-zero result).
  - `--resume`: existing `.done` skips the chapter entirely.
- **Done when:** `go test ./internal/downloader/...` is green.

### M3 — chromedp Cloudflare fetcher (½–1 day)
- `internal/fetcher/chrome.go`:
  - Launch headless Chrome via `chromedp`.
  - Navigate to manga URL.
  - Wait until `cf_clearance` cookie appears (or timeout = 30 s).
  - Extract cookie + `navigator.userAgent`.
  - Close Chrome.
- `internal/fetcher/fetcher.go`:
  - `net/http` client with cookie jar and the captured UA.
  - Implements `Fetcher`.
  - Retries + jitter live here so the downloader stays simple.
- Manual integration check (not unit-tested): hit one chapter image,
  expect 200 + image bytes.
- **Done when:** a small `cmd/internal/cffprobe` (or a `_test.go` with
  build tag `integration`) downloads one image successfully.

### M4 — CLI wiring (¼ day)
- `main.go`: parse flags, build site adapter, build fetcher, build
  downloader, run, exit with the right code.
- Stderr progress: one line per chapter start/finish, plus a final
  summary (`409/410 chapters complete, 1 failed: chap-273`).
- **Done when:** `downloader --help` prints flags and a fresh run on a
  small manga produces the expected layout.

### M5 — End-to-end smoke (≤ 1 hr active, hours wall-clock)
- Run `downloader <hxh-url> --to 3` → expect 3 chapter folders, each
  with `.done`, images viewable in Preview.
- Run `downloader <hxh-url> --resume` → expect M3+ to complete the
  rest; the first 3 are skipped instantly.
- **Done when:** all 410 chapters are on disk with `.done`, and the
  binary exits 0.

## 7. TDD Order (the red-green spine)

1. `site_test.go` red → `site.go` green. (M1)
2. `downloader_test.go` red → `downloader.go` green using fake. (M2)
3. `chrome.go` written by hand against the live site (no unit test;
   integration only). (M3)
4. `main.go` is glue; smoke test in M5 is its acceptance test.

## 8. Operating Rules (per `agent-safety`)

- **No git commits** without explicit human approval, even when work
  is "done".
- **No edits to tests** once approved, except to add cases.
- **Pause before destructive ops** — e.g. wiping a partially populated
  `downloads/` tree — show the plan and wait for "yes".
- **Stay inside `/Users/anhpham/Documents/Projects/script/downloader`**
  unless explicitly told otherwise.

## 9. Open Questions

None blocking. The following are deferred to v2 and noted here so
they're not forgotten:

- Multi-site support (a second `Site` adapter and a host-→-adapter
  registry in `main.go`).
- CBZ output (`archive/zip` after `.done`).
- `ComicInfo.xml` for comic readers.
- A `--watch` mode that polls for new chapters on a schedule.
