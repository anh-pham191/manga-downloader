# Registry + `update-all` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remember the source URL of every archived manga, add `update-all` to pull new chapters for all of them, and handle a source-domain change in one step.

**Architecture:** A new `internal/registry` package owns `<root>/.registry.json`. `main.go` and the MCP sync executor upsert into it after successful runs. A new `pipeline.UpdateAll` iterates the registry running `Resume` per manga, using a new `fetcher.Classify` helper to distinguish Cloudflare expiry from host-unreachable before ever asking for a new domain. A new `Site.Search` adapter method backs a `discover` verb that proposes URLs for the 25 pre-existing archives.

**Tech Stack:** Go 1.26, goquery, gofrs/flock, modelcontextprotocol/go-sdk. Tests use the standard `testing` package with `t.TempDir()`.

**Spec:** `docs/superpowers/specs/2026-09-05-registry-update-all-design.md`

## Global Constraints

- Module path is `github.com/anhpham/downloader`. Run tests with `go test ./...` from the repo root.
- Commit with `git -c user.email=10724369+anh-pham191@users.noreply.github.com commit` is NOT needed: the repo already has that email configured. Just `git commit`. End every commit message with `Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>`.
- Never re-add a headless browser. Never retry on `fetcher.ErrCloudflareExpired`.
- `.ok` (not `.done`) is the in-scratch chapter sentinel; do not touch it.
- Registry file name is exactly `.registry.json` in the manga root. Lock file is `.registry.json.lock`.
- Host rewrite replaces scheme+host only; path and query are untouched.
- `update-all` runs `pipeline.Resume` (not `SyncManga`).
- Site search endpoint: `POST https://<host>/frontend/search/search`, form `search=<query>&type=0`, Referer `https://<host>/`. Response is an HTML fragment of `<li><a href="..."><div class="search_info"><p class="name">Title</p>...` items. Fixture already saved at `internal/site/source/testdata/search.html`.
- A pre-existing test helper pattern: `internal/pipeline/pipeline_test.go` defines `fakeSite` and `fakeFetcher`; follow that style for new fakes.

---

### Task 1: `internal/registry` package

**Files:**
- Create: `internal/registry/registry.go`
- Create: `internal/registry/registry_test.go`

**Interfaces:**
- Produces:
  ```go
  package registry
  const FileName = ".registry.json"
  type Entry struct {
      URL        string    `json:"url"`
      Added      time.Time `json:"added"`
      LastSynced time.Time `json:"last_synced,omitempty"`
  }
  type Registry struct {
      Version int              `json:"version"`
      Manga   map[string]Entry `json:"manga"`
      root    string           // unexported; set by Load
  }
  func Load(root string) (*Registry, error)         // missing file → empty registry, nil error
  func (r *Registry) Save() error                    // atomic: write .registry.json.tmp then rename; holds .registry.json.lock via flock
  func (r *Registry) Upsert(name, url string)        // sets URL; sets Added if new
  func (r *Registry) Touch(name string, at time.Time) // sets LastSynced; no-op if name absent
  func (r *Registry) Names() []string                // sorted keys
  func (r *Registry) RewriteHost(newHost string) (int, error) // returns count changed; newHost may include scheme; default https
  func (r *Registry) Get(name string) (Entry, bool)
  ```

- [ ] **Step 1: Write the failing tests**

```go
package registry

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoad_MissingFileIsEmpty(t *testing.T) {
	r, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Manga) != 0 || r.Version != 1 {
		t.Fatalf("expected empty v1 registry, got %+v", r)
	}
}

func TestUpsertSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	r, _ := Load(root)
	r.Upsert("Gintama", "https://truyenqqko.com/truyen-tranh/gintama-216")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, FileName)); err != nil {
		t.Fatal("registry file not written:", err)
	}
	r2, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := r2.Get("Gintama")
	if !ok || e.URL != "https://truyenqqko.com/truyen-tranh/gintama-216" {
		t.Fatalf("round trip lost entry: %+v ok=%v", e, ok)
	}
	if e.Added.IsZero() {
		t.Fatal("Added should be set on first upsert")
	}
}

func TestUpsert_KeepsAddedOnUpdate(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("A", "https://x.com/a")
	first := r.Manga["A"].Added
	time.Sleep(2 * time.Millisecond)
	r.Upsert("A", "https://x.com/a2")
	if !r.Manga["A"].Added.Equal(first) {
		t.Fatal("Added must not change on update")
	}
	if r.Manga["A"].URL != "https://x.com/a2" {
		t.Fatal("URL must update")
	}
}

func TestTouch(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("A", "https://x.com/a")
	at := time.Date(2026, 9, 5, 10, 0, 0, 0, time.UTC)
	r.Touch("A", at)
	r.Touch("missing", at) // must not panic or create
	if !r.Manga["A"].LastSynced.Equal(at) {
		t.Fatal("LastSynced not set")
	}
	if _, ok := r.Manga["missing"]; ok {
		t.Fatal("Touch must not create entries")
	}
}

func TestNames_Sorted(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("b", "https://x.com/b")
	r.Upsert("A", "https://x.com/a")
	r.Upsert("c", "https://x.com/c")
	got := r.Names()
	want := []string{"A", "b", "c"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestRewriteHost(t *testing.T) {
	r, _ := Load(t.TempDir())
	r.Upsert("A", "https://truyenqqko.com/truyen-tranh/a-1")
	r.Upsert("B", "https://truyenqqko.com/truyen-tranh/b-2?x=1")
	r.Upsert("C", "https://other.example/keep-me")
	n, err := r.RewriteHost("truyenqqnew.com")
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("expected 3 rewritten, got %d", n)
	}
	if r.Manga["A"].URL != "https://truyenqqnew.com/truyen-tranh/a-1" {
		t.Fatal("A:", r.Manga["A"].URL)
	}
	if r.Manga["B"].URL != "https://truyenqqnew.com/truyen-tranh/b-2?x=1" {
		t.Fatal("B lost query:", r.Manga["B"].URL)
	}
	if r.Manga["C"].URL != "https://truyenqqnew.com/keep-me" {
		t.Fatal("C:", r.Manga["C"].URL)
	}
	// scheme supplied explicitly
	if _, err := r.RewriteHost("http://plain.example"); err != nil {
		t.Fatal(err)
	}
	if r.Manga["A"].URL != "http://plain.example/truyen-tranh/a-1" {
		t.Fatal("explicit scheme not honoured:", r.Manga["A"].URL)
	}
}

func TestLoad_CorruptFile(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, FileName), []byte("{not json"), 0o644)
	if _, err := Load(root); err == nil {
		t.Fatal("expected error for corrupt registry")
	}
}

func TestSave_NoTempLeftBehind(t *testing.T) {
	root := t.TempDir()
	r, _ := Load(root)
	r.Upsert("A", "https://x.com/a")
	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() != FileName {
			t.Fatalf("unexpected file left in root: %s", e.Name())
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/registry/`
Expected: FAIL to compile (package has no non-test files / undefined symbols).

- [ ] **Step 3: Implement**

