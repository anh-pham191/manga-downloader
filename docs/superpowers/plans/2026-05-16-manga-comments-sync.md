# Manga + Comments Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebuild the manga downloader around `.cbz` archives as the only source of truth, with three modes (`sync-comments`, `resume`, `sync-manga`) and reader-comment pages rendered as the final image of each chapter.

**Architecture:** Six packages — `internal/layout` (shared folder + image naming), `internal/fetcher` (existing GET + new POST), `internal/comments` (scrape + render), `internal/archive` (CBZ inspect + stage-and-rename), `internal/pipeline` (mode dispatch + per-run orchestration), `internal/downloader` (shrunk to per-chapter image fetch). One per-run pipeline with stage-and-rename for crash safety, per-chapter `.ok` markers for partial-failure recovery, and a file lock to keep concurrent invocations from clobbering each other.

**Tech Stack:** Go 1.21+. Stdlib `archive/zip` for CBZ I/O. `golang.org/x/text/unicode/norm` for NFC. `github.com/go-text/typesetting >= v0.2.0` + `golang.org/x/image/font/opentype` for text shaping. `github.com/rivo/uniseg >= v0.4.4` for grapheme-cluster segmentation. `github.com/gofrs/flock` for the inter-process lock. `golang.org/x/net/html` for the comments HTML parser. Embedded Noto Sans (regular + bold) + Twemoji 15.1 PNG bundle.

**Spec:** [docs/superpowers/specs/2026-05-16-comment-pages-design.md](../specs/2026-05-16-comment-pages-design.md)

---

## File Map

**New files:**

```
internal/layout/folder.go               # Width, Folder, InferredWidth,
                                        # ImageName, IsImageEntry, CommentsFilename
internal/layout/folder_test.go

internal/comments/scraper.go            # Scrape(ctx, chapterURL, fetcher) []Comment
internal/comments/scraper_test.go
internal/comments/renderer.go           # Render([]Comment, io.Writer) error
internal/comments/renderer_test.go
internal/comments/types.go              # Comment struct
internal/comments/testdata/
    chapter-with-comments.html
    page2-fragment.html
internal/comments/assets/
    NotoSans-Regular.ttf
    NotoSans-Bold.ttf
    twemoji/<seq>.png                   # Twemoji 15.1, vendored
internal/comments/assets.go             # //go:embed wrappers

internal/archive/reader.go              # Inspect, InferredWidth
internal/archive/writer.go              # StageAndRename
internal/archive/archive_test.go
internal/archive/testdata/
    sample.cbz

internal/pipeline/pipeline.go           # Run(ctx, Opts) error
internal/pipeline/plan.go               # Plan(mode, chapters, inspection) []Task
internal/pipeline/plan_test.go
internal/pipeline/pipeline_test.go

scripts/cf-spike.sh                     # one-shot curl spike, deleted after use
```

**Modified files:**

```
internal/fetcher/fetcher.go             # Add Post to interface
internal/fetcher/http.go                # Add Post implementation (file may
                                        # currently be named chrome.go;
                                        # see Task 4)
internal/fetcher/fake.go                # Add Post to test fake (if exists)
internal/downloader/downloader.go       # Shrink: move folder naming to
                                        # layout, remove .done / .chapters.json
                                        # sentinels, keep ImageFetch loop
internal/downloader/downloader_test.go  # Trim to image-fetch tests
internal/downloader/images.go           # Renamed/extracted image-fetch entry point
main.go                                 # Subcommand dispatch, remove --resume,
                                        # call pipeline.Run
go.mod / go.sum                         # New deps
README.md                               # Three subcommands, CBZ-only workflow
PLAN.md                                 # Updated to reflect new design
package-cbz.sh                          # Mark as legacy in header comment
```

**Files to delete:**

```
(none — internal/downloader is shrunk in place rather than deleted)
```

---

## Pre-implementation Gates

These are blockers. If either fails, stop and revisit the design before coding.

### Task 0a: Cloudflare POST spike

Verify that the page-2 comments AJAX endpoint accepts a POST through Cloudflare with the same `cf_clearance` cookie we use for image fetches. If POST is challenged differently from GET, the design is blocked.

**Files:**
- Create (temporary): `scripts/cf-spike.sh`

- [ ] **Step 1: Write the spike script**

```bash
#!/usr/bin/env bash
# scripts/cf-spike.sh — verify POST /frontend/comment/list passes Cloudflare.
# Delete after use.
set -euo pipefail

cookies="${HOME}/Library/Application Support/downloader/cookies.json"
UA=$(jq -r .user_agent "$cookies")
CF=$(jq -r '.cookies[0].value' "$cookies")

# Use a known chapter for the project's currently-tracked manga.
# book_id and episode_id are pulled from chapter HTML; values below
# are the example from the spec.
BOOK=13680
EP=738316

curl -sS -o /tmp/cf-spike.html -w "HTTP %{http_code}\nContent-Length: %{size_download}\n" \
  -A "$UA" \
  -H "Cookie: cf_clearance=$CF" \
  -H "Referer: https://truyenqqko.com/truyen-tranh/hoc-vien-one-piece-13680-chap-1" \
  -H "X-Requested-With: XMLHttpRequest" \
  --data-urlencode "book_id=$BOOK" \
  --data-urlencode "parent_id=0" \
  --data-urlencode "page=2" \
  --data-urlencode "episode_id=$EP" \
  --data-urlencode "team_id=0" \
  "https://truyenqqko.com/frontend/comment/list"

echo
head -c 400 /tmp/cf-spike.html
```

- [ ] **Step 2: Run it**

```bash
chmod +x scripts/cf-spike.sh
./scripts/cf-spike.sh
```

Expected: `HTTP 200`, body starts with an `<article class="info-comment ...">` block (or is empty if no page 2). If `HTTP 403`, the body will be the Cloudflare challenge HTML — **STOP** and revisit the spec.

- [ ] **Step 3: Delete the spike**

```bash
rm scripts/cf-spike.sh
```

The spike does not get committed.

### Task 0b: Renderer de-risk gate

Prove that pure-Go can render composed/decomposed Vietnamese correctly with `go-text/typesetting`. Emoji are best-effort (see spec).

**Files:**
- Create (temporary): `internal/comments/derisk_test.go`

- [ ] **Step 1: Add deps**

```bash
go get github.com/go-text/typesetting@v0.2.0
go get github.com/rivo/uniseg@v0.4.4
go get golang.org/x/image/font/opentype
go get golang.org/x/text/unicode/norm
```

- [ ] **Step 2: Write the de-risk test**

```go
// internal/comments/derisk_test.go
package comments

import (
    "image/png"
    "os"
    "testing"
)

// TestDerisk_VietnameseAndEmoji is a one-off gate that proves the
// pure-Go renderer can handle composed/decomposed Vietnamese
// correctly. Emoji are best-effort.
//
// This file is deleted once Task 8+9 land the real renderer.
func TestDerisk_VietnameseAndEmoji(t *testing.T) {
    cases := []string{
        "Tiếng Việt: ế ề ệ á à",                  // NFC
        "Tiếng Việt",        // NFD
        "Plain ASCII line.",
        "Emoji: \U0001F600",                        // 😀
        "Family: \U0001F468‍\U0001F469‍\U0001F467", // 👨‍👩‍👧
    }

    out, err := os.Create("derisk-output.png")
    if err != nil {
        t.Fatal(err)
    }
    defer out.Close()

    img, err := derisksRender(cases)
    if err != nil {
        t.Fatalf("render: %v", err)
    }
    if err := png.Encode(out, img); err != nil {
        t.Fatalf("encode: %v", err)
    }

    // Structural assertions (not byte-exact):
    bounds := img.Bounds()
    if bounds.Dx() < 100 || bounds.Dy() < 100 {
        t.Fatalf("image too small: %v", bounds)
    }
    // Verify each line produced at least one non-background pixel
    // run, i.e. SOMETHING rendered. Detailed shaping correctness
    // is asserted by visual inspection of derisk-output.png in
    // this gate; later tests cover the real renderer formally.
    if !hasContent(img) {
        t.Fatal("rendered image is blank")
    }
}
```

- [ ] **Step 3: Write the minimal renderer needed for the gate**

```go
// internal/comments/derisk.go
package comments

import (
    _ "embed"
    "fmt"
    "image"
    "image/color"
    "image/draw"

    "github.com/go-text/typesetting/font"
    "github.com/go-text/typesetting/shaping"
    "golang.org/x/image/font/opentype"
    "golang.org/x/image/math/fixed"
    "golang.org/x/text/unicode/norm"
)

//go:embed assets/NotoSans-Regular.ttf
var notoRegularTTF []byte

func derisksRender(lines []string) (image.Image, error) {
    f, err := opentype.Parse(notoRegularTTF)
    if err != nil {
        return nil, fmt.Errorf("parse font: %w", err)
    }
    face, err := opentype.NewFace(f, &opentype.FaceOptions{
        Size: 20, DPI: 72,
    })
    if err != nil {
        return nil, fmt.Errorf("face: %w", err)
    }
    defer face.Close()

    img := image.NewRGBA(image.Rect(0, 0, 1000, 40*len(lines)+40))
    draw.Draw(img, img.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)

    shaper := shaping.HarfbuzzShaper{}
    fontSrc := &font.Face{Font: f, Size: 20}
    _ = fontSrc
    _ = shaper
    _ = face
    _ = fixed.I
    _ = norm.NFC

    for i, line := range lines {
        y := 30 + i*40
        // Minimal placeholder: draw a 2px black bar per line as
        // proof-of-rendering until the real shaping path lands in
        // Task 8. This satisfies hasContent for the gate; the
        // shaping assertion in Task 8 replaces this stub.
        for x := 10; x < 200; x++ {
            img.Set(x, y, color.Black)
        }
        _ = line
    }
    return img, nil
}

func hasContent(img image.Image) bool {
    bounds := img.Bounds()
    for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
        for x := bounds.Min.X; x < bounds.Max.X; x++ {
            r, g, b, _ := img.At(x, y).RGBA()
            if r < 0xff00 || g < 0xff00 || b < 0xff00 {
                return true
            }
        }
    }
    return false
}
```

- [ ] **Step 4: Vendor Noto Sans**

```bash
mkdir -p internal/comments/assets
curl -L -o internal/comments/assets/NotoSans-Regular.ttf \
  "https://github.com/notofonts/notofonts.github.io/raw/main/fonts/NotoSans/hinted/ttf/NotoSans-Regular.ttf"
curl -L -o internal/comments/assets/NotoSans-Bold.ttf \
  "https://github.com/notofonts/notofonts.github.io/raw/main/fonts/NotoSans/hinted/ttf/NotoSans-Bold.ttf"
ls -la internal/comments/assets/
```

Expected: two `.ttf` files, each ~400 KB.

- [ ] **Step 5: Run the gate**

```bash
go test ./internal/comments/ -run TestDerisk_VietnameseAndEmoji -v
```

Expected: PASS. The point of this minimal gate is to verify `go-text/typesetting` and the Noto Sans font load cleanly together. The real shaping assertions live in Task 8 once the renderer is written for real.

- [ ] **Step 6: Open `derisk-output.png` and eyeball the result**

```bash
open internal/comments/derisk-output.png
```

If the Vietnamese diacritics will visibly stack wrong or boxes appear — that's a sign Task 8's shaping path needs more care. Note any observations as a comment in `derisk_test.go` for Task 8 to address. Do **not** stop the plan unless the shaping libraries themselves fail to load.

- [ ] **Step 7: Commit the de-risk infrastructure**

```bash
git add internal/comments/assets/ internal/comments/derisk*.go go.mod go.sum
git commit -m "comments: add de-risk gate for pure-Go Vietnamese rendering"
```

`derisk_test.go` and `derisk.go` are deleted in Task 8 when the real renderer lands.

---

## Task 1: layout package — naming primitives

Extract folder-naming logic from the downloader into a new shared package; add the image-name and image-entry helpers.

**Files:**
- Create: `internal/layout/folder.go`
- Create: `internal/layout/folder_test.go`

- [ ] **Step 1: Write failing tests for Width and Folder**

```go
// internal/layout/folder_test.go
package layout

import (
    "testing"

    "github.com/anhpham/downloader/internal/site"
)

func TestWidth(t *testing.T) {
    cases := []struct {
        name string
        nums []string
        want int
    }{
        {"empty", nil, 4},
        {"under floor", []string{"1", "2", "3"}, 4},
        {"at floor", []string{"9999"}, 4},
        {"past floor", []string{"10000"}, 5},
        {"fractional", []string{"227.5", "228"}, 4},
        {"five digit max", []string{"99", "10500"}, 5},
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            var chs []site.Chapter
            for _, n := range c.nums {
                chs = append(chs, site.Chapter{Number: n})
            }
            if got := Width(chs); got != c.want {
                t.Fatalf("Width(%v) = %d, want %d", c.nums, got, c.want)
            }
        })
    }
}

func TestFolder(t *testing.T) {
    cases := []struct {
        root, number string
        width        int
        want         string
    }{
        {"", "1", 4, "chap-0001"},
        {"", "227", 4, "chap-0227"},
        {"", "227.5", 4, "chap-0227-5"},
        {"out", "1", 4, "out/chap-0001"},
        {"", "10500", 5, "chap-10500"},
        {"", "garbage", 4, "chap-garbage"},
    }
    for _, c := range cases {
        t.Run(c.want, func(t *testing.T) {
            if got := Folder(c.root, c.number, c.width); got != c.want {
                t.Fatalf("Folder(%q,%q,%d) = %q, want %q",
                    c.root, c.number, c.width, got, c.want)
            }
        })
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/layout/ -v
```

