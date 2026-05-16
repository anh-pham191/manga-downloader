# MCP Server for Claude Desktop — Design

**Date:** 2026-05-16
**Branch:** `feature/mcp-server`
**Status:** Draft (pre-implementation)

---

## 1. Problem

The downloader is a CLI: every operation requires remembering a
subcommand, flags, and hand-editing `cookies.json` when Cloudflare
rotates the `cf_clearance` token. The user wants a conversational
front-end via Claude Desktop:

- "List my manga and tell me which have missing comments."
- "Sync new chapters for One Piece."
- "Backfill comments for Gintama."
- "The token expired — here's a new one: \<paste\>. Try again."

Claude Desktop already supports launching local MCP servers over
stdio. Exposing the existing pipeline as MCP tools is the cheapest
path to that UX. There is **no other goal**: this is glue, not a new
product.

## 2. Scope

**In scope (v1):**

- One Go MCP server, shipped as a `mcp` subcommand of the existing
  `downloader` binary.
- Tool coverage for every CLI capability:
  - `sync-manga`, `resume`, `sync-comments` (via pipeline `Run`).
- Plus the tools the CLI doesn't have but Claude Desktop needs:
  - `update_cookie`, `get_cookie_status` (token management).
  - `list_manga`, `inspect_manga` (so Claude can answer "what do I
    have?" without invoking a sync).
  - `cancel_run` (cooperative cancellation of an in-flight sync).
- A clean `CF_TOKEN_EXPIRED` error so Claude can prompt the user for a
  fresh token in conversation, then re-issue the sync.
- One sync at a time across the whole server (the existing per-archive
  `flock` stays as a backstop).
- Unit + integration tests over an in-process MCP client/server pair.
- README section explaining the Claude Desktop config block.

**Out of scope (v1):**

- HTTP / SSE transport. Claude Desktop wants stdio.
- Auth, multi-user, remote MCP.
- Persistent run history (the `.cbz` archive is the history).
- MCP progress notifications via `progressToken` — deferred to v2.
- Resource subscriptions, prompts, sampling.
- Headless-browser token refresh (already ruled out for the CLI).
- A second source site.

## 3. Acceptance Criteria

A v1 release is accepted when **all** of the following hold:

1. `bin/downloader mcp` starts a stdio MCP server that responds to
   `initialize` and lists eight tools: `update_cookie`,
   `get_cookie_status`, `list_manga`, `inspect_manga`, `sync_manga`,
   `resume`, `sync_comments`, `cancel_run`.
2. With a Claude Desktop config pointing at the binary, the user can
   ask "list my manga" and Claude returns the per-archive summary
   (name, size, chapters, comment coverage) without further prompting.
3. When the cookie is expired and the user asks Claude to sync, the
   tool call fails with structured error code `CF_TOKEN_EXPIRED` and a
   message instructing Claude how to recover. Claude can then call
   `update_cookie` with a value the user pastes and re-issue the sync,
   and the sync succeeds.
4. While a sync is running, a second `sync_*` call returns
   `RUN_IN_PROGRESS` with the active manga name + start time; the
   first sync continues unaffected. `cancel_run` aborts the first
   sync within 2 seconds.
5. `update_cookie` preserves all other entries in `cookies.json`
   (other cookies, `user_agent`) and is crash-safe (temp-file +
   rename).
6. All new code in `internal/mcp/` has unit tests; the end-to-end
   path is covered by an in-process MCP client/server test that does
   not touch the network.
7. `go vet ./...` and `go test ./...` pass; `gofmt -l ./...` produces
   no output.
8. README explains the Claude Desktop config block, the
   cookie-refresh flow, and how to manually smoke-test the server.

## 4. Hazards & Mitigations

| # | Hazard | Mitigation |
|---|---|---|
| H1 | MCP SDK API churn | Pin `github.com/modelcontextprotocol/go-sdk` to a specific minor version in `go.mod`. Wrap the SDK in `internal/mcp/server.go` so an SDK swap touches one file. |
| H2 | stdio protocol pollution (any `fmt.Println` from a tool corrupts JSON-RPC) | Route all logging to `stderr` via a dedicated `*log.Logger`. Forbid `fmt.Print*` in `internal/mcp/`. Add a test that captures stdout during a tool call and asserts only protocol bytes appear there. |
| H3 | Long-running sync blocks the stdio loop | Run pipeline in a goroutine; tool handler waits with `select` on completion + context. Server keeps responding to `tools/list`, `ping`, and `cancel_run` while sync runs. |
| H4 | Concurrent sync corrupts archive | One sync at a time enforced by a server-level `sync.Mutex` + state machine. Existing per-archive `flock` is a second line of defence. |
| H5 | Cookie-file write loses other entries | Read → mutate `cf_clearance` only → write to temp file in same dir → fsync → atomic rename. Preserve `user_agent` and any other cookies verbatim. |
| H6 | Token-expiry error gets buried as generic failure | Tool wrapper checks `errors.Is(err, fetcher.ErrCloudflareExpired)` first and maps to `CF_TOKEN_EXPIRED` with a recovery instruction in the error message; tested. |
| H7 | Claude Desktop crashes / disconnects mid-sync | Server detects stdin EOF (SDK does this), cancels the active context, lets the pipeline write its scratch `.ok` markers, exits cleanly. Re-running picks up where it left off. |
| H8 | Wrong cookie file path on a teammate's machine | `mcp` subcommand accepts `--cookies` and `--root` flags; the Claude Desktop config block in the README shows how to set them. Default mirrors the CLI default. |
| H9 | User asks for a sync of a manga the cookie was never refreshed for | `update_cookie` accepts an optional `domain` parameter; default is the existing entry's domain or `.truyenqqko.com`. Documented. |

## 5. Architecture

### Package layout (new + touched)

```
downloader/
├── main.go                     # add `mcp` subcommand dispatch
├── internal/
│   ├── mcp/                    # NEW
│   │   ├── server.go           # SDK wiring, tool registration, run loop
│   │   ├── tools.go            # tool input/output schemas + dispatch
│   │   ├── cookies.go          # update_cookie + get_cookie_status handlers
│   │   ├── manga.go            # list_manga + inspect_manga handlers
│   │   ├── sync.go             # sync_manga / resume / sync_comments handlers
│   │   ├── runstate.go         # one-sync-at-a-time state machine
│   │   ├── errors.go           # MCP error codes + wrapping
│   │   └── *_test.go
│   └── (existing internal/* — unchanged in shape, only re-used)
└── README.md                   # NEW "Claude Desktop" section
```

### Why this shape

- `internal/mcp/server.go` is the only file that imports the MCP SDK.
  Every other file in `internal/mcp/` deals in plain Go types and
  `*log.Logger`. If the SDK changes, `server.go` changes and nothing
  else.
- Each tool family lives in its own file (cookies, manga, sync). The
  files are small and each has one clear job. `tools.go` is the
  registry — it maps tool name → handler so `server.go` doesn't grow
  a giant switch.
- `runstate.go` owns the single-sync invariant; no other file knows
  the rules. Tests can drive it directly.
- `errors.go` defines the MCP error codes as constants so tests can
  assert on them without string matching.

### Key types

```go
// internal/mcp/runstate.go
type RunState struct {
    mu      sync.Mutex
    active  *ActiveRun // nil when idle
}

type ActiveRun struct {
    Name      string
    Mode      string // "sync-manga" | "resume" | "sync-comments"
    StartedAt time.Time
    Cancel    context.CancelFunc
}

// Acquire returns nil if the run was registered, or an error
// with code RUN_IN_PROGRESS if another run is active.
func (r *RunState) Acquire(name, mode string, cancel context.CancelFunc) (*ActiveRun, error)

// Release clears the active run. Safe to call from defer.
func (r *RunState) Release()

// Snapshot returns the active run for cancel_run / list-style tools.
func (r *RunState) Snapshot() *ActiveRun
```

```go
// internal/mcp/errors.go
const (
    CodeCFTokenExpired = "CF_TOKEN_EXPIRED"
    CodeRunInProgress  = "RUN_IN_PROGRESS"
    CodeNoArchive      = "NO_ARCHIVE"
    CodeBadInput       = "BAD_INPUT"
    CodeInternal       = "INTERNAL"
)

type ToolError struct {
    Code    string
    Message string
    Cause   error
}
```

```go
// internal/mcp/sync.go — shared executor for the three sync tools
type SyncInput struct {
    URL         string `json:"url"`
    Name        string `json:"name,omitempty"`
    Concurrency int    `json:"concurrency,omitempty"` // default 4
    From        int    `json:"from,omitempty"`
    To          int    `json:"to,omitempty"`
}

type SyncOutput struct {
    Name         string `json:"name"`
    Mode         string `json:"mode"`
    Chapters     int    `json:"chapters_in_archive_after"`
    NewChapters  int    `json:"chapters_added"`
    Commented    int    `json:"chapters_with_comments_after"`
    DurationMS   int64  `json:"duration_ms"`
}
```

### Tool catalogue

| Tool | Input | Output | Notes |
|---|---|---|---|
| `update_cookie` | `value: string`, `user_agent?: string`, `domain?: string` | `{ ok: true, cookie_path: string }` | Writes via temp+rename. Rejects empty values. |
| `get_cookie_status` | `{}` | `{ cookie_path, has_clearance, mtime, last8 }` | Never returns the full token. `last8` is the trailing 8 chars for confirmation. |
| `list_manga` | `{}` | `[{ name, size_bytes, chapters, comments_attached, missing_comments }]` | Reads `*.cbz` from `--root` and runs `archive.Inspect` on each. |
| `inspect_manga` | `{ name }` | Same shape as one `list_manga` entry plus `archive_width`. | Fails with `NO_ARCHIVE` if missing. |
| `sync_manga` | `SyncInput` | `SyncOutput` | Calls pipeline `Run` with mode `SyncManga`. |
| `resume` | `SyncInput` | `SyncOutput` | Mode `Resume`. |
| `sync_comments` | `SyncInput` (ignores `from`/`to`) | `SyncOutput` | Mode `SyncComments`. |
| `cancel_run` | `{}` | `{ cancelled: bool, was_running: string? }` | Calls `RunState.Snapshot().Cancel`. Safe to call when idle. |

### Token-failure flow

1. User: "Sync new chapters for Gintama from \<url\>."
2. Claude calls `resume`.
3. Pipeline returns `fetcher.ErrCloudflareExpired`.
4. Tool wrapper maps to `ToolError{Code: CF_TOKEN_EXPIRED, Message:
   "cf_clearance is invalid or expired. Ask the user for a fresh
   value (DevTools → Application → Cookies → cf_clearance), then
   call update_cookie before retrying."}`.
5. Claude sees the error, prompts the user in chat for a token.
6. User pastes token.
7. Claude calls `update_cookie { value: "<pasted>" }`.
8. Claude calls `resume` again. Succeeds.

The tool description for every sync tool mentions this contract so
Claude understands the recovery without having to discover it the
hard way.

### Concurrency + cancellation

- `RunState.Acquire` returns `RUN_IN_PROGRESS` if another sync is
  active. Tools that don't run the pipeline (`update_cookie`,
  `list_manga`, etc.) never touch `RunState` and are always
  available.
