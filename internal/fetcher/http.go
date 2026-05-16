package fetcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
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
		// 90s: image downloads on this source's CDN routinely take
		// 30-60s for the larger pages; a 30s budget was tight enough
		// to fail real syncs. The pipeline retries on timeout (see
		// shouldRetry), so the 90s cap is per-attempt, not per-image.
		o.Timeout = 90 * time.Second
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

// Get implements Fetcher with retries on 429/5xx, dial errors, and
// per-attempt timeouts. Successful 200 responses include a small
// post-request jitter to avoid hammering the host.
func (h *HTTPFetcher) Get(ctx context.Context, req Request) (*Response, error) {
	var lastErr error
	for attempt := 1; attempt <= h.maxAttempts; attempt++ {
		resp, err := h.attempt(ctx, req)
		if err == nil {
			h.jitter()
			return resp, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == h.maxAttempts {
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

// Post issues a POST application/x-www-form-urlencoded request to
// req.URL. Cookie jar, User-Agent, retries, and Cloudflare-403
// handling mirror Get.
func (h *HTTPFetcher) Post(ctx context.Context, req Request, form url.Values) (*Response, error) {
	var lastErr error
	for attempt := 1; attempt <= h.maxAttempts; attempt++ {
		resp, err := h.postAttempt(ctx, req, form)
		if err == nil {
			h.jitter()
			return resp, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == h.maxAttempts {
			break
		}
		// Exponential backoff: 500ms, 1s, 2s … mirrors Get.
		wait := time.Duration(1<<(attempt-1)) * 500 * time.Millisecond
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, lastErr
}

func (h *HTTPFetcher) postAttempt(ctx context.Context, req Request, form url.Values) (*Response, error) {
	body := strings.NewReader(form.Encode())
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, req.URL, body)
	if err != nil {
		return nil, fmt.Errorf("build POST request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	httpReq.Header.Set("X-Requested-With", "XMLHttpRequest")
	httpReq.Header.Set("Accept", "text/html, */*; q=0.01")
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9")
	httpReq.Header.Set("User-Agent", h.userAgent)
	if req.Referer != "" {
		httpReq.Header.Set("Referer", req.Referer)
	}
	// Cookies come from h.client.Jar — http.Client.Do attaches them
	// automatically using the URL of the outgoing request.

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
		return nil, fmt.Errorf("unexpected status %d for POST %s", resp.StatusCode, req.URL)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return &Response{Body: raw, ContentType: resp.Header.Get("Content-Type")}, nil
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

// shouldRetry distinguishes caller-driven cancellations (don't retry)
// from our own h.client.Timeout firing (do retry). Both surface as
// context.DeadlineExceeded; the difference is whether the *caller's*
// context is still alive at the time of the error.
func shouldRetry(ctx context.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCloudflareExpired) {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		// Caller's context fired → propagate, don't retry.
		// Otherwise it was our per-request Timeout → retry.
		return ctx.Err() == nil
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		return true
	}
	// Network / dial / TLS / read errors fall through to retryable.
	return true
}