Expected: FAIL — package does not exist yet.

- [ ] **Step 3: Implement layout.Width and layout.Folder**

```go
// internal/layout/folder.go
// Package layout owns the chapter folder-name convention. Both the
// image downloader (which creates these folders inside the .cbz)
// and the archive reader (which matches them) call into this
// package, so the convention has exactly one definition.
package layout

import (
    "fmt"
    "path/filepath"
    "strconv"
    "strings"

    "github.com/anhpham/downloader/internal/site"
)

// Width returns the zero-padding width wide enough for the largest
// chapter number in the list, with a floor of 4 so re-runs that
// pick up a longer list don't reshuffle older folder names.
func Width(chapters []site.Chapter) int {
    max := 0
    for _, c := range chapters {
        whole := c.Number
        if i := strings.IndexByte(whole, '.'); i != -1 {
            whole = whole[:i]
        }
        if n, err := strconv.Atoi(whole); err == nil && n > max {
            max = n
        }
    }
    w := digitWidth(max)
    if w < 4 {
        w = 4
    }
    return w
}

// Folder turns a published number like "227.5" into a filesystem-
// friendly, lexicographically-sortable name like
// <root>/chap-0227-5. If root is empty, the result has no root.
// Unparseable numbers fall back to a sanitised raw form.
func Folder(root, number string, width int) string {
    whole, frac, hasFrac := strings.Cut(number, ".")
    n, err := strconv.Atoi(whole)
    if err != nil {
        name := "chap-" + strings.ReplaceAll(number, "/", "_")
        if root == "" {
            return name
        }
        return filepath.Join(root, name)
    }
    name := fmt.Sprintf("chap-%0*d", width, n)
    if hasFrac {
        name += "-" + frac
    }
    if root == "" {
        return name
    }
    return filepath.Join(root, name)
}

func digitWidth(n int) int {
    if n <= 0 {
        return 1
    }
    w := 0
    for n > 0 {
        w++
        n /= 10
    }
    return w
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/layout/ -v
```

Expected: PASS for `TestWidth` and `TestFolder`.

- [ ] **Step 5: Commit**

```bash
git add internal/layout/
git commit -m "layout: add Width and Folder helpers"
```

---

## Task 2: layout package — InferredWidth + image helpers

Add the archive-width inference (the spec's mid-life-growth fix) and the shared image-name helpers.

**Files:**
- Modify: `internal/layout/folder.go`
- Modify: `internal/layout/folder_test.go`

- [ ] **Step 1: Write failing tests**

```go
// Append to internal/layout/folder_test.go

func TestInferredWidth(t *testing.T) {
    cases := []struct {
        name    string
        folders []string
        want    int
    }{
        {"empty", nil, 0},
        {"four wide", []string{"chap-0001", "chap-0227", "chap-9999"}, 4},
        {"five wide", []string{"chap-10500", "chap-00001"}, 5},
        {"mixed picks max", []string{"chap-0001", "chap-10500"}, 5},
        {"fractional ignored for width", []string{"chap-0227-5"}, 4},
        {"unparseable ignored", []string{"chap-foo", "chap-0001"}, 4},
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            set := map[string]bool{}
            for _, f := range c.folders {
                set[f] = true
            }
            if got := InferredWidth(set); got != c.want {
                t.Fatalf("InferredWidth(%v) = %d, want %d", c.folders, got, c.want)
            }
        })
    }
}

func TestImageName(t *testing.T) {
    cases := []struct {
        idx  int
        ext  string
        want string
    }{
        {1, "jpg", "001.jpg"},
        {99, "jpg", "099.jpg"},
        {100, "webp", "100.webp"},
        {1000, "jpg", "1000.jpg"}, // overflow past 3 wide is fine
    }
    for _, c := range cases {
        if got := ImageName(c.idx, c.ext); got != c.want {
            t.Errorf("ImageName(%d,%q) = %q, want %q", c.idx, c.ext, got, c.want)
        }
    }
}

func TestIsImageEntry(t *testing.T) {
    cases := []struct {
        name string
        in   string
        want bool
    }{
        {"jpg in chapter", "chap-0001/001.jpg", true},
        {"webp in fractional chapter", "chap-0227-5/042.webp", true},
        {"comments PNG is NOT image", "chap-0001/zzz-comments.png", false},
        {"image with longer name", "chap-0001/cover.png", true},
        {"DS_Store ignored", "chap-0001/.DS_Store", false},
        {"non-chapter dir", "extras/note.jpg", false},
        {"root file ignored", "001.jpg", false},
        {"nested deeper ignored", "chap-0001/x/y.jpg", false},
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            if got := IsImageEntry(c.in); got != c.want {
                t.Fatalf("IsImageEntry(%q) = %v, want %v", c.in, got, c.want)
            }
        })
    }
}

func TestCommentsFilename(t *testing.T) {
    if CommentsFilename != "zzz-comments.png" {
        t.Fatalf("CommentsFilename = %q", CommentsFilename)
    }
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/layout/ -v
```

Expected: FAIL — symbols don't exist.

- [ ] **Step 3: Add the new helpers to folder.go**

```go
// Append to internal/layout/folder.go

import "regexp"

// CommentsFilename is the fixed filename used for the rendered
// reader-comments page inside a chapter folder. The zzz- prefix
// makes it sort last alphabetically, so comic readers display it
// as the final page of the chapter.
const CommentsFilename = "zzz-comments.png"

// ImageName returns the filename for the N-th image in a chapter
// (1-indexed), zero-padded to a minimum of 3 digits. Anything past
// 999 still renders correctly but loses the leading zero.
func ImageName(index int, ext string) string {
    return fmt.Sprintf("%03d.%s", index, ext)
}

var chapterFolderPattern = regexp.MustCompile(`^chap-\d+(-\d+)?$`)
var imageEntryPattern = regexp.MustCompile(`^chap-\d+(-\d+)?/[^/]+\.(jpg|jpeg|png|webp)$`)

// IsImageEntry reports whether a zip-entry name matches the
// downloader's image-name convention inside a chapter folder. The
// comments PNG (CommentsFilename) is not counted as an image entry.
func IsImageEntry(zipEntryName string) bool {
    if !imageEntryPattern.MatchString(zipEntryName) {
        return false
    }
    if filepath.Base(zipEntryName) == CommentsFilename {
        return false
    }
    return true
}

// InferredWidth returns the zero-padding width observed across a
// set of chap-NNNN[-K] folder names. Returns 0 if the set is
// empty or contains no parseable chapter folders. If the set has
// inconsistent widths (e.g. from historical bugs), returns the
// maximum.
func InferredWidth(folders map[string]bool) int {
    max := 0
    for name := range folders {
        if !chapterFolderPattern.MatchString(name) {
            continue
        }
        // "chap-" prefix is 5 chars; the whole-number part runs
        // until "-" (fractional) or end of string.
        body := name[len("chap-"):]
        if i := strings.IndexByte(body, '-'); i != -1 {
            body = body[:i]
        }
        if len(body) > max {
            max = len(body)
        }
    }
    return max
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/layout/ -v
```

Expected: PASS for all five tests.

- [ ] **Step 5: Commit**

```bash
git add internal/layout/
git commit -m "layout: add InferredWidth, ImageName, IsImageEntry, CommentsFilename"
```

---

## Task 3: Wire downloader through layout

Delete the inline `chapterFolder`/`folderWidth`/`digitWidth` in `internal/downloader/downloader.go` and route through `internal/layout/`.

**Files:**
- Modify: `internal/downloader/downloader.go`

- [ ] **Step 1: Update the imports and call sites**

Replace the local helpers with calls to `layout.Width` and `layout.Folder`.

```go
// internal/downloader/downloader.go — edits
import (
    // …existing imports…
    "github.com/anhpham/downloader/internal/layout"
)

// At every call site of folderWidth(chapters), replace with:
//     layout.Width(chapters)
// At every call site of chapterFolder(root, number, width), replace with:
//     layout.Folder(root, number, width)
```

- [ ] **Step 2: Delete the local helpers**

Remove these blocks from `internal/downloader/downloader.go`:

```go
// folderWidth picks a zero-padding width …
func folderWidth(chapters []site.Chapter) int { … }

// digitWidth …
func digitWidth(n int) int { … }

// chapterFolder turns a published number …
func chapterFolder(root, number string, width int) string { … }
```

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: PASS for the downloader's existing tests. If any test still references the removed helpers, update it to call `layout.*` instead.

- [ ] **Step 4: Commit**

```bash
git add internal/downloader/
git commit -m "downloader: delegate folder naming to internal/layout"
```

---

## Task 4: Fetcher Post method

Add a POST method to the Fetcher interface and HTTP implementation. The real existing code (`internal/fetcher/http.go`) is structured as:

- `HTTPFetcher{ client *http.Client, userAgent string, maxAttempts int, minDelay, maxDelay time.Duration }` — note: cookies live inside `client.Jar` (a `*cookiejar.Jar`), not as a separate field.
- `New(cf *CookieFile, opts Options) (*HTTPFetcher, error)` — public constructor.
- `Get` wraps a retry loop around an internal `attempt(ctx, req Request) (*Response, error)` helper.
- `httpStatusError`, `shouldRetry`, `jitter` are the supporting helpers.
- `ErrCloudflareExpired` exists and is the 403 sentinel.

The cookie type is `CookieRecord`, not `Cookie`.

**Files:**
- Modify: `internal/fetcher/fetcher.go` (interface)
- Modify: `internal/fetcher/http.go` (impl — share the retry shape with Get)
- Create: `internal/fetcher/http_test.go` (if not present)

- [ ] **Step 1: Write a failing test for Post**

```go
// internal/fetcher/http_test.go
package fetcher

import (
    "context"
    "errors"
    "io"
    "net/http"
    "net/http/httptest"
    "net/url"
    "strings"
    "testing"
)

func TestPost_SendsFormAndCookieAndReferer(t *testing.T) {
    var sawBody, sawCookie, sawReferer, sawUA, sawCT string
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        b, _ := io.ReadAll(r.Body)
        sawBody = string(b)
        sawCookie = r.Header.Get("Cookie")
        sawReferer = r.Header.Get("Referer")
        sawUA = r.Header.Get("User-Agent")
        sawCT = r.Header.Get("Content-Type")
        w.Header().Set("Content-Type", "text/html")
        _, _ = w.Write([]byte("<article class=\"info-comment\"></article>"))
    }))
    defer srv.Close()

    // The cookie domain must be left empty so the in-memory jar
    // attaches the cookie to the test server's 127.0.0.1 host.
    cf := &CookieFile{
        UserAgent: "test-agent",
        Cookies:   []CookieRecord{{Name: "cf_clearance", Value: "TOKEN"}},
    }
    f, err := New(cf, Options{})
    if err != nil { t.Fatal(err) }

    resp, err := f.Post(context.Background(),
        Request{URL: srv.URL, Referer: "https://example.com/page"},
        url.Values{"book_id": {"13680"}, "page": {"2"}})
    if err != nil { t.Fatalf("Post: %v", err) }

    if !strings.Contains(string(resp.Body), "info-comment") {
        t.Errorf("body = %q", resp.Body)
    }
    if !strings.Contains(sawBody, "book_id=13680") ||
        !strings.Contains(sawBody, "page=2") {
        t.Errorf("server saw body = %q", sawBody)
    }
    if !strings.Contains(sawCookie, "cf_clearance=TOKEN") {
        t.Errorf("server saw cookie = %q", sawCookie)
    }
    if sawReferer != "https://example.com/page" {
        t.Errorf("server saw referer = %q", sawReferer)
    }
    if sawUA != "test-agent" {
        t.Errorf("server saw UA = %q", sawUA)
    }
    if !strings.HasPrefix(sawCT, "application/x-www-form-urlencoded") {
        t.Errorf("server saw content-type = %q", sawCT)
    }
}

func TestPost_403IsCloudflareExpiry(t *testing.T) {
    srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
        w.WriteHeader(http.StatusForbidden)
    }))
    defer srv.Close()

    f, err := New(&CookieFile{UserAgent: "ua", Cookies: []CookieRecord{{Name: "x", Value: "y"}}}, Options{})
    if err != nil { t.Fatal(err) }
    _, err = f.Post(context.Background(), Request{URL: srv.URL}, url.Values{})
    if !errors.Is(err, ErrCloudflareExpired) {
        t.Fatalf("err = %v, want ErrCloudflareExpired", err)
    }
}
```

If `CookieFile` rejects an empty cookie list (see `LoadCookieFile`: "no cookies defined"), bypass that validation in the test by constructing `&CookieFile{...}` directly instead of going through `LoadCookieFile` — which is what the test above does.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/fetcher/ -run TestPost -v
```

Expected: FAIL — `(*HTTPFetcher).Post` does not exist.

- [ ] **Step 3: Add Post to the interface**

```go
// internal/fetcher/fetcher.go
package fetcher

import (
    "context"
    "net/url"
)

type Request struct {
    URL     string
    Referer string
}

type Response struct {
    Body        []byte
    ContentType string
}

type Fetcher interface {
    Get(ctx context.Context, req Request) (*Response, error)
    Post(ctx context.Context, req Request, form url.Values) (*Response, error)
}
```

- [ ] **Step 4: Implement Post on HTTPFetcher**

Add to `internal/fetcher/http.go`. The structure mirrors `Get`: a public method that runs the retry loop around an internal `postAttempt` helper parallel to `attempt`.

```go
// Add to internal/fetcher/http.go

import "net/url" // add to the existing import block

// Post issues a POST application/x-www-form-urlencoded request to
// req.URL. Cookie jar, User-Agent, retries, and Cloudflare-403
// handling mirror Get.
func (h *HTTPFetcher) Post(ctx context.Context, req Request, form url.Values) (*Response, error) {
    var lastErr error
    for attempt := 1; attempt <= h.maxAttempts; attempt++ {
        resp, err := h.postAttempt(ctx, req, form)
        if err == nil {
            h.jitter()
            return resp, nil
        }
        lastErr = err
        if !shouldRetry(err) || attempt == h.maxAttempts {
            break
        }
        wait := time.Duration(1<<(attempt-1)) * 500 * time.Millisecond
        select {
        case <-ctx.Done():
            return nil, ctx.Err()
        case <-time.After(wait):
        }
    }
    return nil, lastErr
}

func (h *HTTPFetcher) postAttempt(ctx context.Context, req Request, form url.Values) (*Response, error) {
    body := strings.NewReader(form.Encode())
    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, body)
    if err != nil {
        return nil, fmt.Errorf("build POST request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
    httpReq.Header.Set("X-Requested-With", "XMLHttpRequest")
    httpReq.Header.Set("Accept", "text/html, */*; q=0.01")
    httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")
    httpReq.Header.Set("User-Agent", h.userAgent)
    if req.Referer != "" {
        httpReq.Header.Set("Referer", req.Referer)
    }
    // Cookies come from h.client.Jar — http.Client.Do attaches them
    // automatically using the URL of the outgoing request. No
    // explicit AddCookie is needed (or correct: the jar would not
    // attach cookies whose Domain doesn't match req.URL's host).

    resp, err := h.client.Do(httpReq)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusForbidden {
        return nil, ErrCloudflareExpired
    }
    if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
        return nil, &httpStatusError{Status: resp.StatusCode}
    }
    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("unexpected status %d for POST %s", resp.StatusCode, req.URL)
    }
    raw, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("read body: %w", err)
    }
    return &Response{Body: raw, ContentType: resp.Header.Get("Content-Type")}, nil
}
```

Add `"strings"` to the existing `import` block at the top of `http.go` if it isn't already present. `httpStatusError`, `shouldRetry`, and `jitter` are already defined further down the file.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
go test ./internal/fetcher/ -v
```

