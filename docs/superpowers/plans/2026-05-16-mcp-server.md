# MCP Server Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a local MCP server (stdio) bundled as a `mcp` subcommand of `bin/downloader`, exposing every CLI capability to Claude Desktop plus cookie management and run-state control.

**Architecture:** Single Go binary. New `internal/mcp/` package wraps the official `github.com/modelcontextprotocol/go-sdk`. Each tool family (cookies, manga, sync) lives in its own file; `server.go` is the SDK seam; `runstate.go` enforces one-sync-at-a-time; `errors.go` defines structured error codes. The existing `internal/pipeline`, `internal/fetcher`, `internal/archive`, and `internal/site/source` packages are reused without changes.

**Tech Stack:** Go 1.21+, `github.com/modelcontextprotocol/go-sdk`, existing `github.com/anhpham/downloader/internal/*` packages.

**Spec:** `docs/superpowers/specs/2026-05-16-mcp-server-design.md`

---

## Files (created or modified)

- Create: `internal/mcp/server.go`
- Create: `internal/mcp/server_test.go`
- Create: `internal/mcp/errors.go`
- Create: `internal/mcp/errors_test.go`
- Create: `internal/mcp/cookies.go`
- Create: `internal/mcp/cookies_test.go`
- Create: `internal/mcp/manga.go`
- Create: `internal/mcp/manga_test.go`
- Create: `internal/mcp/runstate.go`
- Create: `internal/mcp/runstate_test.go`
- Create: `internal/mcp/sync.go`
- Create: `internal/mcp/sync_test.go`
- Create: `internal/mcp/tools.go`
- Create: `internal/mcp/integration_test.go`
- Modify: `main.go` — add `mcp` subcommand dispatch
- Modify: `go.mod` / `go.sum` — add SDK dependency
- Modify: `README.md` — add Claude Desktop section

---

## Task 1: SDK dependency + `mcp` subcommand skeleton

**Files:**
- Create: `internal/mcp/server.go`
- Create: `internal/mcp/server_test.go`
- Modify: `main.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add SDK to go.mod**

Run:

```bash
go get github.com/modelcontextprotocol/go-sdk@latest
go mod tidy
```

Verify the import path resolves by running `go list github.com/modelcontextprotocol/go-sdk/mcp`.

- [ ] **Step 2: Write the failing server test**

Create `internal/mcp/server_test.go`:

```go
package mcp

