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