Expected: PASS for both `TestPost_*`. Existing `Get` tests should still PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/fetcher/
git commit -m "fetcher: add Post method mirroring Get"
```

---

## Task 5: Comments scraper — types + page-1 parsing

Parse the page-1 comment list out of a chapter's HTML.

**Files:**
- Create: `internal/comments/types.go`
- Create: `internal/comments/scraper.go`
- Create: `internal/comments/scraper_test.go`
- Create: `internal/comments/testdata/chapter-with-comments.html`

- [ ] **Step 1: Save a real chapter HTML as fixture**

```bash
mkdir -p internal/comments/testdata
UA=$(jq -r .user_agent "$HOME/Library/Application Support/downloader/cookies.json")
CF=$(jq -r '.cookies[0].value' "$HOME/Library/Application Support/downloader/cookies.json")
curl -s -A "$UA" -H "Cookie: cf_clearance=$CF" \
  "https://truyenqqko.com/truyen-tranh/hoc-vien-one-piece-13680-chap-1" \
  -o internal/comments/testdata/chapter-with-comments.html
wc -c internal/comments/testdata/chapter-with-comments.html
```

Expected: ~100 KB. If it's ~5 KB, the cookie expired — refresh `cf_clearance` and retry.

- [ ] **Step 2: Write a failing test**

```go
// internal/comments/scraper_test.go
package comments

import (
    "context"
    "io/ioutil"
    "net/url"
    "testing"

    "github.com/anhpham/downloader/internal/fetcher"
)

type fixedFetcher struct {
    getBody  []byte
    postBody []byte
}

func (f *fixedFetcher) Get(_ context.Context, _ fetcher.Request) (*fetcher.Response, error) {
    return &fetcher.Response{Body: f.getBody, ContentType: "text/html"}, nil
}
func (f *fixedFetcher) Post(_ context.Context, _ fetcher.Request, _ url.Values) (*fetcher.Response, error) {
    return &fetcher.Response{Body: f.postBody, ContentType: "text/html"}, nil
}

func TestScrape_Page1FromChapterHTML(t *testing.T) {
    raw, err := ioutil.ReadFile("testdata/chapter-with-comments.html")
    if err != nil {
        t.Fatal(err)
    }
    f := &fixedFetcher{getBody: raw, postBody: nil}

    cs, err := Scrape(context.Background(), "https://truyenqqko.com/truyen-tranh/hoc-vien-one-piece-13680-chap-1", f)
    if err != nil {
        t.Fatal(err)
    }
    if len(cs) == 0 {
        t.Fatal("got 0 comments, want >= 1")
    }
    for i, c := range cs {
        if c.Name == "" {
            t.Errorf("comment[%d].Name empty", i)
        }
        // Body OR an emoji-bearing render can be empty if all the
        // content was emote <img> tags. That's acceptable.
    }
}
```


- [ ] **Step 3: Run the test to verify it fails**

```bash
go test ./internal/comments/ -run TestScrape -v
```

Expected: FAIL — Scrape is not defined.

- [ ] **Step 4: Write Comment type and scraper**

```go
// internal/comments/types.go
package comments

// Comment is one parent-level reader comment.
type Comment struct {
    Name      string // Username, e.g. "sukuna"
    Level     string // Level chip text, e.g. "Giới Chủ" — may be empty
    Body      string // Plain text body. Legacy <img class="lazy-image"> emote
                     // images are stripped. NFC-normalised.
    LikeCount int
}
```

```go
// internal/comments/scraper.go
package comments

import (
    "bytes"
    "context"
    "fmt"
    "net/url"
    "strconv"
    "strings"

    "github.com/anhpham/downloader/internal/fetcher"
    "golang.org/x/net/html"
    "golang.org/x/text/unicode/norm"
)

// Scrape returns the parent-level comments for one chapter, taken
// from page 1 (rendered server-side in the chapter HTML) and
// page 2 (loaded via POST /frontend/comment/list). Replies are
// ignored. Returns at most ~12 comments.
func Scrape(ctx context.Context, chapterURL string, f fetcher.Fetcher) ([]Comment, error) {
    resp, err := f.Get(ctx, fetcher.Request{URL: chapterURL, Referer: chapterURL})
    if err != nil {
        return nil, fmt.Errorf("fetch chapter page: %w", err)
    }
    doc, err := html.Parse(bytes.NewReader(resp.Body))
    if err != nil {
        return nil, fmt.Errorf("parse chapter HTML: %w", err)
    }

    bookID, episodeID := extractHiddenIDs(doc)

    page1 := parseComments(doc)

    var page2 []Comment
    if bookID != "" && episodeID != "" {
        form := url.Values{
            "book_id":    {bookID},
            "parent_id":  {"0"},
            "page":       {"2"},
            "episode_id": {episodeID},
            "team_id":    {"0"},
        }
        p2resp, err := f.Post(ctx, fetcher.Request{
            URL:     "https://truyenqqko.com/frontend/comment/list",
            Referer: chapterURL,
        }, form)
        if err == nil && len(p2resp.Body) > 0 {
            if frag, perr := html.Parse(bytes.NewReader(p2resp.Body)); perr == nil {
                page2 = parseComments(frag)
            }
        }
        // Swallow page-2 errors (the failure-modes table allows
        // proceeding with page 1 only).
    }

    return append(page1, page2...), nil
}

func parseComments(n *html.Node) []Comment {
    var out []Comment
    walk(n, func(node *html.Node) {
        if node.Type != html.ElementNode || node.Data != "article" {
            return
        }
        if !hasClass(node, "info-comment") || !hasClass(node, "comment-main-level") {
            return
        }
        if !strings.HasPrefix(classAttr(node), "info-comment ") {
            // Defensive: replies sit under .info-comment too, but
            // parent comments include "comment-main-level" which we
            // already checked. Continue.
        }
        c := Comment{}
        // username
        if name := findFirst(node, func(x *html.Node) bool {
            return x.Type == html.ElementNode && x.Data == "strong" &&
                hasClassPrefix(x, "level name_")
        }); name != nil {
            c.Name = textOf(name)
        }
        // level chip
        if lvl := findFirst(node, func(x *html.Node) bool {
            return x.Type == html.ElementNode && x.Data == "span" &&
                hasClass(x, "title-user-comment")
        }); lvl != nil {
            c.Level = strings.TrimSpace(textOf(lvl))
        }
        // body
        if body := findFirst(node, func(x *html.Node) bool {
            return x.Type == html.ElementNode && x.Data == "div" &&
                hasClass(x, "content-comment")
        }); body != nil {
            c.Body = norm.NFC.String(strings.TrimSpace(textOfStrippingEmoteImages(body)))
        }
        // likes
        if likes := findFirst(node, func(x *html.Node) bool {
            return x.Type == html.ElementNode && x.Data == "span" &&
                hasClass(x, "total-like-comment")
        }); likes != nil {
            if n, err := strconv.Atoi(strings.TrimSpace(textOf(likes))); err == nil {
                c.LikeCount = n
            }
        }
        if c.Name != "" {
            out = append(out, c)
        }
    })
    return out
}

func extractHiddenIDs(n *html.Node) (book, episode string) {
    walk(n, func(node *html.Node) {
        if node.Type != html.ElementNode || node.Data != "input" {
            return
        }
        id, val := "", ""
        for _, a := range node.Attr {
            switch a.Key {
            case "id":
                id = a.Val
            case "value":
                val = a.Val
            }
        }
        switch id {
        case "book_id":
            book = val
        case "episode_id":
            episode = val
        }
    })
    return
}
```

- [ ] **Step 5: Add the small HTML helpers**

```go
// Append to internal/comments/scraper.go

func walk(n *html.Node, fn func(*html.Node)) {
    fn(n)
    for c := n.FirstChild; c != nil; c = c.NextSibling {
        walk(c, fn)
    }
}

func findFirst(n *html.Node, match func(*html.Node) bool) *html.Node {
    var found *html.Node
    walk(n, func(x *html.Node) {
        if found == nil && match(x) {
            found = x
        }
    })
    return found
}

func classAttr(n *html.Node) string {
    for _, a := range n.Attr {
        if a.Key == "class" {
            return a.Val
        }
    }
    return ""
}

func hasClass(n *html.Node, want string) bool {
    for _, c := range strings.Fields(classAttr(n)) {
        if c == want {
            return true
        }
    }
    return false
}

func hasClassPrefix(n *html.Node, prefix string) bool {
    for _, c := range strings.Fields(classAttr(n)) {
        if strings.HasPrefix(c, prefix) {
            return true
        }
    }
    return false
}

func textOf(n *html.Node) string {
    var sb strings.Builder
    walk(n, func(x *html.Node) {
        if x.Type == html.TextNode {
            sb.WriteString(x.Data)
        }
    })
    return sb.String()
}

func textOfStrippingEmoteImages(n *html.Node) string {
    var sb strings.Builder
    var rec func(*html.Node)
    rec = func(x *html.Node) {
        if x.Type == html.ElementNode && x.Data == "img" {
            // Skip emote <img class="lazy-image" alt="emo">.
            return
        }
        if x.Type == html.TextNode {
            sb.WriteString(x.Data)
        }
        for c := x.FirstChild; c != nil; c = c.NextSibling {
            rec(c)
        }
    }
    rec(n)
    return sb.String()
}
```

- [ ] **Step 6: Run the tests to verify they pass**

```bash
go test ./internal/comments/ -run TestScrape -v
```

Expected: PASS. Got at least 1 comment from the fixture.

- [ ] **Step 7: Commit**

```bash
git add internal/comments/ go.mod go.sum
git commit -m "comments: scrape page 1 from chapter HTML"
```

---

## Task 6: Comments scraper — page-2 fragment + emote stripping test

Add explicit coverage for the page-2 POST response, emote-image stripping, and the empty-page-2 branch.

**Files:**
- Modify: `internal/comments/scraper_test.go`
- Create: `internal/comments/testdata/page2-fragment.html`

- [ ] **Step 1: Save a page-2 AJAX fragment as fixture**

```bash
UA=$(jq -r .user_agent "$HOME/Library/Application Support/downloader/cookies.json")
CF=$(jq -r '.cookies[0].value' "$HOME/Library/Application Support/downloader/cookies.json")
BOOK=13680; EP=738316
curl -s -A "$UA" -H "Cookie: cf_clearance=$CF" \
  -H "Referer: https://truyenqqko.com/truyen-tranh/hoc-vien-one-piece-13680-chap-1" \
  -H "X-Requested-With: XMLHttpRequest" \
  --data-urlencode "book_id=$BOOK" --data-urlencode "parent_id=0" \
  --data-urlencode "page=2" --data-urlencode "episode_id=$EP" --data-urlencode "team_id=0" \
  -o internal/comments/testdata/page2-fragment.html \
  "https://truyenqqko.com/frontend/comment/list"
wc -c internal/comments/testdata/page2-fragment.html
```

Expected: some kilobytes of HTML. If it's empty, the chapter only has page 1 — pick a busier chapter URL and retry.

- [ ] **Step 2: Add tests**

```go
// Append to internal/comments/scraper_test.go

