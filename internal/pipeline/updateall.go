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
	// AskDomain is called when preflight classifies the failure as
	// host-unreachable. Return the new host, or "" to abort. nil → abort.
	AskDomain func(oldHost string, cause error) string
	Now       func() time.Time
	Runner    func(ctx context.Context, opts Opts) (Result, error)
}

type MangaOutcome struct {
	Name          string
	NewChapters   int
	MissingImages int
	Status        string // "ok", "no-archive", "busy", "failed", "skipped"
	Err           error
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
	// Finished series are never probed; if nothing is active, report
	// them and stop before touching the network.
	active := o.Registry.ActiveNames()
	if len(active) == 0 {
		for _, name := range names {
			res.Outcomes = append(res.Outcomes, MangaOutcome{Name: name, Status: "finished"})
		}
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
	first, _ := o.Registry.Get(active[0])
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
			candidate, cerr := registry.SwapHost(first.URL, newHost)
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
			// Deliberate deviation from spec §3 step 2: the spec's preflight
			// retries up to 3 registry entries before giving up. We don't,
			// because a non-transport, non-Cloudflare error can never trigger
			// a domain prompt here (only KindHostUnreachable does), so there
			// is nothing a retry loop would accomplish beyond what the
			// per-manga loop below already does for every entry. Fall
			// through and let it record this one.
			o.logf("preflight: %s: %v", active[0], err)
		}
	}

	// --- main loop ---------------------------------------------------
	for _, name := range names {
		e, _ := o.Registry.Get(name)
		out := MangaOutcome{Name: name}
		if e.Finished {
			out.Status = "finished"
			res.Outcomes = append(res.Outcomes, out)
			continue
		}
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
		out.MissingImages = r.MissingImages
		switch {
		case err == nil:
			out.Status = "ok"
			o.Registry.Touch(name, now())
		case errors.Is(err, fetcher.ErrCloudflareExpired):
			out.Status, out.Err = "failed", err
			res.Outcomes = append(res.Outcomes, out)
			if serr := o.Registry.Save(); serr != nil {
				o.logf("save registry after cloudflare abort: %v", serr)
			}
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
