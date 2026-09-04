package mcp

import (
	"context"
	"errors"

	"github.com/anhpham/downloader/internal/pipeline"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// register attaches every tool to the SDK server. Called from New().
func (s *Server) register() {
	s.registerCookieTools()
	s.registerMangaTools()
	s.registerSyncTools()
}

// --- update_cookie -----------------------------------------------------------

type UpdateCookieInput struct {
	Value     string `json:"value" jsonschema:"The cf_clearance value the user copied from their browser DevTools"`
	UserAgent string `json:"user_agent,omitempty" jsonschema:"Optional updated User-Agent (paste the value of navigator.userAgent)"`
	Domain    string `json:"domain,omitempty" jsonschema:"Optional cookie domain (defaults to the existing entry's domain or .truyenqqko.com)"`
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

// --- list_manga / inspect_manga ---------------------------------------------

type ListMangaOutput struct {
	Items []MangaEntry `json:"items"`
}

type InspectMangaInput struct {
	Name string `json:"name" jsonschema:"The manga name without the .cbz suffix (e.g. \"Gintama\")"`
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

// --- sync_manga / resume / sync_comments / cancel_run ----------------------

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
			Name:        "update_all",
			Description: "Pull new chapters for every registered manga (runs resume per manga). If it returns DOMAIN_MOVED, ask the user for the site's new host and call again with `domain`." + descSuffix,
		},
		func(ctx context.Context, req *sdk.CallToolRequest, in UpdateAllInput) (*sdk.CallToolResult, UpdateAllOutput, error) {
			out, err := s.updateAll(ctx, in)
			if err == nil {
				return nil, out, nil
			}
			var te *ToolError
			if errors.As(err, &te) {
				return toolErr(te), UpdateAllOutput{}, nil
			}
			return toolErr(MapError(err)), UpdateAllOutput{}, nil
		},
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
