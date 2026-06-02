# downloader

A small Go CLI that mirrors a manga from a Cloudflare-protected
source site to local disk for offline reading. Each chapter becomes
a folder of zero-padded JPEGs, with a `.done` sentinel so re-runs
skip what's already on disk.

```text
~/Documents/Manga/
└── <Manga Name>/
    ├── chap-0001/
    │   ├── 001.jpg
    │   ├── 002.jpg
    │   └── .done
    ├── chap-0002/
    └── …
```

---

## Why it works the way it does

The source site sits behind Cloudflare's "Verify you are human"
challenge. Programmatic browsers can't reliably solve it — the
challenge fingerprints automation in subtle ways that strip every
chromedp/Selenium trick we tried.

Rather than fight that, the tool takes the obvious shortcut: **you
solve the challenge once, in your real browser**, copy the
`cf_clearance` cookie and your User-Agent into a JSON file, and the
downloader hands them to a normal `net/http` client for everything
that follows. The cookie typically lasts a few hours; when it
expires, you refresh it and re-run with `--resume`.

This is dramatically simpler than headless-browser approaches and is
the only path that survived contact with Cloudflare in practice.

---

## Install

Requirements:

- Go 1.21 or newer
- macOS, Linux, or Windows — only macOS is exercised; the code uses
  no platform-specific APIs.

```sh
git clone https://github.com/<your-username>/downloader.git
cd downloader
go build -o bin/downloader .
```

The output is a single ~10 MB static binary at `bin/downloader`.

---

## First-time setup: cookies

1. **Open the manga URL** in Chrome, Safari, or Firefox.
2. **Solve the Cloudflare checkbox** if it appears.
3. **Copy `cf_clearance`** — DevTools (Cmd+Opt+I on macOS) →
   *Application* tab → *Cookies* → the source site → click
   `cf_clearance` → copy the *Value*.
4. **Copy your User-Agent** — DevTools → *Console* → type
   `navigator.userAgent` → press Enter → copy the printed string.
5. **Save them as JSON** at:

   - macOS: `~/Library/Application Support/downloader/cookies.json`
   - Linux: `~/.config/downloader/cookies.json`
   - Windows: `%AppData%\downloader\cookies.json`

   ```json
   {
     "user_agent": "Mozilla/5.0 …(paste exactly)…",
     "cookies": [
       {
         "name": "cf_clearance",
         "value": "PASTE_VALUE_HERE",
         "domain": ".example.com"
       }
     ]
   }
   ```

   You can also pass an explicit path via `--cookies <path>`.

If the file is missing the binary prints these instructions and
exits with code 2.

---

## Usage

Three subcommands, all operating against `<--out>/<name>.cbz`:

```sh
bin/downloader sync-manga    [flags] <manga-url>   # new chapters + comments
                                                   # AND backfill missing comments
bin/downloader resume        [flags] <manga-url>   # new chapters + comments only
bin/downloader sync-comments [flags] <manga-url>   # backfill comments only (no new chapters)
```

The archive is the only persistent state — no per-chapter folders or
`.done` markers stay on disk. A scratch directory at
`<out>/.<name>.scratch/` is used during a run and deleted on clean
exit; partial work survives a kill via per-chapter `.ok` markers.

Concurrent runs against the same archive are prevented by a file
lock at `<out>/<name>.cbz.lock`.

Flags:

| Flag | Default | Notes |
|---|---|---|
| `--out` | `~/Documents/Manga` | Root for all `<name>.cbz` archives |
| `--name` | URL slug | Pass `--name "Friendly Title"` to control the filename |
| `--concurrency` | `4` | Chapters in flight; drop to 2 if you get rate-limited |
| `--from N` / `--to M` | none | Inclusive range filter (applies to image download in `resume` / `sync-manga`; ignored by `sync-comments`) |
| `--refresh-comments` | off | `sync-comments` only: re-scrape and **replace** comments on **all** archived chapters, not just those missing comments. Warned and ignored in other modes. |
| `--cookies` | platform default | Path to the cookie JSON file |
| `--verbose` | off | Per-chapter progress to stderr |

Common recipes:

```sh
# Fresh archive: download every chapter + comments
bin/downloader sync-manga --name "Friendly Title" "<manga-url>"

# Resume after a Cloudflare expiry — already-archived chapters are skipped
bin/downloader resume "<manga-url>"

# Backfill comment pages onto an existing archive (no new chapters)
bin/downloader sync-comments "<manga-url>"

# Re-scrape comments (up to 5 pages) and REPLACE them on EVERY archived
# chapter — use as a one-off after the comment-page depth changed
bin/downloader sync-comments --refresh-comments "<manga-url>"

# Be polite on a small laptop / flaky connection
bin/downloader sync-manga --concurrency 2 --verbose "<manga-url>"
```

Exit codes:

- `0` — every chapter downloaded
- `1` — at least one chapter failed; rerun with `--resume`
- `2` — setup error (bad URL, missing cookies, etc.)

---

## Claude Desktop (MCP server)

The same operations are available to Claude Desktop via a local MCP
server. Build the binary first (`go build -o bin/downloader .`),
then add this block to your Claude Desktop config
(`~/Library/Application Support/Claude/claude_desktop_config.json`
on macOS):

```json
{
  "mcpServers": {
    "manga-downloader": {
      "command": "<absolute-path-to-repo>/bin/downloader",
      "args": ["mcp"]
    }
  }
}
```

