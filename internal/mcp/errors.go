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
