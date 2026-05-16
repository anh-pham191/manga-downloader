package fetcher

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPost_SendsFormAndCookieAndReferer(t *testing.T) {
	var sawBody, sawCookie, sawReferer, sawUA, sawCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		sawBody = string(b)
		sawCookie = r.Header.Get("Cookie")
		sawReferer = r.Header.Get("Referer")
		sawUA = r.Header.Get("User-Agent")
		sawCT = r.Header.Get("Content-Type")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<article class=\"info-comment\"></article>"))
	}))
	defer srv.Close()

	// Parse the test server host so we can set the cookie domain to
	// match. jarFromCookies rejects empty domains and the cookie jar
	// only attaches cookies whose domain matches the request URL.
	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// srvURL.Host is "127.0.0.1:<port>"; the jar matches on hostname
	// only, so use the full host string as the domain.
	srvHost := srvURL.Hostname() // "127.0.0.1"

	cf := &CookieFile{
		UserAgent: "test-agent",
		Cookies:   []CookieRecord{{Name: "cf_clearance", Value: "TOKEN", Domain: srvHost}},
	}
	f, err := New(cf, Options{})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := f.Post(context.Background(),
		Request{URL: srv.URL, Referer: "https://example.com/page"},
		url.Values{"book_id": {"13680"}, "page": {"2"}})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}

	if !strings.Contains(string(resp.Body), "info-comment") {
		t.Errorf("body = %q", resp.Body)
	}
	if !strings.Contains(sawBody, "book_id=13680") ||
		!strings.Contains(sawBody, "page=2") {
		t.Errorf("server saw body = %q", sawBody)
	}
	if !strings.Contains(sawCookie, "cf_clearance=TOKEN") {
		t.Errorf("server saw cookie = %q", sawCookie)
	}
	if sawReferer != "https://example.com/page" {
		t.Errorf("server saw referer = %q", sawReferer)
	}
	if sawUA != "test-agent" {
		t.Errorf("server saw UA = %q", sawUA)
	}
	if !strings.HasPrefix(sawCT, "application/x-www-form-urlencoded") {
		t.Errorf("server saw content-type = %q", sawCT)
	}
}

func TestPost_403IsCloudflareExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	srvURL, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	srvHost := srvURL.Hostname()

	f, err := New(&CookieFile{UserAgent: "ua", Cookies: []CookieRecord{{Name: "x", Value: "y", Domain: srvHost}}}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.Post(context.Background(), Request{URL: srv.URL}, url.Values{})
	if !errors.Is(err, ErrCloudflareExpired) {
		t.Fatalf("err = %v, want ErrCloudflareExpired", err)
	}
}

// TestGet_RetriesOnClientTimeout proves that a per-attempt timeout
// (h.client.Timeout firing) is retried, not surfaced immediately.
// The test server sleeps long enough to trip a tiny client timeout
// on the first attempt, then responds promptly on subsequent ones.
func TestGet_RetriesOnClientTimeout(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			// Block long enough to trip the client timeout below.
			time.Sleep(100 * time.Millisecond)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	srvHost, _ := url.Parse(srv.URL)
	f, err := New(
		&CookieFile{UserAgent: "ua", Cookies: []CookieRecord{{Name: "x", Value: "y", Domain: srvHost.Hostname()}}},
		Options{Timeout: 25 * time.Millisecond, MinDelay: time.Millisecond, MaxDelay: time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := f.Get(context.Background(), Request{URL: srv.URL})
	if err != nil {
		t.Fatalf("Get: %v (calls=%d)", err, calls)
	}
	if string(resp.Body) != "ok" {
		t.Fatalf("body = %q, want %q", string(resp.Body), "ok")
	}
	if calls < 2 {
		t.Fatalf("server saw %d call(s); expected at least 2 (retry must fire)", calls)
	}
}

// TestGet_DoesNotRetryCallerCancel asserts that a *caller* cancelling
// the context shortcuts the retry loop — we don't want to keep
// hammering on something the parent told us to drop.
func TestGet_DoesNotRetryCallerCancel(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// Sleep so the caller has time to cancel mid-request.
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	srvHost, _ := url.Parse(srv.URL)
	f, err := New(
		&CookieFile{UserAgent: "ua", Cookies: []CookieRecord{{Name: "x", Value: "y", Domain: srvHost.Hostname()}}},
		Options{Timeout: time.Second, MinDelay: time.Millisecond, MaxDelay: time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err = f.Get(ctx, Request{URL: srv.URL})
	if err == nil {
		t.Fatal("expected error from cancelled ctx, got nil")
	}
	if calls > 1 {
		t.Fatalf("server saw %d call(s); expected 1 (no retry after caller cancel)", calls)
	}
}