Replace `<absolute-path-to-repo>` with the directory you cloned this
repo into (Claude Desktop needs an absolute path).

Restart Claude Desktop. You can then ask things like:

- "List my manga."
- "Sync new chapters for Gintama from `<url>`."
- "Backfill comments for Hikaru no Go from `<url>`."

When Cloudflare rotates the token, the sync tool returns a
`CF_TOKEN_EXPIRED` error and Claude will ask you for a fresh value.
Paste the `cf_clearance` you copied from DevTools and Claude calls
`update_cookie` for you, then retries the sync.

### Tools

| Tool | Purpose |
|---|---|
| `update_cookie` | Write a new `cf_clearance` (and optionally `user_agent` / `domain`). |
| `get_cookie_status` | Whether the cookie file has a `cf_clearance`, its mtime, and the last 8 chars (for confirmation). |
| `list_manga` | Every `.cbz` under `--out` with chapter + comment counts. |
| `inspect_manga` | The same data for one named archive. |
| `sync_manga` | New chapters + comments + backfill (mode equivalent of CLI `sync-manga`). |
| `resume` | New chapters + comments only. |
| `sync_comments` | Backfill comments only. |
| `cancel_run` | Cancel the active sync. Partial progress survives via scratch markers. |

### Manual smoke test (no Claude Desktop needed)

```sh
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' | bin/downloader mcp
```

Expect a single JSON line on stdout naming the server
`manga-downloader`.

---

## Behaviour notes

- **Atomic writes**: each image is written to `NNN.ext.part` and
  renamed only on success. A crash mid-download never leaves a
  half-written file.
- **`.done` sentinel**: only written after every image in a chapter
  is on disk. `--resume` looks for this file, not for image counts.
- **Retries**: each request retries 3× with exponential backoff on
  429, 5xx, and dial errors. 403 is treated as Cloudflare expiry and
  surfaced immediately.
- **Politeness**: 200–500 ms jitter between successful requests.
- **Comments**: each chapter's top-level comments are scraped (up to 5
  pages, stopping early on the first empty page) and rendered into a
  `zzz-comments.png` inside the chapter folder. Replies are not
  scraped. New chapters get comments automatically; to refresh comments
  on chapters already in the archive, use
  `sync-comments --refresh-comments`, which replaces the existing
  comments page rather than duplicating it.

---

## Cookie expiry mid-run

For long mangas the cookie may expire before the run finishes. If
you see this on stderr:

```
chap N: list images: fetch chapter page: cloudflare cookie likely expired
→ refresh cf_clearance in /…/cookies.json and re-run with --resume
```

…just refresh `cf_clearance` from the same browser tab, save the
file, and re-run with `--resume`. Already-downloaded chapters are
skipped instantly.

If a fresh cookie is *also* rejected, the source site may have
changed domain (these aggregators rebrand periodically). Update the
URL you pass, change `domain` in `cookies.json`, and rewrite the
cached chapter URLs on disk:

```sh
find ~/Documents/Manga -name .chapters.json -exec \
  sed -i '' 's/<old-domain>/<new-domain>/g' {} +
```

---

## Packaging into `.cbz`

After downloading, run `./package-cbz.sh` to bundle each manga
folder into a single `.cbz` (Comic Book Zip) file readable by any
comic reader. Defaults to walking `~/Documents/Manga`; pass a
different root as the first arg.

```sh
./package-cbz.sh                    # walks ~/Documents/Manga
./package-cbz.sh /path/to/manga     # custom root
```

**Adding new chapters to an existing `.cbz`:**

When you re-run the downloader for an ongoing manga, download with
`--resume` as usual, then re-run `package-cbz.sh`. It uses
`zip -u` (update mode) which only appends the new chapter folders
to the existing archive — no unzip/rezip required, so even a 6 GB
archive only takes seconds to update.

The script excludes downloader bookkeeping files (`.done`,
`.chapters.json`, `.DS_Store`) so the archive is just images.

---

## Project layout

```
downloader/
├── main.go                         # flag parsing, wiring, exit codes
├── package-cbz.sh                  # post-download CBZ packaging
├── status.sh                       # quick "what's the run doing" check
├── PLAN.md                         # design + acceptance criteria
├── AGENTS.md                       # context for future agents
└── internal/
    ├── fetcher/                    # cookies + retries + jitter
    ├── site/
    │   ├── site.go                 # Site interface
    │   └── source/                 # selectors for the supported site
    └── downloader/                 # orchestration: list → fetch → save
```

The `Site` and `Fetcher` interfaces are deliberate seams: parsing is
unit-tested with checked-in HTML fixtures, orchestration is
unit-tested with a fake `Fetcher`. Adding a second site is a new
package under `internal/site/`.

## Tests

```sh
go test ./...
go vet ./...
gofmt -l .
```

The parser tests use synthetic HTML fixtures in
`internal/site/source/testdata/`. If the source site's HTML changes,
those tests fail red — update the selectors in
`internal/site/source/site.go` and the fixtures together.

---

## Limitations / non-goals

- **One source site.**
- **No CBZ during download** — output is plain folders; use
  `package-cbz.sh` afterwards.
- **No watch mode** — re-run when new chapters drop.
- **No GUI** — CLI only.

---

## Licence

MIT. Use it for your own archive of titles you have legitimate
access to. Don't redistribute downloaded content.
