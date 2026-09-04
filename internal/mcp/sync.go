package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/pipeline"
	"github.com/anhpham/downloader/internal/registry"
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
	if mode != pipeline.SyncComments {
		if reg, rerr := registry.Load(e.Root); rerr == nil {
			reg.Upsert(name, in.URL)
			reg.Touch(name, time.Now())
			_ = reg.Save()
		}
	}
	return SyncOutput{
		Name:       name,
		Mode:       modeLabel(mode),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

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
	Name          string `json:"name"`
	NewChapters   int    `json:"new_chapters"`
	MissingImages int    `json:"missing_images"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
}

// updateAll runs pipeline.UpdateAll against every registered manga.
func (s *Server) updateAll(ctx context.Context, in UpdateAllInput) (UpdateAllOutput, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, err := s.sync.RunState.Acquire("*", "update-all", cancel); err != nil {
		return UpdateAllOutput{}, err
	}
	defer s.sync.RunState.Release()

	reg, err := registry.Load(s.opts.Root)
	if err != nil {
		return UpdateAllOutput{}, err
	}
	cf, err := fetcher.LoadCookieFile(s.sync.CookiesPath)
	if err != nil {
		return UpdateAllOutput{}, &ToolError{Code: CodeBadInput, Message: "cookie file unreadable; call update_cookie first", Cause: err}
	}
	f, err := fetcher.New(cf, fetcher.Options{})
	if err != nil {
		return UpdateAllOutput{}, err
	}
	if names := reg.Names(); len(names) > 0 {
		first, _ := reg.Get(names[0])
		if healed, changed, herr := f.HealUserAgent(runCtx, first.URL); herr != nil {
			if errors.Is(herr, fetcher.ErrCloudflareExpired) {
				return UpdateAllOutput{}, MapError(herr)
			}
			// A transport failure here is handled by UpdateAll's own
			// preflight below; only bail on non-transport errors.
			if fetcher.Classify(herr) != fetcher.KindHostUnreachable {
				return UpdateAllOutput{}, herr
			}
		} else if changed {
			_ = fetcher.SaveUserAgent(s.sync.CookiesPath, healed)
		}
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
		oo := UpdateAllOutcome{Name: o.Name, NewChapters: o.NewChapters, MissingImages: o.MissingImages, Status: o.Status}
		if o.Err != nil {
			oo.Error = o.Err.Error()
		}
		out.Outcomes = append(out.Outcomes, oo)
	}
	return out, err
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