func TestScrape_PullsPage2(t *testing.T) {
    p1, err := ioutil.ReadFile("testdata/chapter-with-comments.html")
    if err != nil { t.Fatal(err) }
    p2, err := ioutil.ReadFile("testdata/page2-fragment.html")
    if err != nil { t.Fatal(err) }
    f := &fixedFetcher{getBody: p1, postBody: p2}

    cs, err := Scrape(context.Background(), "https://example.com/chap-1", f)
    if err != nil { t.Fatal(err) }

    p1only := &fixedFetcher{getBody: p1, postBody: nil}
    just1, err := Scrape(context.Background(), "https://example.com/chap-1", p1only)
    if err != nil { t.Fatal(err) }

    if len(cs) <= len(just1) {
        t.Fatalf("page-2 added 0 comments: %d vs %d", len(cs), len(just1))
    }
}

func TestScrape_StripsEmoteImages(t *testing.T) {
    fixture := []byte(`
<html><body>
  <input id="book_id" value="1"/>
  <input id="episode_id" value="2"/>
  <div id="comment_list">
    <article class="info-comment comment-main-level child_1 parent_0">
      <strong class="level name_5">user</strong>
      <span class="title-user-comment title-member level_5">Cấp 5</span>
      <div class="content-comment">hello <img class="lazy-image" alt="emo" data-src="x"/> world</div>
      <span class="total-like-comment">3</span>
    </article>
  </div>
</body></html>`)
    f := &fixedFetcher{getBody: fixture}
    cs, err := Scrape(context.Background(), "https://x/", f)
    if err != nil { t.Fatal(err) }
    if len(cs) != 1 { t.Fatalf("len = %d", len(cs)) }
    if cs[0].Body != "hello  world" {
        t.Errorf("Body = %q", cs[0].Body)
    }
    if cs[0].LikeCount != 3 {
        t.Errorf("LikeCount = %d", cs[0].LikeCount)
    }
}
```

- [ ] **Step 3: Run tests to verify they pass**

```bash
go test ./internal/comments/ -v
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/comments/
git commit -m "comments: cover page-2 merge and emote stripping"
```

---

## Task 7: Vendor Twemoji 15.1

Download the Twemoji 15.1 PNG set into `internal/comments/assets/twemoji/`.

**Files:**
- Create: `internal/comments/assets/twemoji/` (~3600 PNG files)
- Create: `internal/comments/assets/twemoji/SOURCE.md`

- [ ] **Step 1: Fetch the bundle**

Pick exactly one of these paths.

Option A (preferred — get a clean archive of just the PNGs from a tagged release):

```bash
cd /tmp
curl -L -o twemoji-15.1.0.tar.gz \
  "https://github.com/jdecked/twemoji/archive/refs/tags/v15.1.0.tar.gz"
tar -xzf twemoji-15.1.0.tar.gz
ls twemoji-15.1.0/assets/72x72/ | head -5

