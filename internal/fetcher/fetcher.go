// Package fetcher abstracts HTTP fetching so the parser, site
// adapter, and downloader can be tested without the network. The
// real implementation lives in chrome.go and handles Cloudflare via
// chromedp; the interface here is what the rest of the codebase
// depends on.
package fetcher

import "context"

// Request describes one HTTP GET to perform. Referer is sent as the
// Referer header — required by the source site's hot-link protection
// on image CDNs (PLAN.md hazard H3).
type Request struct {
	URL     string
	Referer string
}

// Response is the raw bytes plus enough metadata to write the file.
type Response struct {
	Body        []byte
	ContentType string
}

// Fetcher fetches one URL at a time. Implementations are expected to
// handle their own retries, jitter, and rate limiting; callers treat
// each call as a single all-or-nothing operation.
type Fetcher interface {
	Get(ctx context.Context, req Request) (*Response, error)
}
