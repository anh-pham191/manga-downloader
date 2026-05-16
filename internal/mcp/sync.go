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
	URL         string `json:"url" jsonschema:"The manga page URL on the source site"`
	Name        string `json:"name,omitempty" jsonschema:"Override the default name (URL slug)"`
	Concurrency int    `json:"concurrency,omitempty" jsonschema:"Number of chapters in flight (default 4)"`
	From        int    `json:"from,omitempty" jsonschema:"Inclusive lower chapter bound (ignored by sync_comments)"`
	To          int    `json:"to,omitempty" jsonschema:"Inclusive upper chapter bound (ignored by sync_comments)"`
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
//
// SyncExecutor.Run always returns either nil or a *ToolError. Callers
// can rely on this in handler glue.
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