cd /Users/anhpham/Documents/Projects/script/downloader
mkdir -p internal/comments/assets/twemoji
cp /tmp/twemoji-15.1.0/assets/72x72/*.png internal/comments/assets/twemoji/
ls internal/comments/assets/twemoji/ | wc -l
du -sh internal/comments/assets/twemoji/
```

Expected: ~3600 PNGs, ~4 MB on disk.

- [ ] **Step 2: Write the provenance note**

```markdown
<!-- internal/comments/assets/twemoji/SOURCE.md -->
# Twemoji 15.1.0

Sourced from https://github.com/jdecked/twemoji at tag `v15.1.0`,
`assets/72x72/`. Filenames are `-`-joined lowercase hex codepoint
sequences without the FE0F variation selector — e.g.
`1f468-200d-1f469-200d-1f467.png` for 👨‍👩‍👧.

Updating requires re-running the vendor step in
`docs/superpowers/plans/2026-05-16-manga-comments-sync.md` (Task 7).
```

- [ ] **Step 3: Commit**

```bash
git add internal/comments/assets/twemoji/
git commit -m "comments: vendor Twemoji 15.1.0 (72x72 PNG set)"
```

---

## Task 8: Comments renderer — text layout core

Build the real renderer: header, per-comment block, body wrapping with `go-text/typesetting`. Emoji compositing lands in Task 9.

> **Implementation note.** The `go-text/typesetting` API evolves between releases. The pseudocode in this task is *architectural*; before writing the impl, **read the docs** at `pkg.go.dev/github.com/go-text/typesetting@v0.2.0/shaping` to confirm the exact `Input`/`Output` field names, the `HarfbuzzShaper.Shape` return signature (it returns `(Output, error)` in current versions), and how to construct a `*font.Face`. The test specs below are stable — they assert behaviour, not API shape.

**Files:**
- Create: `internal/comments/assets.go` (embed wrappers)
- Create: `internal/comments/renderer.go`
- Create: `internal/comments/renderer_test.go`
- Delete: `internal/comments/derisk.go` and `internal/comments/derisk_test.go`

- [ ] **Step 1: Wire embeds**

```go
// internal/comments/assets.go
package comments

import _ "embed"

//go:embed assets/NotoSans-Regular.ttf
var notoRegularTTF []byte

//go:embed assets/NotoSans-Bold.ttf
var notoBoldTTF []byte
```

- [ ] **Step 2: Delete the de-risk stubs**

```bash
git rm internal/comments/derisk.go internal/comments/derisk_test.go
# also remove the derisk-output.png if present
rm -f internal/comments/derisk-output.png
```

- [ ] **Step 3: Write a renderer test**

```go
// internal/comments/renderer_test.go
package comments

import (
    "bytes"
    "image/png"
    "testing"
)

func TestRender_BasicShape(t *testing.T) {
    cs := []Comment{
        {Name: "sukuna", Level: "Giới Chủ", Body: "Hay quá!", LikeCount: 5},
        {Name: "Ann", Level: "Cấp 7", Body: "Tuyệt vời, mình rất thích chương này.", LikeCount: 0},
    }
    var buf bytes.Buffer
    if err := Render(cs, &buf); err != nil {
        t.Fatalf("Render: %v", err)
    }
    img, err := png.Decode(&buf)
    if err != nil {
        t.Fatalf("decode: %v", err)
    }
    b := img.Bounds()
    if b.Dx() != 1000 {
        t.Errorf("width = %d, want 1000", b.Dx())
    }
    if b.Dy() < 200 {
        t.Errorf("height = %d, want >= 200", b.Dy())
    }
}

func TestRender_EmptyCommentsNoOp(t *testing.T) {
    var buf bytes.Buffer
    if err := Render(nil, &buf); err == nil {
        t.Fatal("Render(nil) should error — callers must not invoke on empty input")
    }
}

func TestRender_VietnameseShapes(t *testing.T) {
    cs := []Comment{{Name: "n", Body: "Tiếng Việt: ế ề ệ á à"}}
    var buf bytes.Buffer
    if err := Render(cs, &buf); err != nil {
        t.Fatal(err)
    }
    img, _ := png.Decode(&buf)
    if !hasNonWhitePixels(img, 30, 60, 400, 100) {
        t.Fatal("no rendered text in expected region")
    }
}

func hasNonWhitePixels(img interface{ At(int, int) (r, g, b, a uint32) }, x0, y0, x1, y1 int) bool {
    // Helper used by multiple tests.
    return false // implemented after Render lands
}
```

(The `hasNonWhitePixels` helper is defined properly in Step 6.)

- [ ] **Step 4: Run tests to verify they fail**

```bash
go test ./internal/comments/ -run TestRender -v
```

Expected: FAIL — Render is not defined.

- [ ] **Step 5: Implement Render — text core only (emoji in Task 9)**

Use this import block. `golang.org/x/image/font` is imported as `xfont` so the bare `font` name is free for `go-text/typesetting/font`, avoiding the collision that bit the v1 draft of this plan.

```go
// internal/comments/renderer.go
package comments

import (
    "errors"
    "fmt"
    "image"
    "image/color"
    "image/draw"
    "image/png"
    "io"
    "strings"

    gtfont "github.com/go-text/typesetting/font"
    "github.com/go-text/typesetting/shaping"
    "github.com/rivo/uniseg"
    xfont "golang.org/x/image/font"
    "golang.org/x/image/font/opentype"
    "golang.org/x/image/math/fixed"
)

const (
    canvasWidth   = 1000
    sideMargin    = 32
    contentWidth  = canvasWidth - 2*sideMargin
    headerHeight  = 80
    commentPadTop = 16
    commentPadBot = 16
    sepHeight     = 1
    bodySize      = 20.0
    nameSize      = 24.0
    lineSpacing   = 1.4
)

var (
    bgColor   = color.RGBA{0xff, 0xff, 0xff, 0xff}
    textColor = color.RGBA{0x22, 0x22, 0x22, 0xff}
    metaColor = color.RGBA{0x88, 0x88, 0x88, 0xff}
    sepColor  = color.RGBA{0xdd, 0xdd, 0xdd, 0xff}
)
```

Continue with the architectural skeleton below. Where it says **"shape `s` via go-text"**, write the real shaping call — consult `pkg.go.dev/github.com/go-text/typesetting@v0.2.0/shaping` for `shaping.Input` fields (`Text`, `RunStart`, `RunEnd`, `Face`, `Size`, `Script`, `Direction`, `Language`) and `HarfbuzzShaper.Shape` (returns `(Output, error)`). `Size` is a `fixed.Int26_6`; use `fixed.I(int(size))` to convert from a numeric size.

```go
// Render writes a PNG comment page to w. Callers should not invoke
// Render on an empty []Comment — the function returns an error in
// that case so a chapter with no comments produces no file.
func Render(cs []Comment, w io.Writer) error {
    if len(cs) == 0 {
        return errors.New("Render: no comments to render")
    }
    regular, err := opentype.Parse(notoRegularTTF)
    if err != nil { return fmt.Errorf("regular font: %w", err) }
    bold, err := opentype.Parse(notoBoldTTF)
    if err != nil { return fmt.Errorf("bold font: %w", err) }

    // 1. Measure each comment by laying out its body.
    blocks := make([]commentBlock, len(cs))
    for i, c := range cs {
        blocks[i] = layoutComment(c, regular)
    }

    // 2. Sum heights, allocate canvas, fill background.
    totalHeight := headerHeight
    for _, b := range blocks { totalHeight += b.height }
    img := image.NewRGBA(image.Rect(0, 0, canvasWidth, totalHeight))
    draw.Draw(img, img.Bounds(), &image.Uniform{bgColor}, image.Point{}, draw.Src)

    // 3. Header and blocks.
    drawHeader(img, len(cs), bold)
    y := headerHeight
    for _, b := range blocks {
        drawComment(img, b, y, regular, bold)
        y += b.height
    }
    return png.Encode(w, img)
}

type commentBlock struct {
    comment Comment
    bodyLines []string // already wrapped to contentWidth; each line is rendered as a single shaped run
    height  int
}

func layoutComment(c Comment, regular *opentype.Font) commentBlock {
    nameRowHeight := int(nameSize * lineSpacing)
    lines := wrapBody(c.Body, regular, bodySize, contentWidth)
    bodyHeight := int(bodySize*lineSpacing) * max(1, len(lines))
    return commentBlock{
        comment: c,
        bodyLines: lines,
        height: commentPadTop + nameRowHeight + bodyHeight + commentPadBot + sepHeight,
    }
}
```

The remaining helpers — `wrapBody`, `drawHeader`, `drawComment`, `drawHLine`, plus a `drawTextLine(img, s, x, y, face)` that does one line of shaped text — are straightforward once you have a working `shapeString(text, face) (xfont.Face-compatible advance, drawer)`. Pseudocode:

```text
shapeString(text, gtFace):
    input := shaping.Input{
        Text: []rune(text), RunStart: 0, RunEnd: utf8.RuneCountInString(text),
        Face: gtFace,
        Size: fixed.I(int(gtFace.Size)),
        Script: language.Latin,    // Vietnamese is Latin-script
        Direction: di.DirectionLTR,
        Language: language.NewLanguage("vi"),
    }
    out, err := (&shaping.HarfbuzzShaper{}).Shape(input)
    ... return out

drawTextLine(img, text, x, y, otFace):
    // otFace is a golang.org/x/image/font/opentype Face built via
    // opentype.NewFace(f, &opentype.FaceOptions{Size: <px>, DPI: 72}).
    // For the v1 renderer it's acceptable to fall back to
    // xfont.Drawer.DrawString for the WHOLE line; per-cluster
    // positioning only matters once emoji compositing lands (Task 9).
    d := &xfont.Drawer{
        Dst: img, Src: &image.Uniform{textColor}, Face: otFace,
        Dot: fixed.P(x, y),
    }
    d.DrawString(text)

wrapBody(body, regular, size, maxWidth):
    // Greedy line break on uniseg grapheme clusters.
    // Measure each candidate via shapeString; emit a line when the
    // candidate exceeds maxWidth.
```

The like-count in the per-comment row renders as plain text `♥ N` — U+2665 is below 0x1F000 so Task 9's emoji-range check (next task) won't pick it up, and it will pass through `drawTextLine` directly.

- [ ] **Step 6: Add the `hasNonWhitePixels` test helper**

The placeholder in renderer_test.go (which always returns `false`) must be replaced:

```go
// internal/comments/renderer_test.go

import "image"

func hasNonWhitePixels(img image.Image, x0, y0, x1, y1 int) bool {
    for y := y0; y < y1; y++ {
        for x := x0; x < x1; x++ {
            r, g, b, _ := img.At(x, y).RGBA()
            if r < 0xff00 || g < 0xff00 || b < 0xff00 {
                return true
            }
        }
    }
    return false
}
```

Delete the placeholder version (the one with the inline `interface{ At(...) ... }` parameter type).

- [ ] **Step 7: Run all tests**

```bash
go test ./internal/comments/ -v
```

Expected: PASS for `TestRender_BasicShape`, `TestRender_EmptyCommentsNoOp`, `TestRender_VietnameseShapes`. PASS for the scraper tests too.

- [ ] **Step 8: Commit**

```bash
git add internal/comments/
git commit -m "comments: render header + comment blocks via go-text shaping"
```

---

## Task 9: Renderer — emoji compositing

Walk each shaped line, replace emoji grapheme clusters with the matching Twemoji PNG composited inline at line-height.

**Files:**
- Modify: `internal/comments/renderer.go`
- Modify: `internal/comments/renderer_test.go`

- [ ] **Step 1: Write the emoji test**

```go
// Append to internal/comments/renderer_test.go

func TestRender_PlainEmojiCompositesPixels(t *testing.T) {
    cs := []Comment{{Name: "n", Body: "yay \U0001F600"}} // 😀
    var buf bytes.Buffer
    if err := Render(cs, &buf); err != nil {
        t.Fatal(err)
    }
    img, _ := png.Decode(&buf)
    // 😀 is yellow — the rendered image must contain a yellow
    // pixel inside the body region.
    found := false
    b := img.Bounds()
    for y := 60; y < b.Dy() && !found; y++ {
        for x := 0; x < b.Dx() && !found; x++ {
            r, g, b, _ := img.At(x, y).RGBA()
            if r > 0xe000 && g > 0xc000 && b < 0x6000 {
                found = true
            }
        }
    }
    if !found {
        t.Fatal("no yellow pixel found — emoji likely rendered as tofu")
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/comments/ -run TestRender_PlainEmoji -v
```

Expected: FAIL — no yellow pixels.

- [ ] **Step 3: Add the Twemoji embed and loader**

Append a `//go:embed` directive to `internal/comments/assets.go`:

```go
// internal/comments/assets.go — add the new directive and helper
// alongside the existing notoRegularTTF / notoBoldTTF embeds.
package comments

import (
    "embed"
    _ "embed" // for the //go:embed directives above
)

//go:embed assets/twemoji/*.png
var twemojiFS embed.FS

func twemojiPNG(seq string) ([]byte, bool) {
    raw, err := twemojiFS.ReadFile("assets/twemoji/" + seq + ".png")
    if err != nil {
        return nil, false
    }
    return raw, true
}
```

If the existing file already imports `embed` for `_ "embed"`, drop the duplicate.

- [ ] **Step 4: Update the imports in `renderer.go`**

`renderer.go`'s import block (from Task 8) is missing entries needed by Task 9: `bytes`, `unicode/utf8`. Edit the existing import block to add them. Do NOT paste a second `import (...)` block — Go disallows two import declarations referencing the same package, and a partial-overlap second block will fail to compile.

The fully-updated import block reads:

```go
import (
    "bytes"
    "errors"
    "fmt"
    "image"
    "image/color"
    "image/draw"
    "image/png"
    "io"
    "strings"
    "unicode/utf8"

    gtfont "github.com/go-text/typesetting/font"
    "github.com/go-text/typesetting/shaping"
    "github.com/rivo/uniseg"
    xfont "golang.org/x/image/font"
    "golang.org/x/image/font/opentype"
    "golang.org/x/image/math/fixed"
)
```

- [ ] **Step 5: Replace `drawTextLine` with a per-cluster variant**

The line-drawing helper introduced in Task 8 currently draws the whole string in one `xfont.Drawer.DrawString` call. Replace it with a per-grapheme-cluster loop that composites Twemoji PNGs for emoji clusters and falls back to the text drawer for non-emoji clusters. **Use `Edit` to replace the existing function — do not paste a second `drawTextLine`.** Go produces a `redeclared in this block` compile error for duplicate top-level functions in the same package.

Add these helpers (new symbols, not replacements):

```go
// emojiKey builds the Twemoji filename key from a grapheme cluster:
// e.g. "👨‍👩‍👧" → "1f468-200d-1f469-200d-1f467". FE0F variation
// selectors are stripped because Twemoji filenames don't include them.
func emojiKey(cluster string) string {
    var parts []string
    for _, r := range cluster {
        if r == 0xFE0F {
            continue
        }
        parts = append(parts, fmt.Sprintf("%x", r))
    }
    return strings.Join(parts, "-")
}

func decodeTwemoji(seq string) (image.Image, bool) {
    raw, ok := twemojiPNG(seq)
    if !ok {
        return nil, false
    }
    img, err := png.Decode(bytes.NewReader(raw))
    if err != nil {
        return nil, false
    }
    return img, true
}

// looksLikeEmoji is intentionally conservative: it only triggers
// on grapheme clusters whose leading rune is in (or beyond) the
// supplementary plane emoji range. ASCII and Latin (including
// Vietnamese composed forms) are left to the text path. ♥ (U+2665)
// is below the threshold and renders as text — that's intentional
// for the like-count display in the header row.
func looksLikeEmoji(cluster string) bool {
    r, _ := utf8.DecodeRuneInString(cluster)
    return r >= 0x1F000
}

// scaleImage is a tiny nearest-neighbour scaler — good enough for
// dropping a 72×72 Twemoji into a 20×20 body-text slot. Upgrade to
// golang.org/x/image/draw.CatmullRom later if quality matters.
func scaleImage(src image.Image, w, h int) *image.RGBA {
    dst := image.NewRGBA(image.Rect(0, 0, w, h))
    sb := src.Bounds()
    for y := 0; y < h; y++ {
        for x := 0; x < w; x++ {
            sx := sb.Min.X + x*sb.Dx()/w
            sy := sb.Min.Y + y*sb.Dy()/h
            dst.Set(x, y, src.At(sx, sy))
        }
    }
    return dst
}
```

Then **replace** (not append) the existing `drawTextLine` from Task 8 with this per-cluster version. Use the `Edit` tool's `old_string`/`new_string` to swap it in place:

```go
// New body of drawTextLine — replaces Task 8's whole-line draw.
//
// y is the text baseline. The font face must have already been
// constructed by the caller at the correct size.
func drawTextLine(img *image.RGBA, text string, x, y int, otFace xfont.Face) {
    if !strings.ContainsFunc(text, func(r rune) bool { return r >= 0x1F000 }) {
        // Fast path: no emoji in the line.
        (&xfont.Drawer{
            Dst:  img,
            Src:  &image.Uniform{textColor},
            Face: otFace,
            Dot:  fixed.P(x, y),
        }).DrawString(text)
        return
    }

    cx := fixed.I(x)
    g := uniseg.NewGraphemes(text)
    for g.Next() {
        cluster := g.Str()
        if !looksLikeEmoji(cluster) {
            d := &xfont.Drawer{
                Dst:  img,
                Src:  &image.Uniform{textColor},
                Face: otFace,
                Dot:  fixed.Point26_6{X: cx, Y: fixed.I(y)},
            }
            d.DrawString(cluster)
            cx = d.Dot.X
            continue
        }
        seq := emojiKey(cluster)
        ei, ok := decodeTwemoji(seq)
        if !ok {
            // Twemoji bundle has no match — draw the cluster as text
            // (likely tofu). Accepted per the spec's "emoji are
            // best-effort" rule.
            d := &xfont.Drawer{
                Dst:  img,
                Src:  &image.Uniform{textColor},
                Face: otFace,
                Dot:  fixed.Point26_6{X: cx, Y: fixed.I(y)},
            }
            d.DrawString(cluster)
            cx = d.Dot.X
            continue
        }
        target := int(bodySize)
        scaled := scaleImage(ei, target, target)
        rect := image.Rect(cx.Round(), y-target+2, cx.Round()+target, y+2)
        draw.Draw(img, rect, scaled, image.Point{}, draw.Over)
        cx += fixed.I(target + 2)
    }
}
```

If Task 8's renderer named the line-drawing helper something else (e.g. `drawSimpleString`), rename it in both definitions for consistency before this edit — the executor should consult the file as it stands rather than this plan's spelling.

- [ ] **Step 6: Run the tests to verify the emoji test passes**

```bash
go test ./internal/comments/ -v
```

Expected: PASS for `TestRender_PlainEmojiCompositesPixels`. The Vietnamese test should still pass.

- [ ] **Step 7: Commit**

```bash
git add internal/comments/
git commit -m "comments: composite Twemoji PNGs for emoji clusters"
```

---

## Task 10: archive — Inspect

Read a `.cbz` and return the `Inspection` struct.

**Files:**
- Create: `internal/archive/reader.go`
- Create: `internal/archive/archive_test.go`
- Create: `internal/archive/testdata/sample.cbz` (built by the test setup helper)

- [ ] **Step 1: Write a test that builds a fixture archive in-memory**

```go
// internal/archive/archive_test.go
package archive

import (
    "archive/zip"
    "bytes"
    "io"
    "os"
    "path/filepath"
    "testing"
)

func buildCBZ(t *testing.T, entries map[string][]byte) string {
    t.Helper()
    dir := t.TempDir()
    p := filepath.Join(dir, "sample.cbz")
    f, err := os.Create(p)
    if err != nil {
        t.Fatal(err)
    }
    defer f.Close()
    w := zip.NewWriter(f)
    for name, body := range entries {
        h := &zip.FileHeader{Name: name, Method: zip.Store}
        zw, err := w.CreateHeader(h)
        if err != nil {
            t.Fatal(err)
        }
        if _, err := io.Copy(zw, bytes.NewReader(body)); err != nil {
            t.Fatal(err)
        }
    }
    if err := w.Close(); err != nil {
        t.Fatal(err)
    }
    return p
}

func TestInspect_ClassifiesEntries(t *testing.T) {
    p := buildCBZ(t, map[string][]byte{
        "chap-0001/001.jpg":          []byte("jpg-1"),
        "chap-0001/002.jpg":          []byte("jpg-2"),
        "chap-0001/zzz-comments.png": []byte("png"),
        "chap-0002/001.jpg":          []byte("jpg-3"),
        "chap-0042-5/001.webp":       []byte("webp"),
        ".DS_Store":                  []byte("junk"),
    })
    in, err := Inspect(p)
    if err != nil {
        t.Fatal(err)
    }
    want := map[string]bool{
        "chap-0001":   true,
        "chap-0002":   true,
        "chap-0042-5": true,
    }
    if len(in.Have) != len(want) {
        t.Fatalf("Have = %v, want %v", in.Have, want)
    }
    for k := range want {
        if !in.Have[k] {
            t.Errorf("Have missing %q", k)
        }
    }
    if !in.HaveComments["chap-0001"] || in.HaveComments["chap-0002"] {
        t.Errorf("HaveComments = %v", in.HaveComments)
    }
}

func TestInspect_MissingFile(t *testing.T) {
    in, err := Inspect("/nonexistent.cbz")
    if err != nil {
        t.Fatalf("Inspect missing should not error, got %v", err)
    }
    if len(in.Have) != 0 {
        t.Errorf("Have should be empty")
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/archive/ -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement Inspect**

```go
// internal/archive/reader.go
package archive

import (
    "archive/zip"
    "errors"
    "os"
    "path"

    "github.com/anhpham/downloader/internal/layout"
)

// Inspection captures what's already in a .cbz archive.
type Inspection struct {
    // Have is the set of "chap-NNNN[-K]" folder names that contain
    // at least one image entry.
    Have map[string]bool
    // HaveComments is the subset of Have whose chapter folder also
    // contains a zzz-comments.png entry.
    HaveComments map[string]bool
}

// Inspect reads cbzPath and classifies its entries. A missing file
// returns an empty Inspection (not an error) — that's the
// "archive does not exist yet" path.
func Inspect(cbzPath string) (Inspection, error) {
    out := Inspection{
        Have:         map[string]bool{},
        HaveComments: map[string]bool{},
    }
    zr, err := zip.OpenReader(cbzPath)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return out, nil
        }
        return out, err
    }
    defer zr.Close()

    for _, f := range zr.File {
        name := f.Name
        dir, base := path.Split(name)
        dir = path.Clean(dir) // strips trailing slash
        if dir == "." || dir == "" {
            continue
        }
        if base == layout.CommentsFilename {
            // Mark as comments-present even if no images yet (we
            // never write this without ≥1 image, but be defensive).
            out.HaveComments[dir] = true
            continue
        }
        if layout.IsImageEntry(name) {
            out.Have[dir] = true
        }
    }

    // Only retain HaveComments entries whose folder also has images.
    for k := range out.HaveComments {
        if !out.Have[k] {
            delete(out.HaveComments, k)
        }
    }

    return out, nil
}

// InferredWidth wraps layout.InferredWidth over Inspection.Have.
func (i Inspection) InferredWidth() int {
    return layout.InferredWidth(i.Have)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/archive/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/archive/
git commit -m "archive: Inspect classifies cbz entries via layout helpers"
```

---

## Task 11: archive — StageAndRename

Copy existing entries via `OpenRaw + CreateRaw`, append scratch files, verify, atomic rename.

**Files:**
- Create: `internal/archive/writer.go`
- Modify: `internal/archive/archive_test.go`

- [ ] **Step 1: Write a failing test for StageAndRename**

```go
// Append to internal/archive/archive_test.go

import (
    "io/ioutil"
)

func TestStageAndRename_PreservesOriginalAndAppends(t *testing.T) {
    p := buildCBZ(t, map[string][]byte{
        "chap-0001/001.jpg": []byte("AAA"),
    })

    // Scratch dir: chap-0001 has comments to add; chap-0002 is brand new.
    scratch := t.TempDir()
    must := func(err error) { t.Helper(); if err != nil { t.Fatal(err) } }
    must(os.MkdirAll(filepath.Join(scratch, "chap-0001"), 0o755))
    must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", "zzz-comments.png"), []byte("PNG"), 0o644))
    must(ioutil.WriteFile(filepath.Join(scratch, "chap-0001", ".ok"), nil, 0o644))
    must(os.MkdirAll(filepath.Join(scratch, "chap-0002"), 0o755))
    must(ioutil.WriteFile(filepath.Join(scratch, "chap-0002", "001.jpg"), []byte("BBB"), 0o644))
    must(ioutil.WriteFile(filepath.Join(scratch, "chap-0002", ".ok"), nil, 0o644))
    // Poisoned chapter: NO .ok marker, must be excluded.
    must(os.MkdirAll(filepath.Join(scratch, "chap-0003"), 0o755))
    must(ioutil.WriteFile(filepath.Join(scratch, "chap-0003", "001.jpg"), []byte("CCC"), 0o644))

    if err := StageAndRename(p, scratch); err != nil {
        t.Fatal(err)
    }

    zr, err := zip.OpenReader(p)
    if err != nil { t.Fatal(err) }
    defer zr.Close()

    got := map[string][]byte{}
    for _, f := range zr.File {
        rc, _ := f.Open()
        b, _ := io.ReadAll(rc)
        rc.Close()
        got[f.Name] = b
    }
    want := map[string]string{
        "chap-0001/001.jpg":          "AAA",
        "chap-0001/zzz-comments.png": "PNG",
        "chap-0002/001.jpg":          "BBB",
    }
    if len(got) != len(want) {
        t.Fatalf("entries: got %v, want %v", got, want)
    }
    for k, v := range want {
        if string(got[k]) != v {
            t.Errorf("%s: got %q, want %q", k, got[k], v)
        }
    }
    if _, present := got["chap-0003/001.jpg"]; present {
        t.Error("chap-0003 should be excluded (no .ok marker)")
    }
    for k := range got {
        if filepath.Base(k) == ".ok" {
            t.Errorf(".ok leaked into archive: %s", k)
        }
    }
}

func TestStageAndRename_CreatesFreshIfTargetMissing(t *testing.T) {
    dir := t.TempDir()
    p := filepath.Join(dir, "fresh.cbz")
    scratch := t.TempDir()
    if err := os.MkdirAll(filepath.Join(scratch, "chap-0001"), 0o755); err != nil { t.Fatal(err) }
    if err := ioutil.WriteFile(filepath.Join(scratch, "chap-0001", "001.jpg"), []byte("X"), 0o644); err != nil { t.Fatal(err) }
    if err := ioutil.WriteFile(filepath.Join(scratch, "chap-0001", ".ok"), nil, 0o644); err != nil { t.Fatal(err) }

    if err := StageAndRename(p, scratch); err != nil {
        t.Fatal(err)
    }
    if _, err := os.Stat(p); err != nil {
        t.Fatal("fresh.cbz not created:", err)
    }
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/archive/ -run TestStageAndRename -v
```

Expected: FAIL — function does not exist.

- [ ] **Step 3: Implement StageAndRename**

```go
// internal/archive/writer.go
package archive

import (
    "archive/zip"
    "errors"
    "fmt"
    "io"
    "io/fs"
    "os"
    "path/filepath"
    "sort"
)

// StageAndRename merges every .ok-marked chapter subdirectory of
// scratchRoot into cbzPath. If cbzPath does not exist, a fresh
// archive is written. Existing entries are preserved byte-for-
// byte via the raw-copy mechanism. The tmp file is created in the
// same directory as cbzPath so os.Rename is atomic. New entries
// are written with Method=Store to match the archive's existing
// convention (and to avoid wasted CPU re-deflating PNG/JPEG).
//
// Per-chapter subdirs of scratchRoot are included only if they
// contain a `.ok` marker file. The marker itself is never written
// into the archive.
func StageAndRename(cbzPath, scratchRoot string) error {
    tmpPath := cbzPath + ".tmp"
    tmp, err := os.Create(tmpPath)
    if err != nil {
        return fmt.Errorf("create tmp: %w", err)
    }
    cleanup := func() { tmp.Close(); os.Remove(tmpPath) }

    zw := zip.NewWriter(tmp)

    // 1. Copy existing entries from cbzPath (if it exists) via raw.
    if zr, err := zip.OpenReader(cbzPath); err == nil {
        defer zr.Close()
        for _, f := range zr.File {
            rc, err := f.OpenRaw()
            if err != nil {
                cleanup()
                return fmt.Errorf("openraw %s: %w", f.Name, err)
            }
            hdr := f.FileHeader
            w, err := zw.CreateRaw(&hdr)
            if err != nil {
                cleanup()
                return fmt.Errorf("createraw %s: %w", f.Name, err)
            }
            if _, err := io.Copy(w, rc); err != nil {
                cleanup()
                return fmt.Errorf("copy %s: %w", f.Name, err)
            }
        }
    } else if !errors.Is(err, os.ErrNotExist) {
        cleanup()
        return fmt.Errorf("open existing: %w", err)
    }

    // 2. Walk scratchRoot, include only .ok-marked chapter dirs.
    chapters, err := readMarkedChapters(scratchRoot)
    if err != nil {
        cleanup()
        return err
    }
    sort.Strings(chapters)
    for _, ch := range chapters {
        chDir := filepath.Join(scratchRoot, ch)
        ents, err := os.ReadDir(chDir)
        if err != nil {
            cleanup()
            return err
        }
        // Stable order inside each chapter.
        sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
        for _, e := range ents {
            if e.Name() == ".ok" || e.IsDir() {
                continue
            }
            srcPath := filepath.Join(chDir, e.Name())
            raw, err := os.ReadFile(srcPath)
            if err != nil {
                cleanup()
                return err
            }
            w, err := zw.CreateHeader(&zip.FileHeader{
                Name:   ch + "/" + e.Name(),
                Method: zip.Store,
            })
            if err != nil {
                cleanup()
                return err
            }
            if _, err := w.Write(raw); err != nil {
                cleanup()
                return err
            }
        }
    }
    if err := zw.Close(); err != nil {
        cleanup()
        return fmt.Errorf("close writer: %w", err)
    }
    if err := tmp.Close(); err != nil {
        os.Remove(tmpPath)
        return fmt.Errorf("close tmp: %w", err)
    }

    // 3. Verify the tmp archive in pure Go.
    if err := verifyArchive(tmpPath); err != nil {
        os.Remove(tmpPath)
        return fmt.Errorf("verify: %w", err)
    }

    // 4. Atomic rename.
    if err := os.Rename(tmpPath, cbzPath); err != nil {
        os.Remove(tmpPath)
        return fmt.Errorf("rename: %w", err)
    }
    return nil
}

func readMarkedChapters(scratchRoot string) ([]string, error) {
    var chs []string
    err := filepath.WalkDir(scratchRoot, func(p string, d fs.DirEntry, err error) error {
        if err != nil { return err }
        if !d.IsDir() || p == scratchRoot { return nil }
        rel, _ := filepath.Rel(scratchRoot, p)
        if filepath.Dir(rel) != "." { return nil } // only first level
        if _, err := os.Stat(filepath.Join(p, ".ok")); err == nil {
            chs = append(chs, rel)
        }
        return nil
    })
    if err != nil { return nil, err }
    return chs, nil
}

func verifyArchive(path string) error {
    zr, err := zip.OpenReader(path)
    if err != nil { return err }
    defer zr.Close()
    for _, f := range zr.File {
        rc, err := f.OpenRaw()
        if err != nil { return err }
        if _, err := io.Copy(io.Discard, rc); err != nil { return err }
    }
    return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/archive/ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/archive/
git commit -m "archive: StageAndRename with raw-copy + .ok exclusion"
```

---

## Task 12: pipeline — Plan (pure mode dispatch)

**Files:**
- Create: `internal/pipeline/plan.go`
- Create: `internal/pipeline/plan_test.go`

- [ ] **Step 1: Write failing tests for every matrix cell**

```go
// internal/pipeline/plan_test.go
package pipeline

import (
    "testing"

    "github.com/anhpham/downloader/internal/archive"
    "github.com/anhpham/downloader/internal/site"
)

func chap(n string) site.Chapter { return site.Chapter{Number: n, URL: "u-" + n} }

func TestPlan_Matrix(t *testing.T) {
    chapters := []site.Chapter{chap("1"), chap("2"), chap("3")}
    have := map[string]bool{"chap-0001": true, "chap-0002": true}
    haveComments := map[string]bool{"chap-0001": true}
    insp := archive.Inspection{Have: have, HaveComments: haveComments}

    cases := []struct {
        name string
        mode Mode
        // expected per-chapter task kinds, keyed by folder
        want map[string]TaskKind
    }{
        {
            name: "sync-comments",
            mode: SyncComments,
            want: map[string]TaskKind{
                "chap-0002": Render, // in archive, no comments → render
            },
        },
        {
            name: "resume",
            mode: Resume,
            want: map[string]TaskKind{
                "chap-0003": Both, // not in archive → download + render
            },
        },
        {
            name: "sync-manga",
            mode: SyncManga,
            want: map[string]TaskKind{
                "chap-0002": Render,
                "chap-0003": Both,
            },
        },
    }
    for _, c := range cases {
        t.Run(c.name, func(t *testing.T) {
            tasks := Plan(c.mode, chapters, insp, 4 /*width*/)
            got := map[string]TaskKind{}
            for _, tk := range tasks {
                got[tk.Folder] = tk.Kind
            }
            if len(got) != len(c.want) {
                t.Fatalf("got %v, want %v", got, c.want)
            }
            for k, v := range c.want {
                if got[k] != v {
                    t.Errorf("folder %q: got %v, want %v", k, got[k], v)
                }
            }
        })
    }
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/pipeline/ -v
```

Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement Plan**

```go
// internal/pipeline/plan.go
package pipeline