```go
// Package registry persists the source URL of every archived manga so
// update-all can find new chapters without the operator re-pasting
// URLs. One JSON file per manga root.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const FileName = ".registry.json"

type Entry struct {
	URL        string    `json:"url"`
	Added      time.Time `json:"added"`
	LastSynced time.Time `json:"last_synced,omitempty"`
}

type Registry struct {
	Version int              `json:"version"`
	Manga   map[string]Entry `json:"manga"`
	root    string
}

// Load reads <root>/.registry.json. A missing file yields an empty
// registry; a malformed one is an error so we never silently wipe it.
func Load(root string) (*Registry, error) {
	r := &Registry{Version: 1, Manga: map[string]Entry{}, root: root}
	b, err := os.ReadFile(filepath.Join(root, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("registry %s is corrupt: %w", FileName, err)
	}
	if r.Manga == nil {
		r.Manga = map[string]Entry{}
	}
	if r.Version == 0 {
		r.Version = 1
	}
	r.root = root
	return r, nil
}

// Save writes atomically (tmp + rename) under a file lock so two
// concurrent downloader processes cannot interleave writes.
func (r *Registry) Save() error {
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(r.root, FileName+".lock")
	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		return err
	}
	defer func() {
		lock.Unlock()
		os.Remove(lockPath)
	}()

	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(r.root, FileName)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func (r *Registry) Upsert(name, rawURL string) {
	e, ok := r.Manga[name]
	if !ok {
		e.Added = time.Now()
	}
	e.URL = rawURL
	r.Manga[name] = e
}

func (r *Registry) Touch(name string, at time.Time) {
	e, ok := r.Manga[name]
	if !ok {
		return
	}
	e.LastSynced = at
	r.Manga[name] = e
}

func (r *Registry) Get(name string) (Entry, bool) {
	e, ok := r.Manga[name]
	return e, ok
}

func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.Manga))
	for k := range r.Manga {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RewriteHost swaps the scheme+host of every stored URL. newHost may
// be "example.com" (https assumed) or "http://example.com".
func (r *Registry) RewriteHost(newHost string) (int, error) {
	newHost = strings.TrimSpace(newHost)
	if !strings.Contains(newHost, "://") {
		newHost = "https://" + newHost
	}
	nu, err := url.Parse(newHost)
	if err != nil || nu.Host == "" {
		return 0, fmt.Errorf("invalid host %q", newHost)
	}
	n := 0
	for name, e := range r.Manga {
		u, err := url.Parse(e.URL)
		if err != nil {
			continue
		}
		u.Scheme = nu.Scheme
		u.Host = nu.Host
		e.URL = u.String()
		r.Manga[name] = e
		n++
	}
	return n, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/registry/ -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/registry
git commit -m "registry: persist manga source URLs in <root>/.registry.json

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 2: `fetcher.Classify` error classifier

**Files:**
- Create: `internal/fetcher/classify.go`
- Create: `internal/fetcher/classify_test.go`

**Interfaces:**
- Produces:
  ```go
  package fetcher
  type Kind int
  const (
      KindOther Kind = iota
      KindCloudflare        // errors.Is(err, ErrCloudflareExpired)
      KindHostUnreachable   // DNS, connect refused/reset, TLS, timeout dialing
  )
  func Classify(err error) Kind
  func (k Kind) String() string // "other", "cloudflare", "host-unreachable"
  ```

- [ ] **Step 1: Write the failing tests**

```go
package fetcher

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want Kind
	}{
		{"nil", nil, KindOther},
		{"cloudflare direct", ErrCloudflareExpired, KindCloudflare},
		{"cloudflare wrapped", fmt.Errorf("list chapters: %w", ErrCloudflareExpired), KindCloudflare},
		{"dns", &net.DNSError{Err: "no such host", Name: "truyenqqko.com", IsNotFound: true}, KindHostUnreachable},
		{"dns wrapped in op", &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host"}}, KindHostUnreachable},
		{"conn refused", &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}, KindHostUnreachable},
		{"conn reset", &net.OpError{Op: "read", Err: syscall.ECONNRESET}, KindHostUnreachable},
		{"tls hostname", x509.HostnameError{Host: "truyenqqko.com"}, KindHostUnreachable},
		{"tls unknown authority", x509.UnknownAuthorityError{}, KindHostUnreachable},
		{"generic", errors.New("unexpected status 404"), KindOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Classify(c.err); got != c.want {
				t.Fatalf("Classify(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

func TestKindString(t *testing.T) {
	if KindCloudflare.String() != "cloudflare" || KindHostUnreachable.String() != "host-unreachable" || KindOther.String() != "other" {
		t.Fatal("String() labels wrong")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/fetcher/ -run 'TestClassify|TestKindString'`
Expected: FAIL to compile (undefined: Classify, Kind).

- [ ] **Step 3: Implement**

```go
package fetcher

import (
	"crypto/x509"
	"errors"
	"net"
	"syscall"
)

// Kind is a coarse classification of a fetch error, used by update-all
// to decide whether a failure means "refresh the Cloudflare token" or
// "the site moved to a new domain". Anything else is KindOther.
type Kind int

const (
	KindOther Kind = iota
	KindCloudflare
	KindHostUnreachable
)

func (k Kind) String() string {
	switch k {
	case KindCloudflare:
		return "cloudflare"
	case KindHostUnreachable:
		return "host-unreachable"
	default:
		return "other"
	}
}

// Classify inspects err's chain. Cloudflare wins over everything else
// because a 403 is a definite signal; host errors are transport-level
// failures that happen before any HTTP status exists.
func Classify(err error) Kind {
	if err == nil {
		return KindOther
	}
	if errors.Is(err, ErrCloudflareExpired) {
		return KindCloudflare
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return KindHostUnreachable
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, syscall.ENETUNREACH) {
		return KindHostUnreachable
	}
	var hn x509.HostnameError
	var ua x509.UnknownAuthorityError
	var ci x509.CertificateInvalidError
	if errors.As(err, &hn) || errors.As(err, &ua) || errors.As(err, &ci) {
		return KindHostUnreachable
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return KindHostUnreachable
	}
	return KindOther
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/fetcher/`
Expected: PASS (including existing tests).

- [ ] **Step 5: Commit**

```bash
git add internal/fetcher/classify.go internal/fetcher/classify_test.go
git commit -m "fetcher: Classify errors into cloudflare / host-unreachable / other

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 3: `Site.Search` adapter method

**Files:**
- Modify: `internal/site/site.go` (add `SearchHit` type and `Search` to interface)
- Create: `internal/site/source/search.go`
- Create: `internal/site/source/search_test.go`
- Modify: `internal/site/source/adapter.go` (add `Search` method)
- Modify: `internal/pipeline/pipeline_test.go:20-27` (`fakeSite` must implement `Search`)
- Modify: any other `site.Site` fakes found by `grep -rn "ListChapters(_ context" --include=*_test.go internal` (add a `Search` stub returning `nil, nil`)
- Test fixture (already present): `internal/site/source/testdata/search.html`

**Interfaces:**
- Produces:
  ```go
  // in package site
  type SearchHit struct {
      Title string
      URL   string
  }
  // added to interface Site:
  Search(ctx context.Context, baseURL, query string) ([]SearchHit, error)

  // in package source
  func ParseSearch(html string) ([]site.SearchHit, error)
  func (s *Site) Search(ctx context.Context, baseURL, query string) ([]site.SearchHit, error)
  ```
  `baseURL` is any URL on the site (e.g. a manga URL); `Search` derives `scheme://host` from it and POSTs to `<scheme>://<host>/frontend/search/search` with form `search=<query>`, `type=0`, Referer `<scheme>://<host>/`.

- [ ] **Step 1: Write the failing tests**

```go
package source

import (
	"context"
	"net/url"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
)

func TestParseSearch(t *testing.T) {
	hits, err := ParseSearch(loadFixture(t, "search.html"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].Title != "Gintama" || hits[0].URL != "https://truyenqqko.com/truyen-tranh/gintama-216" {
		t.Fatalf("hit[0] = %+v", hits[0])
	}
	if hits[1].Title != "Gintama: 3-nen Z-gumi Ginpachi-sensei Tuuuunnn!!" {
		t.Fatalf("hit[1] title = %q", hits[1].Title)
	}
}

func TestParseSearch_Empty(t *testing.T) {
	hits, err := ParseSearch("\n  \n")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected no hits, got %d", len(hits))
	}
}

type recordingFetcher struct {
	gotURL     string
	gotReferer string
	gotForm    url.Values
	body       string
}

func (r *recordingFetcher) Get(_ context.Context, _ fetcher.Request) (*fetcher.Response, error) {
	return nil, nil
}
func (r *recordingFetcher) Post(_ context.Context, req fetcher.Request, form url.Values) (*fetcher.Response, error) {
	r.gotURL, r.gotReferer, r.gotForm = req.URL, req.Referer, form
	return &fetcher.Response{Body: []byte(r.body), ContentType: "text/html"}, nil
}

func TestSite_Search_PostsToEndpoint(t *testing.T) {
	rf := &recordingFetcher{body: loadFixture(t, "search.html")}
	s := &Site{Fetcher: rf}
	hits, err := s.Search(context.Background(), "https://truyenqqko.com/truyen-tranh/anything-1", "gintama")
	if err != nil {
		t.Fatal(err)
	}
	if rf.gotURL != "https://truyenqqko.com/frontend/search/search" {
		t.Fatalf("posted to %q", rf.gotURL)
	}
	if rf.gotReferer != "https://truyenqqko.com/" {
		t.Fatalf("referer %q", rf.gotReferer)
	}
	if rf.gotForm.Get("search") != "gintama" || rf.gotForm.Get("type") != "0" {
		t.Fatalf("form %v", rf.gotForm)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/site/source/ -run 'Search'`
Expected: FAIL to compile (undefined: ParseSearch, Site.Search).

- [ ] **Step 3: Add the type and interface method in `internal/site/site.go`**

```go
// SearchHit is one result from the site's title search.
type SearchHit struct {
	Title string
	URL   string
}
```
and inside `type Site interface { ... }` add:
```go
	// Search queries the site's title search. baseURL is any URL on the
	// site; the adapter derives scheme+host from it.
	Search(ctx context.Context, baseURL, query string) ([]SearchHit, error)
```

- [ ] **Step 4: Implement `internal/site/source/search.go`**

```go
package source

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/site"
)

const searchPath = "/frontend/search/search"

// ParseSearch parses the HTML fragment returned by the site's
// autocomplete endpoint: a list of <li><a href><p class="name">.
func ParseSearch(html string) ([]site.SearchHit, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return nil, fmt.Errorf("parse search html: %w", err)
	}
	var hits []site.SearchHit
	doc.Find("li a[href]").Each(func(_ int, a *goquery.Selection) {
		href, _ := a.Attr("href")
		title := strings.TrimSpace(a.Find("p.name").First().Text())
		if href == "" || title == "" {
			return
		}
		hits = append(hits, site.SearchHit{Title: title, URL: href})
	})
	return hits, nil
}

// Search POSTs the query to the autocomplete endpoint on the same
// host as baseURL.
func (s *Site) Search(ctx context.Context, baseURL, query string) ([]site.SearchHit, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("search: invalid base url %q", baseURL)
	}
	origin := u.Scheme + "://" + u.Host
	form := url.Values{"search": {query}, "type": {"0"}}
	resp, err := s.Fetcher.Post(ctx, fetcher.Request{URL: origin + searchPath, Referer: origin + "/"}, form)
	if err != nil {
		return nil, fmt.Errorf("search %q: %w", query, err)
	}
	return ParseSearch(string(resp.Body))
}
```

- [ ] **Step 5: Fix every `site.Site` fake so the build passes**

Run `grep -rln "ChapterImages(_ context" --include=*_test.go internal` and add to each fake type (e.g. `fakeSite` in `internal/pipeline/pipeline_test.go`):

```go
func (f *fakeSite) Search(_ context.Context, _, _ string) ([]site.SearchHit, error) {
	return nil, nil
}
```
(adjust receiver name to match each fake).

- [ ] **Step 6: Run the full test suite**

Run: `go build ./... && go test ./...`
Expected: everything compiles and PASSES.

- [ ] **Step 7: Commit**

```bash
git add internal/site internal/pipeline/pipeline_test.go $(git diff --name-only)
git commit -m "site: add Search adapter backed by /frontend/search/search

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 4: `pipeline.RunResult` — expose new-chapter count

**Files:**
- Modify: `internal/pipeline/pipeline.go:43-150` (`Run`)
- Modify: `internal/pipeline/pipeline_test.go` (add one test)

**Interfaces:**
- Produces:
  ```go
  type Result struct {
      NewChapters int // Kind==Both tasks that succeeded
      Failed      int
  }
  func RunResult(ctx context.Context, opts Opts) (Result, error)
  // Run keeps its signature and becomes: func Run(ctx, opts) error { _, err := RunResult(ctx, opts); return err }
  ```

- [ ] **Step 1: Write the failing test**

Append to `internal/pipeline/pipeline_test.go` (it already has `fakeSite`, `fakeFetcher`, and a fresh-sync test that reads `../comments/testdata/chapter-with-comments.html`; copy the same opts construction):

```go
func TestRunResult_CountsNewChapters(t *testing.T) {
	raw, err := ioutil.ReadFile("../comments/testdata/chapter-with-comments.html")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	opts := Opts{
		Mode:        SyncManga,
		MangaURL:    "https://example.com/manga",
		Root:        root,
		Name:        "m",
		Concurrency: 2,
		Site: &fakeSite{chs: []site.Chapter{
			{Number: "1", URL: "https://example.com/manga/1"},
			{Number: "2", URL: "https://example.com/manga/2"},
		}},
		Fetcher: &fakeFetcher{chapterHTML: raw, imageBytes: []byte("\xff\xd8\xff\xd9")},
	}
	res, err := RunResult(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewChapters != 2 || res.Failed != 0 {
		t.Fatalf("got %+v, want NewChapters=2 Failed=0", res)
	}
	// second run: nothing new
	res, err = RunResult(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if res.NewChapters != 0 {
		t.Fatalf("second run should add 0 chapters, got %d", res.NewChapters)
	}
}
```
If the existing fresh-sync test uses different image bytes for `fakeFetcher`, copy exactly what it uses.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/pipeline/ -run TestRunResult`
Expected: FAIL to compile (undefined: RunResult, Result).

- [ ] **Step 3: Implement**

In `pipeline.go`, rename the current `func Run(ctx context.Context, opts Opts) error` body into `RunResult` and add the wrapper:

```go
// Result summarises one run for callers that aggregate (update-all).
type Result struct {
	NewChapters int
	Failed      int
}

// Run executes one mode end-to-end.
func Run(ctx context.Context, opts Opts) error {
	_, err := RunResult(ctx, opts)
	return err
}

// RunResult is Run plus a count of what changed.
func RunResult(ctx context.Context, opts Opts) (Result, error) {
	var res Result
	... existing body, with these edits ...
}
```
Edits inside the body:
- every `return err` / `return fmt.Errorf(...)` becomes `return res, err` / `return res, fmt.Errorf(...)`; `return nil` (nothing to do) becomes `return res, nil`.
- after `failed := runTasks(...)`: 
  ```go
  res.Failed = len(failed)
  failedSet := map[string]bool{}
  for _, f := range failed {
      failedSet[f.Folder] = true
  }
  for _, t := range tasks {
      if t.Kind == Both && !failedSet[t.Folder] {
          res.NewChapters++
      }
  }
  ```
- final `return nil` becomes `return res, nil`; the `len(failed) > 0` error return becomes `return res, fmt.Errorf(...)`.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/pipeline/ ./internal/mcp/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline
git commit -m "pipeline: RunResult returns new-chapter and failure counts

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 5: `pipeline.UpdateAll`

**Files:**
- Create: `internal/pipeline/updateall.go`
- Create: `internal/pipeline/updateall_test.go`

**Interfaces:**
- Consumes: `registry.Registry` (Task 1), `fetcher.Classify` (Task 2), `RunResult` (Task 4).
- Produces:
  ```go
  var ErrDomainMoved = errors.New("source host unreachable; domain may have moved")

  type UpdateAllOpts struct {
      Root        string
      Registry    *registry.Registry
      Site        site.Site
      Fetcher     fetcher.Fetcher
      Concurrency int
      Logger      *log.Logger        // may be nil
      // AskDomain is called when preflight classifies the failure as
      // host-unreachable. Return the new host, or "" to abort. nil → abort.
      AskDomain   func(oldHost string, cause error) string
      Now         func() time.Time   // nil → time.Now
      Runner      func(ctx context.Context, opts Opts) (Result, error) // nil → RunResult
  }

  type MangaOutcome struct {
      Name        string
      NewChapters int
      Status      string // "ok", "no-archive", "busy", "failed", "skipped"
      Err         error
  }

  type UpdateAllResult struct {
      Outcomes    []MangaOutcome
      DomainMoved bool   // true when a host rewrite was applied
      NewHost     string
  }

  func UpdateAll(ctx context.Context, o UpdateAllOpts) (UpdateAllResult, error)
  ```
  Error contract: returns `fetcher.ErrCloudflareExpired` (wrapped) if any step hits it; returns `ErrDomainMoved` if host-unreachable and `AskDomain` returned "" or the new host also fails; otherwise returns nil even if individual manga failed (their status is in `Outcomes`).

- [ ] **Step 1: Write the failing tests**

```go
package pipeline

import (
	"context"
	"errors"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/registry"
	"github.com/anhpham/downloader/internal/site"
)

// scriptedSite returns a per-host error or chapter list for ListChapters.
type scriptedSite struct {
	errByHost map[string]error
	chapters  []site.Chapter
	calls     []string
}

func (s *scriptedSite) ListChapters(_ context.Context, mangaURL string) ([]site.Chapter, error) {
	s.calls = append(s.calls, mangaURL)
	u, _ := url.Parse(mangaURL)
	if err, ok := s.errByHost[u.Host]; ok && err != nil {
		return nil, err
	}
	return s.chapters, nil
}
func (s *scriptedSite) ChapterImages(_ context.Context, _ site.Chapter) ([]site.ImageRef, error) {
	return nil, nil
}
func (s *scriptedSite) Search(_ context.Context, _, _ string) ([]site.SearchHit, error) {
	return nil, nil
}

type nopFetcher struct{}

func (nopFetcher) Get(_ context.Context, _ fetcher.Request) (*fetcher.Response, error) {
	return &fetcher.Response{}, nil
}
func (nopFetcher) Post(_ context.Context, _ fetcher.Request, _ url.Values) (*fetcher.Response, error) {
	return &fetcher.Response{}, nil
}

func newReg(t *testing.T, urls map[string]string) *registry.Registry {
	r, err := registry.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for n, u := range urls {
		r.Upsert(n, u)
	}
	return r
}

func TestUpdateAll_HappyPath(t *testing.T) {
	reg := newReg(t, map[string]string{
		"A": "https://old.example/truyen-tranh/a-1",
		"B": "https://old.example/truyen-tranh/b-2",
	})
	var ran []string
	res, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root:     reg.Root(),
		Registry: reg,
		Site:     &scriptedSite{chapters: []site.Chapter{{Number: "1", URL: "x"}}},
		Fetcher:  nopFetcher{},
		Runner: func(_ context.Context, o Opts) (Result, error) {
			ran = append(ran, o.Name)
			if o.Mode != Resume {
				t.Fatalf("mode must be Resume, got %v", o.Mode)
			}
			return Result{NewChapters: 3}, nil
		},
		Now: func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ran) != 2 || ran[0] != "A" || ran[1] != "B" {
		t.Fatalf("ran %v", ran)
	}
	if res.Outcomes[0].NewChapters != 3 || res.Outcomes[0].Status != "ok" {
		t.Fatalf("outcome %+v", res.Outcomes[0])
	}
	e, _ := reg.Get("A")
	if e.LastSynced.IsZero() {
		t.Fatal("LastSynced not touched")
	}
	if res.DomainMoved {
		t.Fatal("no domain move expected")
	}
}

func TestUpdateAll_CloudflareStopsImmediately(t *testing.T) {
	reg := newReg(t, map[string]string{"A": "https://old.example/a", "B": "https://old.example/b"})
	asked := false
	_, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root:     reg.Root(),
		Registry: reg,
		Site:     &scriptedSite{errByHost: map[string]error{"old.example": fetcher.ErrCloudflareExpired}},
		Fetcher:  nopFetcher{},
		AskDomain: func(string, error) string { asked = true; return "new.example" },
		Runner: func(context.Context, Opts) (Result, error) { t.Fatal("runner must not run"); return Result{}, nil },
	})
	if !errors.Is(err, fetcher.ErrCloudflareExpired) {
		t.Fatalf("want ErrCloudflareExpired, got %v", err)
	}
	if asked {
		t.Fatal("must never ask for a domain on a Cloudflare error")
	}
}

func TestUpdateAll_DomainMovedRewritesAndContinues(t *testing.T) {
	reg := newReg(t, map[string]string{
		"A": "https://old.example/truyen-tranh/a-1",
		"B": "https://old.example/truyen-tranh/b-2",
	})
	s := &scriptedSite{
		errByHost: map[string]error{"old.example": &net.DNSError{Err: "no such host", Name: "old.example"}},
		chapters:  []site.Chapter{{Number: "1", URL: "x"}},
	}
	var gotOld string
	var ranURLs []string
	res, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root:     reg.Root(),
		Registry: reg,
		Site:     s,
		Fetcher:  nopFetcher{},
		AskDomain: func(oldHost string, _ error) string { gotOld = oldHost; return "new.example" },
		Runner: func(_ context.Context, o Opts) (Result, error) {
			ranURLs = append(ranURLs, o.MangaURL)
			return Result{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gotOld != "old.example" {
		t.Fatalf("AskDomain got %q", gotOld)
	}
	if !res.DomainMoved || res.NewHost != "new.example" {
		t.Fatalf("result %+v", res)
	}
	for _, u := range ranURLs {
		pu, _ := url.Parse(u)
		if pu.Host != "new.example" {
			t.Fatalf("runner got un-rewritten url %s", u)
		}
	}
	e, _ := reg.Get("B")
	if e.URL != "https://new.example/truyen-tranh/b-2" {
		t.Fatalf("registry not rewritten: %s", e.URL)
	}
	// registry must be persisted
	r2, _ := registry.Load(reg.Root())
	e2, _ := r2.Get("A")
	if e2.URL != "https://new.example/truyen-tranh/a-1" {
		t.Fatalf("rewrite not saved: %s", e2.URL)
	}
}

func TestUpdateAll_DomainMovedAbortWhenNoAnswer(t *testing.T) {
	reg := newReg(t, map[string]string{"A": "https://old.example/a"})
	_, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root:     reg.Root(),
		Registry: reg,
		Site:     &scriptedSite{errByHost: map[string]error{"old.example": &net.DNSError{Err: "no such host"}}},
		Fetcher:  nopFetcher{},
		AskDomain: func(string, error) string { return "" },
	})
	if !errors.Is(err, ErrDomainMoved) {
		t.Fatalf("want ErrDomainMoved, got %v", err)
	}
}

func TestUpdateAll_NewHostAlsoFailsIsError(t *testing.T) {
	reg := newReg(t, map[string]string{"A": "https://old.example/a"})
	s := &scriptedSite{errByHost: map[string]error{
		"old.example": &net.DNSError{Err: "no such host"},
		"new.example": &net.DNSError{Err: "no such host"},
	}}
	_, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root: reg.Root(), Registry: reg, Site: s, Fetcher: nopFetcher{},
		AskDomain: func(string, error) string { return "new.example" },
	})
	if !errors.Is(err, ErrDomainMoved) {
		t.Fatalf("want ErrDomainMoved, got %v", err)
	}
	e, _ := reg.Get("A")
	if e.URL != "https://old.example/a" {
		t.Fatal("registry must not be rewritten when the new host also fails")
	}
}

func TestUpdateAll_PerMangaFailuresContinue(t *testing.T) {
	reg := newReg(t, map[string]string{
		"A": "https://h.example/a", "B": "https://h.example/b", "C": "https://h.example/c",
	})
	res, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root: reg.Root(), Registry: reg,
		Site:    &scriptedSite{chapters: []site.Chapter{{Number: "1", URL: "x"}}},
		Fetcher: nopFetcher{},
		Runner: func(_ context.Context, o Opts) (Result, error) {
			switch o.Name {
			case "A":
				return Result{}, ErrNoArchive
			case "B":
				return Result{}, ErrAnotherInstance
			}
			return Result{NewChapters: 1}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"A": "no-archive", "B": "busy", "C": "ok"}
	for _, o := range res.Outcomes {
		if o.Status != want[o.Name] {
			t.Fatalf("%s status %q want %q", o.Name, o.Status, want[o.Name])
		}
	}
}

func TestUpdateAll_CloudflareMidLoopStops(t *testing.T) {
	reg := newReg(t, map[string]string{"A": "https://h.example/a", "B": "https://h.example/b"})
	var ran []string
	res, err := UpdateAll(context.Background(), UpdateAllOpts{
		Root: reg.Root(), Registry: reg,
		Site:    &scriptedSite{chapters: []site.Chapter{{Number: "1", URL: "x"}}},
		Fetcher: nopFetcher{},
		Runner: func(_ context.Context, o Opts) (Result, error) {
			ran = append(ran, o.Name)
			return Result{}, fetcher.ErrCloudflareExpired
		},
	})
	if !errors.Is(err, fetcher.ErrCloudflareExpired) {
		t.Fatalf("want cloudflare error, got %v", err)
	}
	if len(ran) != 1 {
		t.Fatalf("must stop after first cloudflare failure, ran %v", ran)
	}
	if len(res.Outcomes) != 1 || res.Outcomes[0].Status != "failed" {
		t.Fatalf("outcomes %+v", res.Outcomes)
	}
}

func TestUpdateAll_EmptyRegistry(t *testing.T) {
	reg := newReg(t, nil)
	res, err := UpdateAll(context.Background(), UpdateAllOpts{Root: reg.Root(), Registry: reg, Site: &scriptedSite{}, Fetcher: nopFetcher{}})
	if err != nil || len(res.Outcomes) != 0 {
		t.Fatalf("empty registry should be a no-op, got %+v %v", res, err)
	}
}
```

Note: these tests call `reg.Root()`. Add to `internal/registry/registry.go`:
```go
// Root returns the manga root this registry was loaded from.
func (r *Registry) Root() string { return r.root }
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/pipeline/ -run TestUpdateAll`
Expected: FAIL to compile.

- [ ] **Step 3: Implement `internal/pipeline/updateall.go`**

```go
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/registry"
	"github.com/anhpham/downloader/internal/site"
)

var ErrDomainMoved = errors.New("source host unreachable; domain may have moved")

type UpdateAllOpts struct {
	Root        string
	Registry    *registry.Registry
	Site        site.Site
	Fetcher     fetcher.Fetcher
	Concurrency int
	Logger      *log.Logger
	AskDomain   func(oldHost string, cause error) string
	Now         func() time.Time
	Runner      func(ctx context.Context, opts Opts) (Result, error)
}

type MangaOutcome struct {
	Name        string
	NewChapters int
	Status      string
	Err         error
}

type UpdateAllResult struct {
	Outcomes    []MangaOutcome
	DomainMoved bool
	NewHost     string
}

func (o *UpdateAllOpts) logf(format string, args ...any) {
	if o.Logger != nil {
		o.Logger.Printf(format, args...)
	}
}

// UpdateAll runs Resume for every registered manga, after a preflight
// that separates "Cloudflare token expired" from "site moved domains".
func UpdateAll(ctx context.Context, o UpdateAllOpts) (UpdateAllResult, error) {
	var res UpdateAllResult
	names := o.Registry.Names()
	if len(names) == 0 {
		return res, nil
	}
	now := o.Now
	if now == nil {
		now = time.Now
	}
	runner := o.Runner
	if runner == nil {
		runner = RunResult
	}

	// --- preflight on the first entry -------------------------------
	first, _ := o.Registry.Get(names[0])
	if _, err := o.Site.ListChapters(ctx, first.URL); err != nil {
		switch fetcher.Classify(err) {
		case fetcher.KindCloudflare:
			return res, err
		case fetcher.KindHostUnreachable:
			oldHost := hostOf(first.URL)
			o.logf("preflight: %s unreachable: %v", oldHost, err)
			if o.AskDomain == nil {
				return res, fmt.Errorf("%w: %v", ErrDomainMoved, err)
			}
			newHost := o.AskDomain(oldHost, err)
			if newHost == "" {
				return res, fmt.Errorf("%w: %v", ErrDomainMoved, err)
			}
			candidate, cerr := swapHost(first.URL, newHost)
			if cerr != nil {
				return res, fmt.Errorf("%w: %v", ErrDomainMoved, cerr)
			}
			if _, verr := o.Site.ListChapters(ctx, candidate); verr != nil {
				if fetcher.Classify(verr) == fetcher.KindCloudflare {
					return res, verr
				}
				return res, fmt.Errorf("%w: new host %s also failed: %v", ErrDomainMoved, newHost, verr)
			}
			if _, rerr := o.Registry.RewriteHost(newHost); rerr != nil {
				return res, rerr
			}
			if serr := o.Registry.Save(); serr != nil {
				return res, fmt.Errorf("save registry after host rewrite: %w", serr)
			}
			res.DomainMoved, res.NewHost = true, hostOf(candidate)
			o.logf("registry rewritten to host %s", res.NewHost)
		default:
			// Not a transport failure and not Cloudflare: fall through and
			// let the per-manga loop record it.
			o.logf("preflight: %s: %v", names[0], err)
		}
	}

	// --- main loop ---------------------------------------------------
	for _, name := range names {
		e, _ := o.Registry.Get(name)
		out := MangaOutcome{Name: name}
		r, err := runner(ctx, Opts{
			Mode:        Resume,
			MangaURL:    e.URL,
			Root:        o.Root,
			Name:        name,
			Concurrency: o.Concurrency,
			Site:        o.Site,
			Fetcher:     o.Fetcher,
			Logger:      o.Logger,
		})
		out.NewChapters = r.NewChapters
		switch {
		case err == nil:
			out.Status = "ok"
			o.Registry.Touch(name, now())
		case errors.Is(err, fetcher.ErrCloudflareExpired):
			out.Status, out.Err = "failed", err
			res.Outcomes = append(res.Outcomes, out)
			_ = o.Registry.Save()
			return res, err
		case errors.Is(err, ErrNoArchive):
			out.Status, out.Err = "no-archive", err
		case errors.Is(err, ErrAnotherInstance):
			out.Status, out.Err = "busy", err
		default:
			out.Status, out.Err = "failed", err
		}
		o.logf("%s: %s (+%d)", name, out.Status, out.NewChapters)
		res.Outcomes = append(res.Outcomes, out)
	}
	if err := o.Registry.Save(); err != nil {
		return res, fmt.Errorf("save registry: %w", err)
	}
	return res, nil
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return u.Host
}

func swapHost(raw, newHost string) (string, error) {
	if !containsScheme(newHost) {
		newHost = "https://" + newHost
	}
	nu, err := url.Parse(newHost)
	if err != nil || nu.Host == "" {
		return "", fmt.Errorf("invalid host %q", newHost)
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	u.Scheme, u.Host = nu.Scheme, nu.Host
	return u.String(), nil
}

func containsScheme(s string) bool {
	for i := 0; i+3 <= len(s); i++ {
		if s[i:i+3] == "://" {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/pipeline/ ./internal/registry/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/pipeline/updateall.go internal/pipeline/updateall_test.go internal/registry/registry.go
git commit -m "pipeline: UpdateAll runs Resume for every registered manga with failure classification

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 6: CLI wiring — auto-record, `register`, `list`, `update-all`, `discover`

**Files:**
- Modify: `main.go` (whole file; keep it thin, move verb handlers into `cmd_registry.go` in package main)
- Create: `cmd_registry.go` (package main)
- Create: `cmd_registry_test.go` (package main; tests the pure helpers only)

**Interfaces:**
- Consumes: `registry.Load/Upsert/Touch/Save/Names/Get`, `pipeline.UpdateAll`, `site.Site.Search`, `fetcher.HealUserAgent`, `fetcher.LoadCookieFile`, `fetcher.New`.
- Produces (package main):
  ```go
  func runRegister(args []string) int
  func runList(args []string) int
  func runUpdateAll(args []string) int
  func runDiscover(args []string) int
  func formatSummary(res pipeline.UpdateAllResult) string   // pure; tested
  func promptDomain(in io.Reader, out io.Writer) func(oldHost string, cause error) string
  ```

- [ ] **Step 1: Write the failing test for the pure helpers**

```go
package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/anhpham/downloader/internal/pipeline"
)

func TestFormatSummary(t *testing.T) {
	res := pipeline.UpdateAllResult{Outcomes: []pipeline.MangaOutcome{
		{Name: "Gintama", NewChapters: 2, Status: "ok"},
		{Name: "Naruto", Status: "no-archive", Err: pipeline.ErrNoArchive},
		{Name: "One Piece", Status: "failed", Err: errors.New("boom")},
	}}
	s := formatSummary(res)
	for _, want := range []string{"Gintama", "2", "ok", "Naruto", "no-archive", "One Piece", "failed", "boom", "new chapters: 2", "failed: 1"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary missing %q:\n%s", want, s)
		}
	}
}

func TestPromptDomain_ReadsLine(t *testing.T) {
	var out strings.Builder
	ask := promptDomain(strings.NewReader("  truyenqqnew.com \n"), &out)
	got := ask("truyenqqko.com", errors.New("no such host"))
	if got != "truyenqqnew.com" {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(out.String(), "truyenqqko.com") || !strings.Contains(out.String(), "no such host") {
		t.Fatalf("prompt must show evidence:\n%s", out.String())
	}
}

func TestPromptDomain_EmptyAborts(t *testing.T) {
	ask := promptDomain(strings.NewReader("\n"), &strings.Builder{})
	if got := ask("x", errors.New("e")); got != "" {
		t.Fatalf("expected abort, got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test . -run 'TestFormatSummary|TestPromptDomain'`
Expected: FAIL to compile.

- [ ] **Step 3: Create `cmd_registry.go`**

```go
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/mcp"
	"github.com/anhpham/downloader/internal/pipeline"
	"github.com/anhpham/downloader/internal/registry"
	sourcesite "github.com/anhpham/downloader/internal/site/source"
)

// recordRun is called by the sync-manga / resume paths after a
// successful pipeline run so the registry learns the URL.
func recordRun(root, name, mangaURL string) {
	reg, err := registry.Load(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: registry unreadable, not recording:", err)
		return
	}
	reg.Upsert(name, mangaURL)
	reg.Touch(name, time.Now())
	if err := reg.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not save registry:", err)
	}
}

// recordURL is called BEFORE a first-time sync-manga so a partial
// download is still resumable via update-all.
func recordURL(root, name, mangaURL string) {
	reg, err := registry.Load(root)
	if err != nil {
		return
	}
	if _, ok := reg.Get(name); ok {
		return
	}
	reg.Upsert(name, mangaURL)
	_ = reg.Save()
}

func runRegister(args []string) int {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
	fs.Parse(args) //nolint:errcheck
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: downloader register [--out root] <name> <manga-url>")
		return 2
	}
	name, u := fs.Arg(0), fs.Arg(1)
	if _, err := slugFromURL(u); err != nil {
		fmt.Fprintln(os.Stderr, "invalid url:", err)
		return 2
	}
	reg, err := registry.Load(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	reg.Upsert(name, u)
	if err := reg.Save(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("registered %s -> %s\n", name, u)
	return 0
}

func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
	fs.Parse(args) //nolint:errcheck
	reg, err := registry.Load(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tLAST SYNCED\tURL")
	for _, n := range reg.Names() {
		e, _ := reg.Get(n)
		last := "never"
		if !e.LastSynced.IsZero() {
			last = e.LastSynced.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", n, last, e.URL)
	}
	tw.Flush()
	return 0
}

func promptDomain(in io.Reader, out io.Writer) func(string, error) string {
	return func(oldHost string, cause error) string {
		fmt.Fprintf(out, "\nThe source host %s is unreachable:\n  %v\n", oldHost, cause)
		fmt.Fprintf(out, "This is NOT a Cloudflare/token error. If the site moved, enter the new host\n(e.g. truyenqqnew.com). Press Enter to abort: ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		return strings.TrimSpace(line)
	}
}

func formatSummary(res pipeline.UpdateAllResult) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tNEW\tSTATUS")
	total, failed := 0, 0
	for _, o := range res.Outcomes {
		detail := o.Status
		if o.Err != nil && o.Status != "ok" {
			detail += ": " + o.Err.Error()
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\n", o.Name, o.NewChapters, detail)
		total += o.NewChapters
		if o.Status == "failed" {
			failed++
		}
	}
	tw.Flush()
	if res.DomainMoved {
		fmt.Fprintf(&b, "\nregistry host rewritten to %s\n", res.NewHost)
	}
	fmt.Fprintf(&b, "\nmanga: %d  new chapters: %d  failed: %d\n", len(res.Outcomes), total, failed)
	return b.String()
}

func runUpdateAll(args []string) int {
	fs := flag.NewFlagSet("update-all", flag.ExitOnError)
	out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
	cookiesPath := fs.String("cookies", defaultCookiesPath(), "path to cookie JSON")
	concurrency := fs.Int("concurrency", 4, "chapters in flight")
	domain := fs.String("domain", "", "new source host to apply if the stored one is unreachable (skips the prompt)")
	verbose := fs.Bool("verbose", false, "per-manga progress to stderr")
	fs.Parse(args) //nolint:errcheck

	reg, err := registry.Load(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(reg.Names()) == 0 {
		fmt.Fprintln(os.Stderr, "registry is empty; run `downloader discover` or `downloader register <name> <url>` first")
		return 0
	}

	cf, err := fetcher.LoadCookieFile(*cookiesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			printCookieInstructions(*cookiesPath)
			return 2
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	f, err := fetcher.New(cf, fetcher.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	firstName := reg.Names()[0]
	first, _ := reg.Get(firstName)
	ctx := context.Background()
	if healed, changed, herr := f.HealUserAgent(ctx, first.URL); herr != nil {
		if errors.Is(herr, fetcher.ErrCloudflareExpired) {
			fmt.Fprintln(os.Stderr, "→ refresh cf_clearance in", *cookiesPath, "and re-run `update-all`")
			return 1
		}
		// A transport failure here is handled by UpdateAll's preflight; only
		// bail on non-transport errors.
		if fetcher.Classify(herr) != fetcher.KindHostUnreachable {
			fmt.Fprintln(os.Stderr, herr)
			return 1
		}
	} else if changed {
		fmt.Fprintln(os.Stderr, "user-agent drifted; healed to:", healed)
		_ = fetcher.SaveUserAgent(*cookiesPath, healed)
	}

	var logger *log.Logger
	if *verbose {
		logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	ask := promptDomain(os.Stdin, os.Stderr)
	if *domain != "" {
		d := *domain
		ask = func(string, error) string { return d }
	}

	res, err := pipeline.UpdateAll(ctx, pipeline.UpdateAllOpts{
		Root:        *out,
		Registry:    reg,
		Site:        &sourcesite.Site{Fetcher: f},
		Fetcher:     f,
		Concurrency: *concurrency,
		Logger:      logger,
		AskDomain:   ask,
	})
	fmt.Print(formatSummary(res))
	switch {
	case err == nil:
		for _, o := range res.Outcomes {
			if o.Status == "failed" {
				return 1
			}
		}
		return 0
	case errors.Is(err, fetcher.ErrCloudflareExpired):
		fmt.Fprintln(os.Stderr, "→ refresh cf_clearance in", *cookiesPath, "and re-run `update-all`")
		return 1
	case errors.Is(err, pipeline.ErrDomainMoved):
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "→ re-run with --domain <newhost> once you know the new address")
		return 1
	default:
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
}

func runDiscover(args []string) int {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
	cookiesPath := fs.String("cookies", defaultCookiesPath(), "path to cookie JSON")
	base := fs.String("site", "https://truyenqqko.com/", "any URL on the source site (used to derive the host)")
	fs.Parse(args) //nolint:errcheck

	reg, err := registry.Load(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	all, err := mcp.ListManga(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var missing []string
	for _, m := range all {
		if _, ok := reg.Get(m.Name); !ok {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) == 0 {
		fmt.Println("every archive is already registered")
		return 0
	}

	cf, err := fetcher.LoadCookieFile(*cookiesPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	f, err := fetcher.New(cf, fetcher.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	s := &sourcesite.Site{Fetcher: f}
	ctx := context.Background()
	var none []string
	for _, name := range missing {
		hits, err := s.Search(ctx, *base, name)
		if err != nil {
			if errors.Is(err, fetcher.ErrCloudflareExpired) {
				fmt.Fprintln(os.Stderr, "→ refresh cf_clearance in", *cookiesPath)
				return 1
			}
			fmt.Fprintf(os.Stderr, "%s: search failed: %v\n", name, err)
			none = append(none, name)
			continue
		}
		if len(hits) == 0 {
			none = append(none, name)
			continue
		}
		fmt.Printf("\n%s\n", name)
		for i, h := range hits {
			if i == 3 {
				break
			}
			fmt.Printf("  %d. %s\n     %s\n", i+1, h.Title, h.URL)
		}
	}
	if len(none) > 0 {
		fmt.Println("\nno search hits (paste a URL with `downloader register <name> <url>`):")
		for _, n := range none {
			fmt.Println("  " + n)
		}
	}
	fmt.Println("\nconfirm with: downloader register \"<name>\" <url>")
	return 0
}
```
Confirm `mcp.MangaEntry` has a `Name` field (`grep -n "Name " internal/mcp/manga.go`); it does per the existing `ListManga` sorting code.

- [ ] **Step 4: Wire verbs into `main.go`**

In `main()`, right after the `mcp` branch and before `parseMode`, add:
```go
	switch os.Args[1] {
	case "register":
		os.Exit(runRegister(os.Args[2:]))
	case "list":
		os.Exit(runList(os.Args[2:]))
	case "update-all":
		os.Exit(runUpdateAll(os.Args[2:]))
	case "discover":
		os.Exit(runDiscover(os.Args[2:]))
	}
```
Before the `pipeline.Run(...)` call, add:
```go
	if mode == pipeline.SyncManga {
		recordURL(*out, slug, mangaURL)
	}
```
In the `switch` on `err` after `pipeline.Run`, in the `case err == nil` branch (add one if there isn't; check the existing switch), add:
```go
		if mode == pipeline.SyncManga || mode == pipeline.Resume {
			recordRun(*out, slug, mangaURL)
		}
```
Update `usage()` text to add:
```
  downloader update-all    [flags]               pull new chapters for every registered manga (uses resume)
  downloader discover      [flags]               propose source URLs for archives missing from the registry
  downloader register      <name> <manga-url>    add/fix a registry entry
  downloader list                                show registered manga
```
and under flags:
```
  --domain string        (update-all) new source host to apply if the stored one is unreachable
  --site string          (discover) any URL on the source site (default https://truyenqqko.com/)
```

- [ ] **Step 5: Build and test**

Run: `go build -o bin/downloader . && go test ./...`
Expected: PASS. Then smoke: `./bin/downloader list --out /tmp/emptyroot` prints just the header; `./bin/downloader register --out /tmp/emptyroot X https://truyenqqko.com/truyen-tranh/x-1 && ./bin/downloader list --out /tmp/emptyroot` shows X.

- [ ] **Step 6: Commit**

```bash
git add main.go cmd_registry.go cmd_registry_test.go
git commit -m "cli: register/list/discover/update-all verbs; auto-record URLs after sync

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 7: MCP — record URLs, expose them, add `update_all` tool

**Files:**
- Modify: `internal/mcp/manga.go` (`MangaEntry` gains `URL`, `LastSynced`; `ListManga`/`InspectManga` fill them from the registry)
- Modify: `internal/mcp/sync.go` (upsert registry after a successful `sync_manga`/`resume`)
- Modify: `internal/mcp/tools.go` (register `update_all`)
- Modify: `internal/mcp/manga_test.go`, `internal/mcp/sync_test.go` (add assertions)

**Interfaces:**
- Consumes: `registry`, `pipeline.UpdateAll`.
- Produces:
  ```go
  // MangaEntry additions
  URL        string `json:"url,omitempty"`
  LastSynced string `json:"last_synced,omitempty"` // RFC3339

  type UpdateAllInput struct {
      Domain      string `json:"domain,omitempty" jsonschema:"New source host to apply if the stored one is unreachable"`
      Concurrency int    `json:"concurrency,omitempty"`
  }
  type UpdateAllOutput struct {
      Outcomes    []UpdateAllOutcome `json:"outcomes"`
      DomainMoved bool               `json:"domain_moved"`
      NewHost     string             `json:"new_host,omitempty"`
  }
  type UpdateAllOutcome struct {
      Name        string `json:"name"`
      NewChapters int    `json:"new_chapters"`
      Status      string `json:"status"`
      Error       string `json:"error,omitempty"`
  }
  ```
  MCP error mapping: `pipeline.ErrDomainMoved` → new code `CodeDomainMoved = "DOMAIN_MOVED"` with message "source host unreachable and no `domain` given; ask the user for the new host and call update_all again with `domain`".

- [ ] **Step 1: Write failing tests**

In `internal/mcp/manga_test.go` add:
```go
func TestListManga_IncludesRegistryURL(t *testing.T) {
	root := t.TempDir()
	// create an empty but valid cbz named A.cbz
	writeEmptyCBZ(t, filepath.Join(root, "A.cbz"))
	reg, _ := registry.Load(root)
	reg.Upsert("A", "https://truyenqqko.com/truyen-tranh/a-1")
	reg.Touch("A", time.Date(2026, 9, 5, 1, 2, 3, 0, time.UTC))
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}
	items, err := ListManga(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].URL != "https://truyenqqko.com/truyen-tranh/a-1" || items[0].LastSynced != "2026-09-05T01:02:03Z" {
		t.Fatalf("items %+v", items)
	}
}
```
Look in `manga_test.go` for an existing helper that creates a `.cbz` (grep `zip.NewWriter`); reuse it. If none exists, add:
```go
func writeEmptyCBZ(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("chap-0001/.ok")
	w.Write(nil)
	zw.Close()
	f.Close()
}
```

In `internal/mcp/sync_test.go` add (following the existing test that injects `runFn`):
```go
func TestSyncExecutor_RecordsRegistry(t *testing.T) {
	root := t.TempDir()
	cookies := writeTestCookies(t) // reuse the existing helper name in this file; grep for how other tests build CookiesPath
	e := &SyncExecutor{Root: root, CookiesPath: cookies, RunState: NewRunState(),
		runFn: func(context.Context, pipeline.Opts) error { return nil }}
	if _, err := e.Run(context.Background(), pipeline.Resume, SyncInput{URL: "https://truyenqqko.com/truyen-tranh/gintama-216"}); err != nil {
		t.Fatal(err)
	}
	reg, _ := registry.Load(root)
	en, ok := reg.Get("gintama-216")
	if !ok || en.URL != "https://truyenqqko.com/truyen-tranh/gintama-216" || en.LastSynced.IsZero() {
		t.Fatalf("registry not recorded: %+v ok=%v", en, ok)
	}
}
```
Adjust `writeTestCookies` / `NewRunState` to whatever helper names the file already uses (read the file first).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/mcp/ -run 'TestListManga_IncludesRegistryURL|TestSyncExecutor_RecordsRegistry'`
Expected: FAIL to compile.

- [ ] **Step 3: Implement**

`manga.go`: add fields to `MangaEntry`; in `ListManga` and `InspectManga`, after building entries, load the registry once and fill:
```go
	reg, rerr := registry.Load(root)
	if rerr == nil {
		for i := range out {
			if e, ok := reg.Get(out[i].Name); ok {
				out[i].URL = e.URL
				if !e.LastSynced.IsZero() {
					out[i].LastSynced = e.LastSynced.UTC().Format(time.RFC3339)
				}
			}
		}
	}
```
(For `InspectManga`, do the same on the single entry.)

`sync.go`: after `runner(runCtx, opts)` returns nil and `mode != pipeline.SyncComments`:
```go
	if reg, rerr := registry.Load(e.Root); rerr == nil {
		reg.Upsert(name, in.URL)
		reg.Touch(name, time.Now())
		_ = reg.Save()
	}
```

`errors.go`: add `CodeDomainMoved = "DOMAIN_MOVED"` and a `case errors.Is(err, pipeline.ErrDomainMoved)` in `MapError`.

`tools.go`: in `registerSyncTools`, add:
```go
	sdk.AddTool(s.sdk,
		&sdk.Tool{
			Name:        "update_all",
			Description: "Pull new chapters for every registered manga (runs resume per manga). If it returns DOMAIN_MOVED, ask the user for the site's new host and call again with `domain`." + descSuffix,
		},
		func(ctx context.Context, req *sdk.CallToolRequest, in UpdateAllInput) (*sdk.CallToolResult, UpdateAllOutput, error) {
			out, err := s.updateAll(ctx, in)
			if err != nil {
				return toolErr(MapError(err)), UpdateAllOutput{}, nil
			}
			return nil, out, nil
		},
	)
```
and in `sync.go`:
```go
func (s *Server) updateAll(ctx context.Context, in UpdateAllInput) (UpdateAllOutput, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, err := s.exec.RunState.Acquire("*", "update-all", cancel); err != nil {
		return UpdateAllOutput{}, err
	}
	defer s.exec.RunState.Release()

	reg, err := registry.Load(s.opts.Root)
	if err != nil {
		return UpdateAllOutput{}, err
	}
	cf, err := fetcher.LoadCookieFile(s.exec.CookiesPath)
	if err != nil {
		return UpdateAllOutput{}, &ToolError{Code: CodeBadInput, Message: "cookie file unreadable; call update_cookie first", Cause: err}
	}
	f, err := fetcher.New(cf, fetcher.Options{})
	if err != nil {
		return UpdateAllOutput{}, err
	}
	var ask func(string, error) string
	if in.Domain != "" {
		ask = func(string, error) string { return in.Domain }
	}
	res, err := pipeline.UpdateAll(runCtx, pipeline.UpdateAllOpts{
		Root: s.opts.Root, Registry: reg, Site: &sourcesite.Site{Fetcher: f}, Fetcher: f,
		Concurrency: defaultConcurrency(in.Concurrency), AskDomain: ask,
	})
	out := UpdateAllOutput{DomainMoved: res.DomainMoved, NewHost: res.NewHost}
	for _, o := range res.Outcomes {
		oo := UpdateAllOutcome{Name: o.Name, NewChapters: o.NewChapters, Status: o.Status}
		if o.Err != nil {
			oo.Error = o.Err.Error()
		}
		out.Outcomes = append(out.Outcomes, oo)
	}
	return out, err
}
```
Read `server.go` first to learn the actual field names for the executor and options on `Server` (`grep -n "exec\|opts" internal/mcp/server.go`) and adjust `s.exec` / `s.opts` accordingly.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/mcp/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp
git commit -m "mcp: expose registry URLs in list_manga, record after sync, add update_all tool

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```

---

### Task 8: Docs

**Files:**
- Modify: `README.md` (new section "Keeping everything up to date")
- Modify: `AGENTS.md` (registry facts under "What's already settled" and "Architecture seams")

- [ ] **Step 1: README**

Add after the existing usage section:

```markdown
## Keeping everything up to date

The downloader remembers where each archive came from in
`~/Documents/Manga/.registry.json`. Every successful `sync-manga` or
`resume` records the URL automatically.

    downloader list                  # what is registered, and when it last synced
    downloader update-all            # resume every registered manga (new chapters only)
    downloader register "Name" URL   # add or fix one entry by hand
    downloader discover              # for archives with no entry: search the site and print candidate URLs

If the source site changes domain, `update-all` detects that the old
host is unreachable (DNS / connection / TLS failure, **not** a
Cloudflare 403) and asks for the new host, then rewrites every stored
URL. Non-interactive: `downloader update-all --domain newhost.com`.

A Cloudflare 403 is always reported as an expired `cf_clearance`
token and never triggers the domain prompt.
```

- [ ] **Step 2: AGENTS.md**

Under "What's already settled" add:
```markdown
- **Registry is the source of truth for manga URLs.** `<root>/.registry.json`
  (`internal/registry`). `update-all` never scans archives for URLs;
  an archive without an entry is invisible to it until `register`ed.
- **Domain-moved vs token-expired is decided by `fetcher.Classify`.**
  403 → Cloudflare, always. DNS/connect/TLS → host unreachable. Only the
  latter may prompt for a new domain. Don't merge these paths.
```
Under "Architecture seams" add:
```markdown
- **`site.Site.Search`** — title search via `POST /frontend/search/search`
  (form `search`, `type=0`). Used only by `discover`.
- **`pipeline.UpdateAll`** — iterates the registry running `Resume`;
  injectable `Runner`/`AskDomain` for tests.
```

- [ ] **Step 3: Commit**

```bash
git add README.md AGENTS.md
git commit -m "docs: registry, update-all, discover, domain-move handling

Co-Authored-By: Claude Fable 5.1 <noreply@anthropic.com>"
```