import (
	"context"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestServer_Initialize asserts the server starts, advertises its
// implementation name, and lists the registered tools.
//
// Initialization happens inside ClientSession.Connect — there is no
// separate Initialize() call. ServerInfo is reachable via
// sess.InitializeResult().
func TestServer_Initialize(t *testing.T) {
	srv, sess := newTestPair(t)

	init := sess.InitializeResult()
	if init == nil {
		t.Fatal("no InitializeResult after Connect")
	}
	if init.ServerInfo.Name != "manga-downloader" {
		t.Fatalf("server name = %q, want %q", init.ServerInfo.Name, "manga-downloader")
	}
	tools, err := sess.ListTools(context.Background(), &sdk.ListToolsParams{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 0 {
		t.Fatalf("expected 0 tools at scaffold stage, got %d", len(tools.Tools))
	}
	_ = srv
}

// newTestPair wires an in-memory client to a fresh server using the
// SDK's documented Connect/Connect pair. Both sides are non-blocking;
// no goroutine to manage.
func newTestPair(t *testing.T) (*Server, *sdk.ClientSession) {
	t.Helper()
	opts := Opts{
		Root:        t.TempDir(),
		CookiesPath: t.TempDir() + "/cookies.json",
	}
	srv, err := New(opts)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	clientT, serverT := sdk.NewInMemoryTransports()
	if _, err := srv.sdk.Connect(context.Background(), serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	c := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "0"}, nil)
	sess, err := c.Connect(context.Background(), clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return srv, sess
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./internal/mcp/...`
Expected: FAIL — package `internal/mcp` does not exist yet.

- [ ] **Step 4: Implement minimal server**

Create `internal/mcp/server.go`:

```go
// Package mcp wraps the official Model Context Protocol Go SDK to
// expose downloader operations to Claude Desktop over stdio.
package mcp

import (
	"context"
	"log"
	"os"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Opts configures the server. Defaults are filled in by the CLI.
type Opts struct {
	Root        string // manga directory (.cbz live here)
	CookiesPath string // cookies.json
	Logger      *log.Logger
}

// Server is the long-lived MCP server. It owns the SDK server,
// the run-state singleton, and the logger.
type Server struct {
	opts Opts
	sdk  *sdk.Server
	log  *log.Logger
}

func New(opts Opts) (*Server, error) {
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stderr, "mcp: ", log.LstdFlags)
	}
	s := &Server{opts: opts, log: opts.Logger}
	s.sdk = sdk.NewServer(&sdk.Implementation{
		Name:    "manga-downloader",
		Version: "0.1.0",
	}, nil)
	return s, nil
}

// Serve runs the server on stdio until the context is cancelled or
// the peer disconnects.
func (s *Server) Serve(ctx context.Context) error {
	return s.sdk.Run(ctx, &sdk.StdioTransport{})
}
```

- [ ] **Step 5: Wire the `mcp` subcommand in main.go**

In `main.go`, before the existing mode dispatch, add a case for `mcp`. The existing `parseMode` returns one of the three sync modes; `mcp` is a different beast and is handled before that switch.

```go
// near the top of main(), after the len(os.Args) check:
if os.Args[1] == "mcp" {
	if err := runMCP(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return
}
```

And add:

```go
func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	root := fs.String("out", defaultOutDir(), "manga root directory")
	cookies := fs.String("cookies", defaultCookiesPath(), "path to cookies.json")
	verbose := fs.Bool("verbose", false, "verbose logging to stderr")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var logger *log.Logger
	if *verbose {
		logger = log.New(os.Stderr, "mcp: ", log.LstdFlags)
	} else {
		logger = log.New(io.Discard, "", 0)
	}
	srv, err := mcp.New(mcp.Opts{
		Root:        *root,
		CookiesPath: *cookies,
		Logger:      logger,
	})
	if err != nil {
		return err
	}
	return srv.Serve(context.Background())
}
```

Add the imports: `"io"`, `"github.com/anhpham/downloader/internal/mcp"`. Reuse the existing `defaultOutDir()` (`main.go:137`) and `defaultCookiesPath()` (`main.go:144`) helpers.

Also update the `usage()` text to list `mcp` alongside the three sync subcommands.

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/mcp/...`
Expected: PASS — initialize returns name `manga-downloader`, zero tools listed.

- [ ] **Step 7: Verify build + vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum main.go internal/mcp/server.go internal/mcp/server_test.go
git commit -m "mcp: scaffold stdio server + mcp subcommand"
```

---

## Task 2: Structured error codes

**Files:**
- Create: `internal/mcp/errors.go`
- Create: `internal/mcp/errors_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/errors_test.go`:

```go
package mcp

import (
	"errors"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
)

func TestMapError_CFExpired(t *testing.T) {
	te := MapError(fetcher.ErrCloudflareExpired)
	if te.Code != CodeCFTokenExpired {
		t.Fatalf("code = %q, want %q", te.Code, CodeCFTokenExpired)
	}
	if te.Message == "" {
		t.Fatal("message must guide the caller to update_cookie")
	}
}

func TestMapError_Generic(t *testing.T) {
	te := MapError(errors.New("boom"))
	if te.Code != CodeInternal {
		t.Fatalf("code = %q, want INTERNAL", te.Code)
	}
}

func TestMapError_Nil(t *testing.T) {
	if MapError(nil) != nil {
		t.Fatal("MapError(nil) must return nil")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestMapError`
Expected: FAIL — `MapError` / `CodeCFTokenExpired` not defined.

- [ ] **Step 3: Implement errors.go**

Create `internal/mcp/errors.go`:

```go
package mcp

import (
	"errors"
	"fmt"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/pipeline"
)

// Error codes surfaced through MCP. Clients (Claude) key on these.
const (
	CodeCFTokenExpired = "CF_TOKEN_EXPIRED"
	CodeRunInProgress  = "RUN_IN_PROGRESS"
	CodeNoArchive      = "NO_ARCHIVE"
	CodeBadInput       = "BAD_INPUT"
	CodeInternal       = "INTERNAL"
)

// ToolError is a structured error returned from any tool handler.
// The MCP SDK surfaces the Message; Code lives in the data block so
// Claude can branch on it.
type ToolError struct {
	Code    string
	Message string
	Cause   error
}

func (e *ToolError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *ToolError) Unwrap() error { return e.Cause }

// MapError converts an internal error into a ToolError with the
// right code. nil passes through.
func MapError(err error) *ToolError {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, fetcher.ErrCloudflareExpired):
		return &ToolError{
			Code:    CodeCFTokenExpired,
			Message: "cf_clearance is invalid or expired. Ask the user for a fresh value (DevTools → Application → Cookies → cf_clearance), then call update_cookie before retrying this tool.",
			Cause:   err,
		}
	case errors.Is(err, pipeline.ErrAnotherInstance):
		return &ToolError{
			Code:    CodeRunInProgress,
			Message: "another sync is in progress against this archive",
			Cause:   err,
		}
	case errors.Is(err, pipeline.ErrNoArchive):
		return &ToolError{
			Code:    CodeNoArchive,
			Message: "no archive found for this manga; use sync_manga to create one",
			Cause:   err,
		}
	default:
		return &ToolError{Code: CodeInternal, Message: err.Error(), Cause: err}
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestMapError`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/errors.go internal/mcp/errors_test.go
git commit -m "mcp: structured error codes + MapError"
```

---

## Task 3: Cookie file read/write helpers

**Files:**
- Create: `internal/mcp/cookies.go`
- Create: `internal/mcp/cookies_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/cookies_test.go`:

```go
package mcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
)

func TestUpdateClearance_PreservesOtherFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")

	seed := fetcher.CookieFile{
		UserAgent: "Mozilla/5.0 (existing)",
		Cookies: []fetcher.CookieRecord{
			{Name: "cf_clearance", Value: "OLD", Domain: ".<source-site>"},
			{Name: "other", Value: "keep", Domain: ".<source-site>"},
		},
	}
	mustWriteJSON(t, path, seed)

	if err := UpdateClearance(path, "NEW", "", ""); err != nil {
		t.Fatal(err)
	}

	got := mustReadJSON(t, path)
	if got.UserAgent != "Mozilla/5.0 (existing)" {
		t.Fatalf("UA = %q, want preserved", got.UserAgent)
	}
	if len(got.Cookies) != 2 {
		t.Fatalf("cookies = %d, want 2", len(got.Cookies))
	}
	if val := findCookie(got, "cf_clearance"); val != "NEW" {
		t.Fatalf("cf_clearance = %q, want NEW", val)
	}
	if val := findCookie(got, "other"); val != "keep" {
		t.Fatalf("other cookie lost: %q", val)
	}
}

func TestUpdateClearance_CreatesFileWhenMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	if err := UpdateClearance(path, "FRESH", "ua/1.0", ".example.com"); err != nil {
		t.Fatal(err)
	}
	got := mustReadJSON(t, path)
	if got.UserAgent != "ua/1.0" {
		t.Fatalf("UA = %q, want ua/1.0", got.UserAgent)
	}
	if findCookie(got, "cf_clearance") != "FRESH" {
		t.Fatal("cf_clearance not set")
	}
	if got.Cookies[0].Domain != ".example.com" {
		t.Fatalf("domain = %q, want .example.com", got.Cookies[0].Domain)
	}
}

func TestUpdateClearance_RejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	err := UpdateClearance(path, "   ", "", "")
	if err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("err = %v, want empty-value rejection", err)
	}
}

func TestCookieStatus(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	seed := fetcher.CookieFile{
		Cookies: []fetcher.CookieRecord{{Name: "cf_clearance", Value: "abcdefghIJKLMNOP", Domain: ".x"}},
	}
	mustWriteJSON(t, path, seed)
	st, err := CookieStatus(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st.HasClearance {
		t.Fatal("expected HasClearance = true")
	}
	if st.Last8 != "IJKLMNOP" {
		t.Fatalf("Last8 = %q", st.Last8)
	}
}

func TestCookieStatus_MissingFile(t *testing.T) {
	st, err := CookieStatus(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if st.HasClearance {
		t.Fatal("missing file must report HasClearance = false")
	}
}

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustReadJSON(t *testing.T, path string) fetcher.CookieFile {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cf fetcher.CookieFile
	if err := json.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	return cf
}

func findCookie(cf fetcher.CookieFile, name string) string {
	for _, c := range cf.Cookies {
		if c.Name == name {
			return c.Value
		}
	}
	return ""
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run "Update|Cookie"`
Expected: FAIL — `UpdateClearance` / `CookieStatus` undefined.

- [ ] **Step 3: Implement cookies.go**

Create `internal/mcp/cookies.go`:

```go
package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
)

const defaultCookieDomain = ".<source-site>"

// CookieStatusResult is what the get_cookie_status tool returns.
type CookieStatusResult struct {
	CookiePath   string `json:"cookie_path"`
	HasClearance bool   `json:"has_clearance"`
	Mtime        string `json:"mtime,omitempty"`
	Last8        string `json:"last8,omitempty"`
}

// CookieStatus inspects the cookie file without leaking the full
// cf_clearance value. A missing file is not an error — it reports
// HasClearance=false.
func CookieStatus(path string) (CookieStatusResult, error) {
	out := CookieStatusResult{CookiePath: path}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	cf, err := loadCookieFile(path)
	if err != nil {
		return out, err
	}
	for _, c := range cf.Cookies {
		if c.Name == "cf_clearance" && c.Value != "" {
			out.HasClearance = true
			if len(c.Value) >= 8 {
				out.Last8 = c.Value[len(c.Value)-8:]
			} else {
				out.Last8 = c.Value
			}
			break
		}
	}
	out.Mtime = info.ModTime().UTC().Format(time.RFC3339)
	return out, nil
}

// UpdateClearance writes a new cf_clearance value while preserving
// every other field of the cookie file. Empty/whitespace values are
// rejected. The write is crash-safe (temp file + rename).
func UpdateClearance(path, value, userAgent, domain string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("cf_clearance value is empty")
	}
	cf, err := loadOrInit(path)
	if err != nil {
		return err
	}
	if userAgent != "" {
		cf.UserAgent = userAgent
	}
	if cf.UserAgent == "" {
		cf.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	}
	setOrAppend(cf, "cf_clearance", value, pickDomain(cf, domain))
	return writeAtomic(path, cf)
}

func pickDomain(cf *fetcher.CookieFile, requested string) string {
	if requested != "" {
		return requested
	}
	for _, c := range cf.Cookies {
		if c.Name == "cf_clearance" && c.Domain != "" {
			return c.Domain
		}
	}
	return defaultCookieDomain
}

// setOrAppend updates the value of the first cookie with this name,
// or appends a new entry. When updating an existing entry, the
// requested `domain` overwrites the stored one — this lets callers
// fix a wrong-domain cookie via update_cookie, not just rotate the
// value. (Source sites rebrand occasionally; the user shouldn't have
// to hand-edit cookies.json for that.)
func setOrAppend(cf *fetcher.CookieFile, name, value, domain string) {
	for i := range cf.Cookies {
		if cf.Cookies[i].Name == name {
			cf.Cookies[i].Value = value
			if domain != "" {
				cf.Cookies[i].Domain = domain
			}
			return
		}
	}
	cf.Cookies = append(cf.Cookies, fetcher.CookieRecord{Name: name, Value: value, Domain: domain})
}

func loadOrInit(path string) (*fetcher.CookieFile, error) {
	cf, err := loadCookieFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &fetcher.CookieFile{}, nil
	}
	return cf, err
}

func loadCookieFile(path string) (*fetcher.CookieFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cf fetcher.CookieFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cf, nil
}

func writeAtomic(path string, cf *fetcher.CookieFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cf); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run "Update|Cookie"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/cookies.go internal/mcp/cookies_test.go
git commit -m "mcp: atomic cookie writer + CookieStatus"
```

---

## Task 4: Register `update_cookie` + `get_cookie_status` tools

**Files:**
- Create: `internal/mcp/tools.go`
- Modify: `internal/mcp/server.go`
- Create: `internal/mcp/integration_test.go`

- [ ] **Step 1: Write the failing integration test**

Create `internal/mcp/integration_test.go`:

```go
package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTool_UpdateCookieAndStatus(t *testing.T) {
	_, client := newTestPair(t)

	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "update_cookie",
		Arguments: map[string]any{
			"value":  "ABCDEFGHIJKLMNOP",
			"domain": ".example.com",
		},
	})
	if err != nil {
		t.Fatalf("update_cookie: %v", err)
	}
	if res.IsError {
		t.Fatalf("update_cookie returned error: %v", contentText(res))
	}

	got, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name: "get_cookie_status",
	})
	if err != nil {
		t.Fatalf("get_cookie_status: %v", err)
	}
	var out CookieStatusResult
	mustUnmarshalStructured(t, got, &out)
	if !out.HasClearance {
		t.Fatal("expected HasClearance=true after update_cookie")
	}
	if out.Last8 != "IJKLMNOP" {
		t.Fatalf("Last8 = %q", out.Last8)
	}
	if !strings.HasSuffix(out.CookiePath, "cookies.json") {
		t.Fatalf("cookie_path = %q", out.CookiePath)
	}
}

func TestTool_UpdateCookieRejectsEmpty(t *testing.T) {
	_, client := newTestPair(t)
	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "update_cookie",
		Arguments: map[string]any{"value": ""},
	})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected IsError=true on empty value")
	}
	// Two channels for the code so Claude can branch deterministically
	// without parsing free-form text: Meta.error_code, plus the body.
	if got := res.Meta["error_code"]; got != CodeBadInput {
		t.Fatalf("Meta.error_code = %v, want %q", got, CodeBadInput)
	}
	if !strings.Contains(contentText(res), CodeBadInput) {
		t.Fatalf("error text %q must contain code %q", contentText(res), CodeBadInput)
	}
}

func contentText(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if t, ok := c.(*sdk.TextContent); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

// mustUnmarshalStructured round-trips res.StructuredContent through
// JSON into dst. The SDK field is `any` containing the raw Output
// struct the handler returned; marshalling it produces the same JSON
// the wire would carry. If a future SDK version wraps this in a
// container type, drill into the wrapper's exported value field here.
func mustUnmarshalStructured(t *testing.T, res *sdk.CallToolResult, dst any) {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatalf("no structured content; got text: %s", contentText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("unmarshal %s into %T: %v", b, dst, err)
	}
}
```

Update `newTestPair` in `server_test.go` to register tools — add a single line at the end of `New()` (next step) so the helper just works.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestTool_Update`
Expected: FAIL — tools not registered.

- [ ] **Step 3: Implement tools.go with cookie tools**

Create `internal/mcp/tools.go`:

```go
package mcp

import (
	"context"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// register attaches every tool to the SDK server. Called from New().
func (s *Server) register() {
	s.registerCookieTools()
}

// --- update_cookie -----------------------------------------------------------

type UpdateCookieInput struct {
	Value     string `json:"value" jsonschema:"required,description=The cf_clearance value the user copied from their browser DevTools"`
	UserAgent string `json:"user_agent,omitempty" jsonschema:"description=Optional updated User-Agent (paste the value of navigator.userAgent)"`
	Domain    string `json:"domain,omitempty" jsonschema:"description=Optional cookie domain (defaults to the existing entry's domain or .<source-site>)"`
}

type UpdateCookieOutput struct {
	OK         bool   `json:"ok"`
	CookiePath string `json:"cookie_path"`
}

func (s *Server) registerCookieTools() {
	sdk.AddTool(s.sdk,
		&sdk.Tool{
			Name:        "update_cookie",
			Description: "Update the cf_clearance cookie used to bypass Cloudflare. Call this when a sync tool returns CF_TOKEN_EXPIRED. Ask the user to copy the fresh value from DevTools → Application → Cookies → cf_clearance.",
		},
		func(ctx context.Context, req *sdk.CallToolRequest, in UpdateCookieInput) (*sdk.CallToolResult, UpdateCookieOutput, error) {
			if err := UpdateClearance(s.opts.CookiesPath, in.Value, in.UserAgent, in.Domain); err != nil {
				return toolErr(&ToolError{Code: CodeBadInput, Message: err.Error(), Cause: err}), UpdateCookieOutput{}, nil
			}
			return nil, UpdateCookieOutput{OK: true, CookiePath: s.opts.CookiesPath}, nil
		},
	)

	sdk.AddTool(s.sdk,
		&sdk.Tool{
			Name:        "get_cookie_status",
			Description: "Report whether cookies.json contains a cf_clearance value, when it was last modified, and the last 8 characters (for confirmation only — never the full token).",
		},
		func(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, CookieStatusResult, error) {
			out, err := CookieStatus(s.opts.CookiesPath)
			if err != nil {
				return toolErr(MapError(err)), CookieStatusResult{}, nil
			}
			return nil, out, nil
		},
	)
}

// toolErr converts a ToolError into a CallToolResult with
// IsError=true. The code goes in BOTH the text body (so a human
// reading transcripts sees it) and the Meta map (so Claude can
// branch deterministically without parsing the text).
func toolErr(te *ToolError) *sdk.CallToolResult {
	if te == nil {
		return nil
	}
	return &sdk.CallToolResult{
		IsError: true,
		Content: []sdk.Content{
			&sdk.TextContent{Text: te.Error()},
		},
		Meta: map[string]any{"error_code": te.Code},
	}
}
```

In `server.go`, append `s.register()` at the end of `New()` before `return s, nil`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/...`
Expected: PASS (existing tests still green, two new ones pass).

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/server.go internal/mcp/integration_test.go
git commit -m "mcp: register update_cookie + get_cookie_status tools"
```

---

## Task 5: Manga inventory helpers

**Files:**
- Create: `internal/mcp/manga.go`
- Create: `internal/mcp/manga_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/manga_test.go`:

```go
package mcp

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/anhpham/downloader/internal/layout"
)

func TestListManga_EmptyRoot(t *testing.T) {
	got, err := ListManga(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 entries, got %d", len(got))
	}
}

func TestListAndInspect(t *testing.T) {
	root := t.TempDir()
	writeFakeArchive(t, root, "Foo.cbz", []string{
		"chap-001/001.jpg",
		"chap-001/" + layout.CommentsFilename,
		"chap-002/001.jpg",
	})
	writeFakeArchive(t, root, "Bar.cbz", []string{
		"chap-001/001.jpg",
	})

	list, err := ListManga(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2, got %d", len(list))
	}

	insp, err := InspectManga(root, "Foo")
	if err != nil {
		t.Fatal(err)
	}
	if insp.Chapters != 2 {
		t.Fatalf("chapters = %d, want 2", insp.Chapters)
	}
	if insp.CommentsAttached != 1 {
		t.Fatalf("commented = %d, want 1", insp.CommentsAttached)
	}
	if insp.MissingComments != 1 {
		t.Fatalf("missing = %d, want 1", insp.MissingComments)
	}

	if _, err := InspectManga(root, "Nonesuch"); err == nil {
		t.Fatal("expected NO_ARCHIVE for missing manga")
	}
}

func writeFakeArchive(t *testing.T, root, name string, entries []string) {
	t.Helper()
	f, err := os.Create(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := zip.NewWriter(f)
	for _, e := range entries {
		fw, err := w.Create(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run "ListManga|ListAndInspect"`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement manga.go**

Create `internal/mcp/manga.go`:

```go
package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anhpham/downloader/internal/archive"
	"github.com/anhpham/downloader/internal/pipeline"
)

type MangaEntry struct {
	Name             string `json:"name"`
	Path             string `json:"path"`
	SizeBytes        int64  `json:"size_bytes"`
	Chapters         int    `json:"chapters"`
	CommentsAttached int    `json:"comments_attached"`
	MissingComments  int    `json:"missing_comments"`
	ArchiveWidth     int    `json:"archive_width,omitempty"`
}

// ListManga walks root and returns one entry per *.cbz, sorted by name.
func ListManga(root string) ([]MangaEntry, error) {
	dirEntries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []MangaEntry
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".cbz") {
			continue
		}
		name := strings.TrimSuffix(de.Name(), ".cbz")
		entry, err := buildEntry(root, name)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// InspectManga returns the entry for a single manga by name (without
// the .cbz suffix). Returns pipeline.ErrNoArchive if not found.
func InspectManga(root, name string) (MangaEntry, error) {
	path := filepath.Join(root, name+".cbz")
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return MangaEntry{}, pipeline.ErrNoArchive
	}
	if err != nil {
		return MangaEntry{}, err
	}
	_ = info
	return buildEntry(root, name)
}

func buildEntry(root, name string) (MangaEntry, error) {
	path := filepath.Join(root, name+".cbz")
	info, err := os.Stat(path)
	if err != nil {
		return MangaEntry{}, err
	}
	insp, err := archive.Inspect(path)
	if err != nil {
		return MangaEntry{}, err
	}
	chapters := len(insp.Have)
	commented := 0
	for folder := range insp.Have {
		if insp.HaveComments[folder] {
			commented++
		}
	}
	return MangaEntry{
		Name:             name,
		Path:             path,
		SizeBytes:        info.Size(),
		Chapters:         chapters,
		CommentsAttached: commented,
		MissingComments:  chapters - commented,
		ArchiveWidth:     insp.InferredWidth(),
	}, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run "ListManga|ListAndInspect"`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/manga.go internal/mcp/manga_test.go
git commit -m "mcp: ListManga + InspectManga over archive.Inspect"
```

---

## Task 6: Register `list_manga` + `inspect_manga` tools

**Files:**
- Modify: `internal/mcp/tools.go`
- Modify: `internal/mcp/integration_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/mcp/integration_test.go`:

```go
func TestTool_ListAndInspect(t *testing.T) {
	srv, client := newTestPair(t)
	writeFakeArchive(t, srv.opts.Root, "Foo.cbz", []string{"chap-001/001.jpg"})

	listRes, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "list_manga"})
	if err != nil {
		t.Fatal(err)
	}
	var list []MangaEntry
	mustUnmarshalStructuredAs(t, listRes, &struct {
		Items *[]MangaEntry `json:"items"`
	}{Items: &list})
	if len(list) != 1 || list[0].Name != "Foo" {
		t.Fatalf("list = %+v", list)
	}

	inspRes, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "inspect_manga",
		Arguments: map[string]any{"name": "Foo"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var entry MangaEntry
	mustUnmarshalStructured(t, inspRes, &entry)
	if entry.Chapters != 1 {
		t.Fatalf("chapters = %d", entry.Chapters)
	}

	missing, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "inspect_manga",
		Arguments: map[string]any{"name": "Nope"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !missing.IsError || missing.Meta["error_code"] != CodeNoArchive {
		t.Fatalf("expected NO_ARCHIVE, got IsError=%v meta=%v text=%q",
			missing.IsError, missing.Meta["error_code"], contentText(missing))
	}
}

func mustUnmarshalStructuredAs(t *testing.T, res *sdk.CallToolResult, wrapper any) {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatalf("no structured content; text: %s", contentText(res))
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, wrapper); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestTool_ListAndInspect`
Expected: FAIL — tools not registered.

- [ ] **Step 3: Register the tools**

In `internal/mcp/tools.go`, extend `register()`:

```go
func (s *Server) register() {
	s.registerCookieTools()
	s.registerMangaTools()
}

type ListMangaOutput struct {
	Items []MangaEntry `json:"items"`
}

type InspectMangaInput struct {
	Name string `json:"name" jsonschema:"required,description=The manga name without the .cbz suffix (e.g. \"Gintama\")"`
}

func (s *Server) registerMangaTools() {
	sdk.AddTool(s.sdk,
		&sdk.Tool{
			Name:        "list_manga",
			Description: "List every .cbz archive under the manga root, with chapter count and comment coverage.",
		},
		func(ctx context.Context, req *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, ListMangaOutput, error) {
			items, err := ListManga(s.opts.Root)
			if err != nil {
				return toolErr(MapError(err)), ListMangaOutput{}, nil
			}
			return nil, ListMangaOutput{Items: items}, nil
		},
	)

	sdk.AddTool(s.sdk,
		&sdk.Tool{
			Name:        "inspect_manga",
			Description: "Inspect a single .cbz archive: chapter count, comment coverage, archive width.",
		},
		func(ctx context.Context, req *sdk.CallToolRequest, in InspectMangaInput) (*sdk.CallToolResult, MangaEntry, error) {
			entry, err := InspectManga(s.opts.Root, in.Name)
			if err != nil {
				return toolErr(MapError(err)), MangaEntry{}, nil
			}
			return nil, entry, nil
		},
	)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/...`
Expected: all green.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/integration_test.go
git commit -m "mcp: register list_manga + inspect_manga"
```

---

## Task 7: Run-state singleton

**Files:**
- Create: `internal/mcp/runstate.go`
- Create: `internal/mcp/runstate_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/runstate_test.go`:

```go
package mcp

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunState_AcquireReleaseAcquire(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	ar, err := rs.Acquire("Gintama", "resume", cancel)
	if err != nil {
		t.Fatal(err)
	}
	if ar.Name != "Gintama" {
		t.Fatal("name not stored")
	}
	rs.Release()
	if _, err := rs.Acquire("Other", "sync-manga", cancel); err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	rs.Release()
}

func TestRunState_ConcurrentAcquireRejected(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	if _, err := rs.Acquire("A", "resume", cancel); err != nil {
		t.Fatal(err)
	}
	_, err := rs.Acquire("B", "resume", cancel)
	var te *ToolError
	if !errors.As(err, &te) || te.Code != CodeRunInProgress {
		t.Fatalf("err = %v, want RUN_IN_PROGRESS", err)
	}
}

func TestRunState_SnapshotIsCopy(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	if _, err := rs.Acquire("A", "resume", cancel); err != nil {
		t.Fatal(err)
	}
	snap := rs.Snapshot()
	if snap == nil || snap.Name != "A" {
		t.Fatalf("snapshot = %+v", snap)
	}
	// Releasing must not blank the snapshot we already took.
	rs.Release()
	if snap.Name != "A" {
		t.Fatal("snapshot was aliased to internal state")
	}
	if rs.Snapshot() != nil {
		t.Fatal("post-release snapshot must be nil")
	}
}

func TestRunState_StartedAtRecent(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	if _, err := rs.Acquire("A", "resume", cancel); err != nil {
		t.Fatal(err)
	}
	defer rs.Release()
	if time.Since(rs.Snapshot().StartedAt) > time.Second {
		t.Fatal("StartedAt not set near now")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestRunState`
Expected: FAIL — `RunState` undefined.

- [ ] **Step 3: Implement runstate.go**

Create `internal/mcp/runstate.go`:

```go
package mcp

import (
	"context"
	"sync"
	"time"
)

// RunState enforces "one sync at a time" across all tool calls.
// Tools that don't run the pipeline never touch RunState.
type RunState struct {
	mu     sync.Mutex
	active *ActiveRun
}

type ActiveRun struct {
	Name      string
	Mode      string
	StartedAt time.Time
	Cancel    context.CancelFunc
}

// Acquire registers an active run. Returns a ToolError with code
// RUN_IN_PROGRESS if another run is already active.
func (r *RunState) Acquire(name, mode string, cancel context.CancelFunc) (*ActiveRun, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active != nil {
		return nil, &ToolError{
			Code:    CodeRunInProgress,
			Message: "another sync is in progress (" + r.active.Mode + " " + r.active.Name + ")",
		}
	}
	r.active = &ActiveRun{
		Name:      name,
		Mode:      mode,
		StartedAt: time.Now().UTC(),
		Cancel:    cancel,
	}
	return r.active, nil
}

// Release clears the active run. Safe to call from defer even when
// Acquire failed (it'll be a no-op then).
func (r *RunState) Release() {
	r.mu.Lock()
	r.active = nil
	r.mu.Unlock()
}

// Snapshot returns a copy of the active run (or nil if idle), safe
// to read after the caller releases.
func (r *RunState) Snapshot() *ActiveRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil {
		return nil
	}
	cp := *r.active
	return &cp
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestRunState`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/runstate.go internal/mcp/runstate_test.go
git commit -m "mcp: RunState singleton enforces one sync at a time"
```

---

## Task 8: Sync executor with injected pipeline runner

**Files:**
- Create: `internal/mcp/sync.go`
- Create: `internal/mcp/sync_test.go`
- Modify: `internal/mcp/server.go` (wire RunState into Server)

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/sync_test.go`:

```go
package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/pipeline"
)

func TestRunSync_MapsCFExpired(t *testing.T) {
	exec := &SyncExecutor{
		Root:        t.TempDir(),
		CookiesPath: t.TempDir() + "/cookies.json",
		RunState:    &RunState{},
		runFn: func(ctx context.Context, opts pipeline.Opts) error {
			return fetcher.ErrCloudflareExpired
		},
	}
	_, err := exec.Run(context.Background(), pipeline.SyncManga, SyncInput{
		URL: "https://example.com/manga", Name: "X",
	})
	var te *ToolError
	if !errors.As(err, &te) || te.Code != CodeCFTokenExpired {
		t.Fatalf("err = %v, want CF_TOKEN_EXPIRED", err)
	}
}

func TestRunSync_HappyPath(t *testing.T) {
	var gotOpts pipeline.Opts
	exec := &SyncExecutor{
		Root:        t.TempDir(),
		CookiesPath: t.TempDir() + "/cookies.json",
		RunState:    &RunState{},
		runFn: func(ctx context.Context, opts pipeline.Opts) error {
			gotOpts = opts
			return nil
		},
	}
	out, err := exec.Run(context.Background(), pipeline.SyncComments, SyncInput{
		URL: "https://x", Name: "X", Concurrency: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "sync-comments" {
		t.Fatalf("mode = %q", out.Mode)
	}
	if gotOpts.Concurrency != 7 {
		t.Fatalf("concurrency passed through wrong: %d", gotOpts.Concurrency)
	}
	if gotOpts.Name != "X" {
		t.Fatalf("name passed through wrong: %q", gotOpts.Name)
	}
}

func TestRunSync_BlockedWhileActive(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	_, _ = rs.Acquire("Other", "resume", cancel)
	exec := &SyncExecutor{
		Root: t.TempDir(), CookiesPath: t.TempDir() + "/cookies.json",
		RunState: rs,
		runFn:    func(ctx context.Context, _ pipeline.Opts) error { return nil },
	}
	_, err := exec.Run(context.Background(), pipeline.Resume, SyncInput{URL: "x", Name: "Y"})
	var te *ToolError
	if !errors.As(err, &te) || te.Code != CodeRunInProgress {
		t.Fatalf("err = %v, want RUN_IN_PROGRESS", err)
	}
}

func TestRunSync_DerivesNameFromURL(t *testing.T) {
	exec := &SyncExecutor{
		Root: t.TempDir(), CookiesPath: t.TempDir() + "/cookies.json",
		RunState: &RunState{},
		runFn:    func(ctx context.Context, _ pipeline.Opts) error { return nil },
	}
	out, err := exec.Run(context.Background(), pipeline.SyncManga, SyncInput{
		URL: "https://<source-site>/truyen-tranh/gintama-216",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "gintama-216" {
		t.Fatalf("derived name = %q", out.Name)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestRunSync`
Expected: FAIL — SyncExecutor undefined.

- [ ] **Step 3: Implement sync.go**

Create `internal/mcp/sync.go`:

```go
package mcp

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/pipeline"
	sourcesite "github.com/anhpham/downloader/internal/site/source"
)

// SyncInput is the shared input shape for sync_manga / resume /
// sync_comments. The wire fields use snake_case to match the rest
// of the MCP tools.
type SyncInput struct {
	URL         string `json:"url" jsonschema:"required,description=The manga page URL on the source site"`
	Name        string `json:"name,omitempty" jsonschema:"description=Override the default name (URL slug)"`
	Concurrency int    `json:"concurrency,omitempty" jsonschema:"description=Number of chapters in flight (default 4)"`
	From        int    `json:"from,omitempty" jsonschema:"description=Inclusive lower chapter bound (ignored by sync_comments)"`
	To          int    `json:"to,omitempty" jsonschema:"description=Inclusive upper chapter bound (ignored by sync_comments)"`
}

type SyncOutput struct {
	Name       string `json:"name"`
	Mode       string `json:"mode"`
	DurationMS int64  `json:"duration_ms"`
}

// pipelineRunner is the seam the tests use to avoid running the
// real pipeline. Production uses pipeline.Run directly.
type pipelineRunner func(ctx context.Context, opts pipeline.Opts) error

// SyncExecutor wires one sync tool to the pipeline. It also owns
// the cookie load + fetcher build because that is the same dance
// every sync tool performs.
type SyncExecutor struct {
	Root        string
	CookiesPath string
	RunState    *RunState
	runFn       pipelineRunner // nil → pipeline.Run
}

func (e *SyncExecutor) Run(ctx context.Context, mode pipeline.Mode, in SyncInput) (SyncOutput, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = deriveName(in.URL)
	}
	if name == "" {
		return SyncOutput{}, &ToolError{Code: CodeBadInput, Message: "could not derive a manga name from URL; pass `name` explicitly"}
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, err := e.RunState.Acquire(name, modeLabel(mode), cancel); err != nil {
		return SyncOutput{}, err
	}
	defer e.RunState.Release()

	cf, err := fetcher.LoadCookieFile(e.CookiesPath)
	if err != nil {
		return SyncOutput{}, &ToolError{Code: CodeBadInput, Message: "cookie file unreadable; call update_cookie first", Cause: err}
	}
	f, err := fetcher.New(cf, fetcher.Options{})
	if err != nil {
		return SyncOutput{}, MapError(err)
	}

	opts := pipeline.Opts{
		Mode:        mode,
		MangaURL:    in.URL,
		Root:        e.Root,
		Name:        name,
		Concurrency: defaultConcurrency(in.Concurrency),
		Site:        &sourcesite.Site{Fetcher: f},
		Fetcher:     f,
	}
	if mode != pipeline.SyncComments {
		opts.From = in.From
		opts.To = in.To
	}

	runner := e.runFn
	if runner == nil {
		runner = pipeline.Run
	}

	start := time.Now()
	if err := runner(runCtx, opts); err != nil {
		return SyncOutput{}, MapError(err)
	}
	return SyncOutput{
		Name:       name,
		Mode:       modeLabel(mode),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

func deriveName(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return path.Base(strings.TrimRight(u.Path, "/"))
}

func modeLabel(m pipeline.Mode) string {
	switch m {
	case pipeline.SyncManga:
		return "sync-manga"
	case pipeline.Resume:
		return "resume"
	case pipeline.SyncComments:
		return "sync-comments"
	default:
		return fmt.Sprintf("mode-%d", m)
	}
}

func defaultConcurrency(in int) int {
	if in <= 0 {
		return 4
	}
	return in
}
```

If `pipeline.Mode` constants are named differently (e.g. `pipeline.ModeSyncManga`), use whatever the existing code defines — check `internal/pipeline/plan.go`.

- [ ] **Step 4: Wire RunState + executor into Server**

In `server.go`, add fields and construction:

```go
type Server struct {
	opts     Opts
	sdk      *sdk.Server
	log      *log.Logger
	runState *RunState
	sync     *SyncExecutor
}

// inside New(), after sdk.NewServer:
s.runState = &RunState{}
s.sync = &SyncExecutor{
	Root:        opts.Root,
	CookiesPath: opts.CookiesPath,
	RunState:    s.runState,
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestRunSync`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/sync.go internal/mcp/sync_test.go internal/mcp/server.go
git commit -m "mcp: SyncExecutor with injectable runner + error mapping"
```

---

## Task 9: Register sync tools + `cancel_run`

**Files:**
- Modify: `internal/mcp/tools.go`
- Modify: `internal/mcp/integration_test.go`

- [ ] **Step 1: Write the failing test**

Append to `integration_test.go`:

```go
func TestTool_SyncCFExpiredFlow(t *testing.T) {
	srv, client := newTestPair(t)
	srv.sync.runFn = func(ctx context.Context, _ pipeline.Opts) error {
		return fetcher.ErrCloudflareExpired
	}
	// We must seed a cookie file so LoadCookieFile succeeds.
	mustWriteJSON(t, srv.opts.CookiesPath, fetcher.CookieFile{
		Cookies: []fetcher.CookieRecord{{Name: "cf_clearance", Value: "x", Domain: ".x"}},
	})

	res, err := client.CallTool(context.Background(), &sdk.CallToolParams{
		Name:      "resume",
		Arguments: map[string]any{"url": "https://<source-site>/truyen-tranh/x-1", "name": "X"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatal("expected IsError on CF expiry")
	}
	if got := res.Meta["error_code"]; got != CodeCFTokenExpired {
		t.Fatalf("Meta.error_code = %v, want %q", got, CodeCFTokenExpired)
	}
	if !strings.Contains(contentText(res), CodeCFTokenExpired) {
		t.Fatalf("missing %s in %q", CodeCFTokenExpired, contentText(res))
	}
}

func TestTool_CancelRun(t *testing.T) {
	srv, client := newTestPair(t)
	mustWriteJSON(t, srv.opts.CookiesPath, fetcher.CookieFile{
		Cookies: []fetcher.CookieRecord{{Name: "cf_clearance", Value: "x", Domain: ".x"}},
	})

	started := make(chan struct{})
	srv.sync.runFn = func(ctx context.Context, _ pipeline.Opts) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	go func() {
		_, _ = client.CallTool(context.Background(), &sdk.CallToolParams{
			Name:      "resume",
			Arguments: map[string]any{"url": "https://example.com/x-1", "name": "X"},
		})
	}()
	<-started

	cancelRes, err := client.CallTool(context.Background(), &sdk.CallToolParams{Name: "cancel_run"})
	if err != nil {
		t.Fatal(err)
	}
	if cancelRes.IsError {
		t.Fatalf("cancel returned error: %s", contentText(cancelRes))
	}
	// Allow the goroutine to settle.
	deadline := time.Now().Add(2 * time.Second)
	for srv.runState.Snapshot() != nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.runState.Snapshot() != nil {
		t.Fatal("run still active after cancel")
	}
}
```

Add the imports needed: `"time"`, `"github.com/anhpham/downloader/internal/fetcher"`, `"github.com/anhpham/downloader/internal/pipeline"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestTool_Sync\|TestTool_Cancel`
Expected: FAIL — tools not registered.

- [ ] **Step 3: Register the sync + cancel tools**

In `tools.go`, extend `register()`:

```go
func (s *Server) register() {
	s.registerCookieTools()
	s.registerMangaTools()
	s.registerSyncTools()
}

func (s *Server) registerSyncTools() {
	descSuffix := " If this tool returns CF_TOKEN_EXPIRED, ask the user for a fresh cf_clearance value and call update_cookie before retrying."

	sdk.AddTool(s.sdk,
		&sdk.Tool{
			Name:        "sync_manga",
			Description: "Download new chapters AND backfill missing comment pages. Use for first-time downloads or full re-syncs." + descSuffix,
		},
		s.syncHandler(pipeline.SyncManga),
	)
	sdk.AddTool(s.sdk,
		&sdk.Tool{
			Name:        "resume",
			Description: "Download new chapters + their comments. Already-archived chapters are left alone (no comment backfill)." + descSuffix,
		},
		s.syncHandler(pipeline.Resume),
	)
	sdk.AddTool(s.sdk,
		&sdk.Tool{
			Name:        "sync_comments",
			Description: "Backfill comment pages onto an existing archive without downloading new chapters." + descSuffix,
		},
		s.syncHandler(pipeline.SyncComments),
	)
	sdk.AddTool(s.sdk,
		&sdk.Tool{
			Name:        "cancel_run",
			Description: "Cancel the in-flight sync, if any. Already-completed chapters survive via the scratch directory.",
		},
		func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, CancelOutput, error) {
			active := s.runState.Snapshot()
			if active == nil {
				return nil, CancelOutput{Cancelled: false}, nil
			}
			active.Cancel()
			return nil, CancelOutput{Cancelled: true, WasRunning: active.Name}, nil
		},
	)
}

type CancelOutput struct {
	Cancelled  bool   `json:"cancelled"`
	WasRunning string `json:"was_running,omitempty"`
}

// SyncExecutor.Run always returns either nil or a *ToolError
// (see internal/mcp/sync.go), so the handler can use it directly
// without re-mapping. errors.As stays for defensive symmetry against
// future refactors.
func (s *Server) syncHandler(mode pipeline.Mode) func(context.Context, *sdk.CallToolRequest, SyncInput) (*sdk.CallToolResult, SyncOutput, error) {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in SyncInput) (*sdk.CallToolResult, SyncOutput, error) {
		out, err := s.sync.Run(ctx, mode, in)
		if err == nil {
			return nil, out, nil
		}
		var te *ToolError
		if errors.As(err, &te) {
			return toolErr(te), SyncOutput{}, nil
		}
		return toolErr(MapError(err)), SyncOutput{}, nil
	}
}
```

Add the import `"errors"` and `"github.com/anhpham/downloader/internal/pipeline"` at the top of `tools.go`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/...`
Expected: all green, including the new CF-expiry and cancel-run tests.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/integration_test.go
git commit -m "mcp: register sync_manga / resume / sync_comments / cancel_run"
```

---

## Task 10: stdout-cleanliness guarantee

**Files:**
- Modify: `internal/mcp/server_test.go`

The MCP protocol uses stdout for JSON-RPC frames; any stray
`fmt.Println` from server code or a handler corrupts the channel.
This test covers BOTH construction-time and handler-time stdout.

- [ ] **Step 1: Add the test**

Append to `server_test.go`:

```go
// TestStdoutStaysClean asserts that constructing the server AND
// invoking every read-only tool never writes to stdout. Tool
// handlers must route logs through the configured stderr logger.
//
// Sync tools are excluded because they need a fully-seeded cookie
// file + an injected runner; the integration tests in
// integration_test.go already exercise their handler bodies.
func TestStdoutStaysClean(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	_, sess := newTestPair(t)
	for _, name := range []string{"get_cookie_status", "list_manga"} {
		if _, err := sess.CallTool(context.Background(), &sdk.CallToolParams{Name: name}); err != nil {
			t.Fatalf("call %s: %v", name, err)
		}
	}

	w.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	if buf.Len() != 0 {
		t.Fatalf("stdout polluted with %d bytes: %q", buf.Len(), buf.String())
	}
}
```

Add imports: `"bytes"`, `"io"`, `"os"`.

- [ ] **Step 2: Run the test**

Run: `go test ./internal/mcp/... -run TestStdoutStaysClean`
Expected: PASS. If it fails, find the rogue `fmt.Print*` call (likely in `tools.go`, `server.go`, or one of the handler files) and route it through `s.log` instead.

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/server_test.go
git commit -m "mcp: assert stdout stays clean across handler calls"
```

---

## Task 11: README — Claude Desktop section

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Append the new section**

Insert this block after the "Usage" section in `README.md` (before "Behaviour notes"):

````markdown
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
````

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README section for Claude Desktop MCP server"
```

---

## Task 12: Manual smoke test in Claude Desktop

This task has no commit. It's the one thing tests can't cover.

- [ ] **Step 1: Build**

```bash
go build -o bin/downloader .
```

- [ ] **Step 2: Edit Claude Desktop config**

Add the `manga-downloader` block to
`~/Library/Application Support/Claude/claude_desktop_config.json`
(see README) and restart Claude Desktop.

- [ ] **Step 3: Run through the happy path**

In Claude Desktop, ask: "List my manga." Expect a table including
`Gintama` (3.7 GB) and `Hikaru no Go` (~800 MB), with chapter counts
matching `inspect_manga`.

- [ ] **Step 4: Run through the token-expiry path**

Manually replace the `cf_clearance` value in `cookies.json` with
garbage (e.g. `BROKEN`). In Claude Desktop, ask: "Resume One Piece
from `<some-url>`." Expect Claude to surface a `CF_TOKEN_EXPIRED`
error and ask you for a fresh token. Paste a real token; expect
Claude to call `update_cookie`, then retry the sync successfully
(at least past the first request — you can `cancel_run` once you
have proof of life).

- [ ] **Step 5: Push the branch + open PR**

```bash
git push -u origin feature/mcp-server
gh pr create --title "Local MCP server for Claude Desktop" --body "$(cat <<'EOF'
## Summary
- New `bin/downloader mcp` subcommand exposes every CLI capability as MCP tools over stdio.
- Eight tools: `update_cookie`, `get_cookie_status`, `list_manga`, `inspect_manga`, `sync_manga`, `resume`, `sync_comments`, `cancel_run`.
- `CF_TOKEN_EXPIRED` error code lets Claude prompt the user for a fresh token in-conversation, then retry the sync.

## Test plan
- [x] `go test ./...` green
- [x] `go vet ./...` clean
- [x] Manual smoke in Claude Desktop: list, inspect, sync, token-expiry recovery, cancel
EOF
)"
```