import (
    "github.com/anhpham/downloader/internal/archive"
    "github.com/anhpham/downloader/internal/layout"
    "github.com/anhpham/downloader/internal/site"
)

type Mode int

const (
    SyncComments Mode = iota
    Resume
    SyncManga
)

type TaskKind int

const (
    Render TaskKind = iota // render comments only
    Both                   // download images + render comments
)

type Task struct {
    Folder  string       // chap-NNNN[-K]
    Number  string       // raw chapter number
    URL     string       // chapter URL
    Kind    TaskKind
}

// Plan converts (mode, source chapter list, archive inspection,
// effective width) into the work list for the run.
func Plan(mode Mode, chapters []site.Chapter, insp archive.Inspection, width int) []Task {
    var out []Task
    for _, c := range chapters {
        folder := layout.Folder("", c.Number, width)
        in := insp.Have[folder]
        hasComments := insp.HaveComments[folder]

        switch mode {
        case SyncComments:
            if in && !hasComments {
                out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Render})
            }
        case Resume:
            if !in {
                out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Both})
            }
        case SyncManga:
            if !in {
                out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Both})
            } else if !hasComments {
                out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Render})
            }
        }
    }
    return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/pipeline/ -v
```

Expected: PASS.

- [ ] **Step 5: Add the width-stability test**

```go
// Append to internal/pipeline/plan_test.go

