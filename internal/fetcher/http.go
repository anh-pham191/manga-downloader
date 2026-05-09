package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"
)

// HTTPFetcher is the production Fetcher: a net/http client primed
// with the cf_clearance cookie and User-Agent the user pasted in
// from their real browser, plus retries and jitter so we behave
// well against the source host.
type HTTPFetcher struct {
	client    *http.Client
	userAgent string

	maxAttempts int
	minDelay    time.Duration
	maxDelay    time.Duration
}

// Options tune the fetcher. Zero values mean "use the default".
type Options struct {
	MaxAttempts int
	MinDelay    time.Duration
	MaxDelay    time.Duration
	Timeout     time.Duration // per-request HTTP timeout
}

func (o Options) withDefaults() Options {
	if o.MaxAttempts == 0 {
		o.MaxAttempts = 3
	}
	if o.MinDelay == 0 {
		o.MinDelay = 200 * time.Millisecond
	}
	if o.MaxDelay == 0 {
		o.MaxDelay = 500 * time.Millisecond
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	return o
}

// New builds an HTTPFetcher from a loaded cookie file. The cookies
// usually come from a one-time DevTools paste after the user solves
// Cloudflare's challenge in their real browser.
func New(cf *CookieFile, opts Options) (*HTTPFetcher, error) {
	opts = opts.withDefaults()
	jar, err := jarFromCookies(cf.Cookies)
	if err != nil {
		return nil, err
	}
	return &HTTPFetcher{
		client:      &http.Client{Jar: jar, Timeout: opts.Timeout},
		userAgent:   cf.UserAgent,
		maxAttempts: opts.MaxAttempts,
		minDelay:    opts.MinDelay,
		maxDelay:    opts.MaxDelay,
	}, nil
}

// ErrCloudflareExpired is surfaced when a request comes back with a
// 403 that smells like Cloudflare; the user needs to refresh
// cf_clearance.
var ErrCloudflareExpired = errors.New("cloudflare cookie likely expired — refresh cf_clearance and re-run with --resume")

// Get implements Fetcher with retries on 429/5xx and dial errors.
// Successful 200 responses include a small post-request jitter to
// avoid hammering the host.
func (h *HTTPFetcher) Get(ctx context.Context, req Request) (*Response, error) {
	var lastErr error
	for attempt := 1; attempt <= h.maxAttempts; attempt++ {
		resp, err := h.attempt(ctx, req)
		if err == nil {
			h.jitter()
			return resp, nil
		}
		lastErr = err
		if !shouldRetry(err) || attempt == h.maxAttempts {
			break
		}
		// Exponential backoff: 500ms, 1s, 2s …
		wait := time.Duration(1<<(attempt-1)) * 500 * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

func (h *HTTPFetcher) attempt(ctx context.Context, req Request) (*Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("User-Agent", h.userAgent)
	httpReq.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")
	if req.Referer != "" {
		httpReq.Header.Set("Referer", req.Referer)
	}

	resp, err := h.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusForbidden {
		return nil, ErrCloudflareExpired
	}
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		return nil, &httpStatusError{Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d for %s", resp.StatusCode, req.URL)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return &Response{Body: body, ContentType: resp.Header.Get("Content-Type")}, nil
}

func (h *HTTPFetcher) jitter() {
	span := h.maxDelay - h.minDelay
	if span <= 0 {
		time.Sleep(h.minDelay)
		return
	}
	time.Sleep(h.minDelay + time.Duration(rand.Int64N(int64(span))))
}

type httpStatusError struct{ Status int }

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("retryable http status %d", e.Status)
}

func shouldRetry(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCloudflareExpired) {
		return false
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		return true
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}