- Each sync handler creates its own `context.Context` derived from
  the request context, hands the `CancelFunc` to `RunState`, and
  defers `Release()`.
- `cancel_run` calls the active `CancelFunc`. The pipeline already
  honours `ctx.Done()` between chapters; partial work survives via
  the existing `.ok` markers.

### Cookie-file write

```go
// internal/mcp/cookies.go (sketch)
func UpdateClearance(path, value, ua, domain string) error {
    cf, err := loadOrInit(path)
    if err != nil { return err }
    setOrAppendCookie(cf, "cf_clearance", value, domainOr(cf, domain))
    if ua != "" { cf.UserAgent = ua }
    return writeAtomic(path, cf)
}
```

`writeAtomic` writes to `path + ".tmp"` in the same directory,
`Sync()`s the file, and `os.Rename`s into place. Standard Unix
atomic-write recipe; survives crash mid-write.

### Logging

- One `*log.Logger` writing to `os.Stderr` is constructed in
  `server.go` and passed to every handler.
- `internal/mcp/` packages MUST NOT call `fmt.Println` /
  `fmt.Printf` (a test enforces this with a `go vet`-style scan).
- The existing pipeline logger continues to work unchanged.

## 6. CLI surface

```
downloader mcp [--cookies PATH] [--root PATH] [--verbose]
```