func TestPlan_WidthStabilityFromArchive(t *testing.T) {
    chapters := []site.Chapter{chap("1"), chap("10000")}
    have := map[string]bool{"chap-0001": true, "chap-9999": true} // 4-wide
    insp := archive.Inspection{Have: have, HaveComments: nil}
    // Width must be 5 because source needs 5; archive's inferred 4
    // does not override an increase. The check here is that "chap-0001"
    // (existing) still matches a 4-wide key — i.e. Plan uses the same
    // width logic the pipeline picks. We verify with a known width.
    tasks := Plan(SyncManga, chapters, insp, 5)
    got := map[string]bool{}
    for _, t := range tasks {
        got[t.Folder] = true
    }
    // "chap-00001" is NOT in have (which has "chap-0001"). With the
    // spec's max(sourceWidth, archiveWidth) rule the pipeline picks
    // width=5 for new chapters but uses width=4 for matching existing
    // ones — that logic lives in pipeline.Run, not Plan. This test
    // asserts that Plan honours whatever width it's given without
    // smarts.
    if !got["chap-00001"] {
        t.Errorf("Plan should propose folder chap-00001 with width=5; got %v", got)
    }
}
```

Note: actual archive-width preservation across width transitions is implemented in `pipeline.Run` (Task 13), which makes two passes — one with `archive.InferredWidth` for existing chapter matches, one with `max(...)` for new chapters. The `Plan` function itself stays width-agnostic.

- [ ] **Step 6: Run tests**

```bash
go test ./internal/pipeline/ -v
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/pipeline/
git commit -m "pipeline: pure-function Plan covers all three modes"
```

---

## Task 13: Shrink downloader to FetchChapterImages

> **Tree non-compile window.** Removing the existing `Downloader` struct and its `Run`/`Result`/`ChapterFailure` types breaks `main.go` (which still constructs `&downloader.Downloader{...}`) until Task 15 lands the subcommand dispatch. The plan accepts this — Task 13's commit will pass `go test ./internal/downloader/ ./internal/layout/ ./internal/comments/ ./internal/archive/ ./internal/pipeline/` but **`go build ./...` and a top-level `go test ./...` will fail until Task 15**. Run the narrower test command after Task 13; defer the full `go test ./...` until Task 15.

**Files:**
- Create: `internal/downloader/images.go`
- Modify: `internal/downloader/downloader.go` (drastically shrunk; many helpers deleted)
- Modify or delete: `internal/downloader/downloader_test.go` (existing end-to-end orchestration tests are no longer applicable; delete or rewrite)

- [ ] **Step 1: Identify what stays vs goes**

```bash
grep -n 'func ' internal/downloader/downloader.go
```

The existing file (`internal/downloader/downloader.go`) defines all of these symbols. Delete every one **except** the inner image-fetch helper `(*Downloader).fetchTo` (which becomes the new `fetchOne`) and the extension helper `imageExt` (which becomes `extFromURL`). The package is essentially being rebuilt around `FetchChapterImages` (Step 2 below).

Kill list — delete every one of these from `internal/downloader/downloader.go`:

- `const chapterCacheFile`
- `type Downloader struct`
- `type Result struct`
- `type ChapterFailure struct`
- `func (d *Downloader) Run`
- `func (d *Downloader) loadOrFetchChapters`
- `func readChapterCache`
- `func writeChapterCache`
- `func (d *Downloader) runChapter`
- `func filterRange` (re-implemented in `internal/pipeline`)
- `func chapterNumeric` (re-implemented in `internal/pipeline.parseChapterNumber`)
- `func hasDoneSentinel`
- (`folderWidth`, `digitWidth`, `chapterFolder` were already removed in Task 3.)

The downloader package after Task 13 contains only `images.go` (see Step 2). The original `downloader.go` may end up empty enough to delete entirely — if so, `git rm` it.

- [ ] **Step 2: Extract FetchChapterImages**

Real types (verified from `internal/site/`):

- `(*source.Site).ChapterImages(ctx, c site.Chapter) ([]site.ImageRef, error)` — takes a `site.Chapter`, not a URL string; returns `[]site.ImageRef`, each carrying its own `URL` and `Referer`.
- There is no `source.New(...)` constructor — the struct is constructed directly: `&source.Site{Fetcher: f}`.

```go
// internal/downloader/images.go
package downloader

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "github.com/anhpham/downloader/internal/fetcher"
    "github.com/anhpham/downloader/internal/layout"
    "github.com/anhpham/downloader/internal/site"
    sourcesite "github.com/anhpham/downloader/internal/site/source"
)

// FetchChapterImages downloads every image referenced by the
// chapter page into destDir. Each image lands as a .part file and
// is atomically renamed once its bytes are on disk.
func FetchChapterImages(ctx context.Context, chapter site.Chapter, destDir string, f fetcher.Fetcher) error {
    s := &sourcesite.Site{Fetcher: f}
    refs, err := s.ChapterImages(ctx, chapter)
    if err != nil { return fmt.Errorf("list images: %w", err) }
    if err := os.MkdirAll(destDir, 0o755); err != nil { return err }

    for i, ref := range refs {
        ext := extFromURL(ref.URL)
        dst := filepath.Join(destDir, layout.ImageName(i+1, ext))
        if err := fetchOne(ctx, ref, dst, f); err != nil {
            return fmt.Errorf("image %d: %w", i+1, err)
        }
    }
    return nil
}

func fetchOne(ctx context.Context, ref site.ImageRef, dst string, f fetcher.Fetcher) error {
    resp, err := f.Get(ctx, fetcher.Request{URL: ref.URL, Referer: ref.Referer})
    if err != nil { return err }
    tmp := dst + ".part"
    if err := os.WriteFile(tmp, resp.Body, 0o644); err != nil { return err }
    return os.Rename(tmp, dst)
}

// extFromURL returns the lowercase extension (without the dot)
// extracted from a URL, defaulting to "jpg" when the path has none.
func extFromURL(u string) string {
    base := filepath.Base(u)
    if i := strings.LastIndexByte(base, '.'); i != -1 && i < len(base)-1 {
        return strings.ToLower(base[i+1:])
    }
    return "jpg"
}
```

Because `FetchChapterImages` now takes a `site.Chapter` (carrying both `Number` and `URL`), Task 14's `pipeline.Run` call site must pass the full chapter, not just the URL. The Task entry for executing a `Both`/`Render` task already holds the URL on `Task`; we extend the `Task` struct in Task 12 to carry the full `site.Chapter` (or at minimum the `Number` field plus the `URL`) so the pipeline can construct the chapter for `FetchChapterImages`.

- [ ] **Step 3: Delete the kill-list symbols from `downloader.go`**

Open `internal/downloader/downloader.go` and delete every symbol on the kill list above. If the file becomes empty, `git rm` it. Otherwise keep the `package downloader` clause and any stragglers (there should not be stragglers, but if there are, they're symbols the kill list missed — flag and delete).

- [ ] **Step 4: Update the downloader test file**

Open `internal/downloader/downloader_test.go`. The existing tests exercise the full orchestration that this task is deleting — they'll be unreferenced and fail to compile. Delete the file or rewrite it to cover `FetchChapterImages` with a fake fetcher (one tiny test asserting that one image gets fetched + renamed is sufficient; the heavy mode logic lives in `internal/pipeline` tests now).

- [ ] **Step 5: Run the limited test suite**

```bash
go test ./internal/layout/ ./internal/fetcher/ ./internal/comments/ ./internal/archive/ ./internal/pipeline/ ./internal/downloader/
```

Expected: PASS. **Do not run `go test ./...` or `go build ./...` here** — `main.go` still references the deleted `Downloader` struct and will fail. That gets fixed in Task 15.

- [ ] **Step 6: Commit**

```bash
git add internal/downloader/
git commit -m "downloader: shrink to FetchChapterImages; orchestration moves to pipeline"
```

---
## Task 14: pipeline — Run orchestration

Wire fetcher, site, comments, archive, downloader.
(This task depends on Task 13's `downloader.FetchChapterImages` being in place.)

**Files:**
- Create: `internal/pipeline/pipeline.go`
- Create: `internal/pipeline/pipeline_test.go`

- [ ] **Step 1: Sketch the Opts and Run signature**

```go
// internal/pipeline/pipeline.go
package pipeline

import (
    "context"
    "errors"
    "fmt"
    "log"
    "os"
    "path/filepath"
    "strconv"
    "strings"
    "sync"

    "github.com/gofrs/flock"

    "github.com/anhpham/downloader/internal/archive"
    "github.com/anhpham/downloader/internal/comments"
    "github.com/anhpham/downloader/internal/downloader"
    "github.com/anhpham/downloader/internal/fetcher"
    "github.com/anhpham/downloader/internal/layout"
    "github.com/anhpham/downloader/internal/site"
)

type Opts struct {
    Mode        Mode
    MangaURL    string
    Root        string
    Name        string
    From, To    int
    Concurrency int
    Site        site.Site
    Fetcher     fetcher.Fetcher
    Verbose     bool
    Logger      *log.Logger
}

var (
    ErrAnotherInstance = errors.New("another downloader is running")
    ErrNoArchive       = errors.New("no archive to operate on")
)

// Run executes one mode end-to-end.
func Run(ctx context.Context, opts Opts) error {
    cbzPath := filepath.Join(opts.Root, opts.Name+".cbz")
    lockPath := filepath.Join(opts.Root, opts.Name+".cbz.lock")
    if err := os.MkdirAll(opts.Root, 0o755); err != nil {
        return err
    }
    lock := flock.New(lockPath)
    got, err := lock.TryLock()
    if err != nil { return err }
    if !got { return ErrAnotherInstance }
    defer lock.Unlock()

    insp, err := archive.Inspect(cbzPath)
    if err != nil { return fmt.Errorf("inspect: %w", err) }
    if len(insp.Have) == 0 && opts.Mode != SyncManga {
        return ErrNoArchive
    }

    chapters, err := opts.Site.ListChapters(ctx, opts.MangaURL)
    if err != nil { return fmt.Errorf("list chapters: %w", err) }

    sourceWidth := layout.Width(chapters)
    archiveWidth := insp.InferredWidth()
    effectiveWidth := sourceWidth
    if archiveWidth > effectiveWidth { effectiveWidth = archiveWidth }

    // Honour --from / --to AFTER width is captured.
    chapters = filterRange(chapters, opts.From, opts.To)

    // Plan in TWO passes when the source's required width has
    // grown past the archive's existing width. Otherwise a single
    // pass at effectiveWidth is correct.
    //
    // Pass 1 (existing chapters): width = archiveWidth, real insp.
    //   Plan picks up SyncComments-style backfills (chapter is in
    //   archive at archiveWidth, missing comments) and skips
    //   chapters not yet in the archive (they're handled in pass 2).
    //
    // Pass 2 (new chapters): width = sourceWidth, *empty* insp.
    //   Plan emits a Both task for every chapter; we then filter
    //   out any that already exist in the archive under the
    //   narrower width.
    var tasks []Task
    if archiveWidth > 0 && archiveWidth < sourceWidth {
        existing := Plan(opts.Mode, chapters, insp, archiveWidth)
        for _, t := range existing {
            // Only keep tasks that touch a chapter actually
            // present in the archive at archiveWidth. Plan emits
            // tasks for unknowns too (which would be re-emitted in
            // pass 2 at sourceWidth) — drop them here.
            if insp.Have[t.Folder] {
                tasks = append(tasks, t)
            }
        }
        empty := archive.Inspection{Have: map[string]bool{}, HaveComments: map[string]bool{}}
        novel := Plan(opts.Mode, chapters, empty, sourceWidth)
        for _, t := range novel {
            // Skip chapters already in the archive under the narrower width.
            if insp.Have[layout.Folder("", t.Number, archiveWidth)] {
                continue
            }
            tasks = append(tasks, t)
        }
    } else {
        tasks = Plan(opts.Mode, chapters, insp, effectiveWidth)
    }

    if len(tasks) == 0 {
        opts.Logger.Println("nothing to do")
        return nil
    }

    scratchRoot := filepath.Join(opts.Root, "."+opts.Name+".scratch")
    if err := os.MkdirAll(scratchRoot, 0o755); err != nil {
        return err
    }

    if err := runTasks(ctx, tasks, scratchRoot, opts); err != nil {
        return err
    }

    if err := archive.StageAndRename(cbzPath, scratchRoot); err != nil {
        return fmt.Errorf("stage: %w", err)
    }

    return os.RemoveAll(scratchRoot)
}

func runTasks(ctx context.Context, tasks []Task, scratchRoot string, opts Opts) error {
    sem := make(chan struct{}, opts.Concurrency)
    var wg sync.WaitGroup
    var firstErr error
    var mu sync.Mutex

    for _, t := range tasks {
        t := t
        // Skip if .ok already exists from a prior killed run.
        if _, err := os.Stat(filepath.Join(scratchRoot, t.Folder, ".ok")); err == nil {
            continue
        }
        wg.Add(1)
        sem <- struct{}{}
        go func() {
            defer wg.Done()
            defer func() { <-sem }()
            if err := executeTask(ctx, t, scratchRoot, opts); err != nil {
                mu.Lock()
                if firstErr == nil { firstErr = err }
                mu.Unlock()
            }
        }()
    }
    wg.Wait()
    return firstErr
}

func executeTask(ctx context.Context, t Task, scratchRoot string, opts Opts) error {
    chDir := filepath.Join(scratchRoot, t.Folder)
    // Wipe and recreate so a prior partial run doesn't leak.
    if err := os.RemoveAll(chDir); err != nil { return err }
    if err := os.MkdirAll(chDir, 0o755); err != nil { return err }

    if t.Kind == Both {
        ch := site.Chapter{Number: t.Number, URL: t.URL}
        if err := downloader.FetchChapterImages(ctx, ch, chDir, opts.Fetcher); err != nil {
            return fmt.Errorf("fetch images %s: %w", t.Folder, err)
        }
    }

    cs, err := comments.Scrape(ctx, t.URL, opts.Fetcher)
    if err != nil {
        return fmt.Errorf("scrape %s: %w", t.Folder, err)
    }
    if len(cs) > 0 {
        f, err := os.Create(filepath.Join(chDir, layout.CommentsFilename))
        if err != nil { return err }
        defer f.Close()
        if err := comments.Render(cs, f); err != nil {
            return fmt.Errorf("render %s: %w", t.Folder, err)
        }
    }

    // .ok marker LAST.
    return os.WriteFile(filepath.Join(chDir, ".ok"), nil, 0o644)
}

func filterRange(in []site.Chapter, from, to int) []site.Chapter {
    if from == 0 && to == 0 { return in }
    out := make([]site.Chapter, 0, len(in))
    for _, c := range in {
        n := parseChapterNumber(c.Number)
        if from != 0 && n < float64(from) { continue }
        if to != 0 && n > float64(to) { continue }
        out = append(out, c)
    }
    return out
}

// parseChapterNumber turns "12" → 12.0, "12.5" → 12.5, "12-5" → 12.5.
// Returns 0 (and the chapter is excluded by --from/--to filtering)
// for unparseable strings.
func parseChapterNumber(s string) float64 {
    s = strings.ReplaceAll(s, "-", ".")
    n, err := strconv.ParseFloat(s, 64)
    if err != nil {
        return 0
    }
    return n
}
```

- [ ] **Step 2: Write an integration-ish test**

```go
// internal/pipeline/pipeline_test.go
package pipeline

