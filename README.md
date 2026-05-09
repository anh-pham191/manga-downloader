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

```sh
bin/downloader "<manga-url>"
```

Defaults:

| Flag | Default | Notes |
|---|---|---|
| `--out` | `~/Documents/Manga` | Root for all manga folders |
| `--name` | URL slug | Pass `--name "Friendly Title"` for a nicer folder |
| `--concurrency` | `4` | Chapters in flight; drop to 2 if you get rate-limited |
| `--from N` / `--to M` | none | Inclusive range filter |
| `--resume` | off | Skip chapters whose folder already contains `.done` |
| `--cookies` | platform default | Path to the JSON file above |
| `--verbose` | off | Per-chapter progress to stderr |

Common recipes:

```sh
# Download just the first 5 chapters into a custom folder
bin/downloader --to 5 --name "Friendly Title" "<manga-url>"

# Resume after a Cloudflare expiry — already-done chapters are instant
bin/downloader --resume "<manga-url>"

# Be polite on a small laptop / flaky connection
bin/downloader --concurrency 2 --verbose "<manga-url>"
```

Exit codes:

- `0` — every chapter downloaded
- `1` — at least one chapter failed; rerun with `--resume`
- `2` — setup error (bad URL, missing cookies, etc.)

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