Defaults match the existing CLI. The subcommand reads stdin /
writes stdout for protocol; logs go to stderr.

Sample `claude_desktop_config.json` block (will live in README):

```json
{
  "mcpServers": {
    "manga-downloader": {
      "command": "/Users/anhpham/Documents/Projects/script/downloader/bin/downloader",
      "args": ["mcp"]
    }
  }
}
```

## 7. Testing strategy

- **Unit tests** per file. `cookies_test.go` covers atomic write,
  preservation, and rejection of empty values. `manga_test.go` uses
  a temp directory of fake `.cbz` files. `sync_test.go` injects a
  fake `pipelineRunner` so it can assert on `Opts` without running
  the real pipeline. `runstate_test.go` covers Acquire/Release/race.
- **In-process MCP integration test** in
  `internal/mcp/server_test.go`: starts the server with the SDK's
  in-memory transport, calls `initialize`, asserts the tool list,
  drives an `update_cookie` round-trip, drives a fake sync that
  returns `ErrCloudflareExpired` and asserts on the structured
  error.
- **stdout-cleanliness test**: wraps a tool call, captures stdout,
  asserts it contains only well-formed JSON-RPC frames.
- **No network in any test.** The fake fetcher used elsewhere in
  the codebase carries over.

## 8. Migration / rollback

- Pure addition. No existing CLI behaviour changes; no flags
  removed; no archive format changes.
- Rollback = drop `internal/mcp/` and the `mcp` subcommand case
  from `main.go`. No data on disk needs to be touched.

## 9. Open questions (resolved)

- **Which MCP SDK?** Official Go SDK at
  `github.com/modelcontextprotocol/go-sdk`. Same language as the
  rest of the codebase; no subprocess; no second toolchain.
- **One binary or two?** One. `bin/downloader mcp` keeps the
  install story trivial (Claude Desktop config points at one path)
  and avoids two go.mod entries to keep in sync.
- **Progress notifications?** Deferred to v2. Final-result text is
  good enough for v1; long syncs stream chapter logs to stderr,
  which Claude Desktop surfaces in its server logs.
- **Auto-refresh the token?** No. The user controls token refresh.
  The point of this feature is the conversation flow, not magic.

## 10. Non-goals (do not let scope creep)

- No web UI, GUI, or extra binary.
- No persistent state beyond the existing `.cbz` archives and
  `cookies.json`.
- No sampling / prompts / resources (MCP features beyond tools).
- No second source site adapter in this PR.
- No refactor of `internal/pipeline` beyond exposing a seam if
  testing demands it.