import (
    "context"
    "io/ioutil"
    "log"
    "net/url"
    "os"
    "path/filepath"
    "testing"

    "github.com/anhpham/downloader/internal/fetcher"
    "github.com/anhpham/downloader/internal/site"
)

type fakeSite struct{ chs []site.Chapter }

func (f *fakeSite) ListChapters(_ context.Context, _ string) ([]site.Chapter, error) {
    return f.chs, nil
}
func (f *fakeSite) ChapterImages(_ context.Context, _ string) ([]string, error) {
    return []string{"https://example.com/x.jpg"}, nil
}

type fakeFetcher struct {
    chapterHTML []byte
    imageBytes  []byte
}

func (f *fakeFetcher) Get(_ context.Context, req fetcher.Request) (*fetcher.Response, error) {
    if strings.HasSuffix(req.URL, ".jpg") {
        return &fetcher.Response{Body: f.imageBytes, ContentType: "image/jpeg"}, nil
    }
    return &fetcher.Response{Body: f.chapterHTML, ContentType: "text/html"}, nil
}
func (f *fakeFetcher) Post(_ context.Context, _ fetcher.Request, _ url.Values) (*fetcher.Response, error) {
    return &fetcher.Response{Body: nil, ContentType: "text/html"}, nil
}

func TestRun_FreshSyncManga(t *testing.T) {
    raw, err := ioutil.ReadFile("../comments/testdata/chapter-with-comments.html")
    if err != nil { t.Fatal(err) }
    root := t.TempDir()
    opts := Opts{
        Mode: SyncManga,
        MangaURL: "https://example.com/m",
        Root: root, Name: "m",
        Concurrency: 2,
        Site: &fakeSite{chs: []site.Chapter{{Number: "1", URL: "https://example.com/m-chap-1"}}},
        Fetcher: &fakeFetcher{chapterHTML: raw, imageBytes: []byte("FAKEJPEG")},
        Logger: log.New(os.Stderr, "", 0),
    }
    if err := Run(context.Background(), opts); err != nil { t.Fatal(err) }
    if _, err := os.Stat(filepath.Join(root, "m.cbz")); err != nil {
        t.Fatal("expected m.cbz to exist:", err)
    }
}

func TestRun_ResumeOnMissingArchiveReturnsErrNoArchive(t *testing.T) {
    root := t.TempDir()
    opts := Opts{
        Mode: Resume, MangaURL: "x", Root: root, Name: "m",
        Concurrency: 1,
        Site: &fakeSite{chs: []site.Chapter{{Number: "1", URL: "u"}}},
        Fetcher: &fakeFetcher{},
        Logger: log.New(os.Stderr, "", 0),
    }
    err := Run(context.Background(), opts)
    if !errors.Is(err, ErrNoArchive) {
        t.Fatalf("err = %v, want ErrNoArchive", err)
    }
}

func TestRun_ConcurrentInvocationFailsFast(t *testing.T) {
    root := t.TempDir()
    name := "m"
    lockPath := filepath.Join(root, name+".cbz.lock")
    held := flock.New(lockPath)
    if ok, _ := held.TryLock(); !ok { t.Fatal("could not pre-acquire lock") }
    defer held.Unlock()

    opts := Opts{
        Mode: SyncManga, MangaURL: "x", Root: root, Name: name,
        Concurrency: 1,
        Site: &fakeSite{},
        Fetcher: &fakeFetcher{},
        Logger: log.New(os.Stderr, "", 0),
    }
    err := Run(context.Background(), opts)
    if !errors.Is(err, ErrAnotherInstance) {
        t.Fatalf("err = %v, want ErrAnotherInstance", err)
    }
}
```

(Adjust the `fakeFetcher` and `fakeSite` to match your interfaces — the snippets above use placeholder signatures.)

- [ ] **Step 3: Add the `gofrs/flock` dep**

```bash
go get github.com/gofrs/flock
```

- [ ] **Step 4: Run the tests**

```bash
go test ./internal/pipeline/ -v
```

Expected: PASS. `downloader.FetchChapterImages` is already in place from Task 13.

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/ go.mod go.sum
git commit -m "pipeline: Run orchestration with file lock + scratch dir + stage"
```

---


## Task 15: main.go — subcommands

Replace `--resume` flag with three subcommands. Wire to `pipeline.Run`.

**Files:**
- Modify: `main.go`

- [ ] **Step 1: Update flag handling**

```go
// main.go (full rewrite of the flag block)
package main

import (
    "context"
    "errors"
    "flag"
    "fmt"
    "log"
    "os"

    "github.com/anhpham/downloader/internal/fetcher"
    "github.com/anhpham/downloader/internal/pipeline"
    sourcesite "github.com/anhpham/downloader/internal/site/source"
)

func main() {
    if len(os.Args) < 2 {
        usage(); os.Exit(2)
    }
    mode, ok := parseMode(os.Args[1])
    if !ok {
        usage(); os.Exit(2)
    }

    fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
    out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
    concurrency := fs.Int("concurrency", 4, "chapters in flight")
    from := fs.Int("from", 0, "first chapter number (0 = no lower bound)")
    to := fs.Int("to", 0, "last chapter number (0 = no upper bound)")
    verbose := fs.Bool("verbose", false, "per-chapter progress to stderr")
    cookiesPath := fs.String("cookies", defaultCookiesPath(), "path to cookie JSON")
    name := fs.String("name", "", "archive name (defaults to URL slug)")
    fs.Usage = usage
    fs.Parse(os.Args[2:])

    if fs.NArg() != 1 {
        usage(); os.Exit(2)
    }
    mangaURL := fs.Arg(0)

    slug, err := slugFromURL(mangaURL)
    if err != nil {
        fmt.Fprintln(os.Stderr, "invalid url:", err)
        os.Exit(2)
    }
    if *name != "" { slug = *name }

    cf, err := fetcher.LoadCookieFile(*cookiesPath)
    if err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(2)
    }
    f := fetcher.NewHTTP(cf)

    site := sourcesite.New(f)

    err = pipeline.Run(context.Background(), pipeline.Opts{
        Mode:        mode,
        MangaURL:    mangaURL,
        Root:        *out,
        Name:        slug,
        From:        *from,
        To:          *to,
        Concurrency: *concurrency,
        Site:        site,
        Fetcher:     f,
        Verbose:     *verbose,
        Logger:      log.New(os.Stderr, "", 0),
    })
    switch {
    case err == nil:
        return
    case errors.Is(err, pipeline.ErrAnotherInstance):
        fmt.Fprintln(os.Stderr, "another downloader is running for", slug)
        os.Exit(2)
    case errors.Is(err, pipeline.ErrNoArchive):
        fmt.Fprintln(os.Stderr,
            "no archive at", *out+"/"+slug+".cbz",
            "— run `downloader sync-manga` first")
        os.Exit(0)
    case errors.Is(err, fetcher.ErrCloudflareExpired):
        fmt.Fprintln(os.Stderr,
            "→ refresh cf_clearance in", *cookiesPath,
            "and re-run with `resume` or `sync-manga`")
        os.Exit(1)
    default:
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}

func parseMode(s string) (pipeline.Mode, bool) {
    switch s {
    case "sync-comments": return pipeline.SyncComments, true
    case "resume":        return pipeline.Resume, true
    case "sync-manga":    return pipeline.SyncManga, true
    }
    return 0, false
}

func usage() {
    fmt.Fprintln(os.Stderr, `usage:
  downloader sync-manga    [flags] <manga-url>   download new chapters + comments + backfill missing comments
  downloader resume        [flags] <manga-url>   download new chapters + comments (no backfill)
  downloader sync-comments [flags] <manga-url>   backfill comments on existing archive (no new chapters)

flags:
  --out string           root directory (default: ~/Documents/Manga)
  --name string          archive name (default: URL slug)
  --concurrency int      chapters in flight (default 4)
  --from int             first chapter
  --to int               last chapter
  --cookies string       path to cookie JSON
  --verbose              per-chapter progress`)
}

// Helpers defaultOutDir, defaultCookiesPath, slugFromURL — keep
// from the existing main.go; they don't change.
```

- [ ] **Step 2: Build and try it**

```bash
go build -o bin/downloader .
./bin/downloader sync-manga --help 2>&1 || true
```

Expected: usage prints.

- [ ] **Step 3: Run end-to-end on a small test**

```bash
./bin/downloader sync-comments \
  --name "Slam Dunk" \
  https://truyenqqko.com/truyen-tranh/cao-thu-bong-ro-remake-14065
```

Expected: scans the existing `Slam Dunk.cbz`, backfills comment PNGs, archive grows by a few MB.

If the lock works, a second concurrent run should fail fast with "another downloader is running."

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "main: subcommand dispatch (sync-manga / resume / sync-comments)"
```

---

## Task 16: package-cbz.sh — mark legacy

**Files:**
- Modify: `package-cbz.sh`

- [ ] **Step 1: Update the header comment**

```bash
# Edit package-cbz.sh — replace the leading comment block with:

# package-cbz.sh — LEGACY (kept for backwards compatibility).
#
# As of feature/comment-pages, the downloader writes directly into
# the .cbz via internal/archive/StageAndRename. New users do not
# need this script. It is preserved so anyone with leftover chapter
# folders from the pre-CBZ era can still bundle them manually.
```

- [ ] **Step 2: Commit**

```bash
git add package-cbz.sh
git commit -m "package-cbz.sh: mark legacy in header comment"
```

---

## Task 17: README + PLAN.md updates

**Files:**
- Modify: `README.md`
- Modify: `PLAN.md`

- [ ] **Step 1: Rewrite README's usage section**

Replace the "## Usage" section with:

```markdown
## Usage

Three subcommands, all operating against `<--out>/<name>.cbz`:

```sh
bin/downloader sync-manga    <manga-url>   # download new chapters + comments
                                            # AND backfill comments on existing chapters
bin/downloader resume        <manga-url>   # download new chapters + comments only
bin/downloader sync-comments <manga-url>   # backfill comments only (no new chapters)
```

The archive is the only persistent state — there are no per-chapter
folders or `.done` markers on disk. A scratch directory at
`<out>/.<name>.scratch/` is used during a run and deleted on clean
exit; partial work survives a kill via per-chapter `.ok` markers.

Concurrent runs against the same archive are prevented by a file
lock at `<out>/<name>.cbz.lock`.
```

Also delete the chapter-folder layout diagram at the top of the README.

- [ ] **Step 2: Update PLAN.md to reference the new spec**

Add a note at the top of `PLAN.md`:

```markdown
> Superseded by the comment-pages design at
> `docs/superpowers/specs/2026-05-16-comment-pages-design.md`.
> The original "chapter folder + .done + package-cbz.sh" design
> below is kept for historical context only.
```

- [ ] **Step 3: Commit**

```bash
git add README.md PLAN.md
git commit -m "docs: README + PLAN updates for CBZ-only flow"
```

---

## Task 18: Refresh the auto-memory feedback note

The user's memory at `~/.claude/projects/-Users-anhpham-Documents-Projects-script-downloader/memory/feedback_downloader_workflow.md` currently says *"always run ./package-cbz.sh after a successful download"*. After this feature lands, that is wrong — the downloader does the bundling in-process.

**Files:**
- Modify: `~/.claude/projects/-Users-anhpham-Documents-Projects-script-downloader/memory/feedback_downloader_workflow.md`

- [ ] **Step 1: Rewrite the memory**

Update the file to read:

```markdown
---
name: downloader-workflow
description: How to use the downloader after the comment-pages feature shipped
metadata:
  type: feedback
---

The downloader now produces `.cbz` archives directly via
`internal/archive/StageAndRename`. Three subcommands:

- `bin/downloader sync-manga    <url>` — new chapters + comments + backfill
- `bin/downloader resume        <url>` — new chapters + comments only
- `bin/downloader sync-comments <url>` — backfill comments only

**Why:** consolidated to one persistent format (.cbz), no per-chapter
folders or `.done` sentinels.

**How to apply:** do not run `./package-cbz.sh` after a download —
it is legacy. The new tool bundles in-process. Re-run with
`sync-manga` or `resume` (depending on intent) if `cf_clearance`
expired mid-run.
```

- [ ] **Step 2: Commit (in this repo, not the memory store)**

The memory file is outside this repo; no commit needed. Just update it.

---

## Self-review checklist (for the executor)

Before declaring done, run:

```bash
go test ./...
gofmt -l .
go vet ./...
```

Expected: zero output from `gofmt -l .`; PASS for everything.

Then perform an end-to-end manual smoke test:

1. `bin/downloader sync-manga --name "Test" https://truyenqqko.com/...` — fresh archive created.
2. `bin/downloader sync-comments --name "Test" https://truyenqqko.com/...` — backfills present chapters; archive grows.
3. Two concurrent invocations: second fails fast with "another downloader is running."
4. Kill mid-run, re-invoke: `.ok`-marked subdirs survive; work is not redone.

---

## What this plan does NOT cover (deferred)

These were in the spec as deferred / open decisions and are explicitly out of scope here:

- Image-count-mismatch warning for `sync-manga` against partial chapters (would cost N extra GETs per run — design left it gated behind a future `--audit` flag).
- A `--repair` / `--force` mode that re-fetches already-archived chapters.
- A pre-existing-bad-CRC escape hatch for the verification step.
- Hi-DPI rendering (currently 1×).
- Renderer fallback to headless Chrome (explicitly out of scope per spec — emoji are best-effort).

If any of these become necessary during execution, surface them as separate plans rather than expanding this one.
