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
