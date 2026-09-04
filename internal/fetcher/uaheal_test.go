package fetcher

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const macChromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/150.0.0.0 Safari/537.36"

func TestChromeMajor(t *testing.T) {
	tests := []struct {
		name string
		ua   string
		want int
		ok   bool
	}{
		{"mac chrome", macChromeUA, 150, true},
		{"full version tuple", "Chrome/151.0.7922.170 Safari/537.36", 151, true},
		{"no chrome token", "Mozilla/5.0 (Macintosh) Safari/605.1.15", 0, false},
		{"empty", "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := chromeMajor(tt.ua)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("chromeMajor(%q) = %d,%v; want %d,%v", tt.ua, got, ok, tt.want, tt.ok)
			}
		})
	}
}

// withChromeMajor must touch only the version: Cloudflare bins on the
// whole string, so a mangled platform token is as bad as a wrong major.
func TestWithChromeMajorPreservesRestOfUA(t *testing.T) {
	got := withChromeMajor(macChromeUA, 151)
	if !strings.Contains(got, "Chrome/151.0.0.0") {
		t.Fatalf("major not rewritten: %q", got)
	}
	if !strings.HasPrefix(got, "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)") ||
		!strings.HasSuffix(got, "Safari/537.36") {
		t.Fatalf("surrounding UA text altered: %q", got)
	}
}

func TestUACandidatesStoredFirstThenInstalledThenFan(t *testing.T) {
	got := uaCandidates(macChromeUA, 151, true)
	var majors []int
	for _, ua := range got {
		m, ok := chromeMajor(ua)
		if !ok {
			t.Fatalf("candidate is not chrome-shaped: %q", ua)
		}
		majors = append(majors, m)
	}
	if majors[0] != 150 {
		t.Fatalf("stored UA must be probed first, got %d", majors[0])
	}
	if majors[1] != 151 {
		t.Fatalf("installed version must be probed second, got %d", majors[1])
	}
	seen := map[int]bool{}
	for _, m := range majors {
		if seen[m] {
			t.Fatalf("duplicate candidate major %d in %v", m, majors)
		}
		seen[m] = true
	}
	for _, want := range []int{149, 152, 148, 153} {
		if !seen[want] {
			t.Fatalf("fan missing major %d: %v", want, majors)
		}
	}
}

// A non-Chrome UA has no version to vary, so probing variants would be
// noise; the stored value is the only candidate.
func TestUACandidatesNonChromeUAIsLeftAlone(t *testing.T) {
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Version/17.0 Safari/605.1.15"
	got := uaCandidates(ua, 151, true)
	if len(got) != 1 || got[0] != ua {
		t.Fatalf("uaCandidates(non-chrome) = %v, want [%q]", got, ua)
	}
}

// newTestFetcher builds a fetcher whose cookie jar targets srv.
func newTestFetcher(t *testing.T, srv *httptest.Server, ua string) *HTTPFetcher {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	f, err := New(&CookieFile{
		UserAgent: ua,
		Cookies:   []CookieRecord{{Name: "cf_clearance", Value: "tok", Domain: u.Hostname()}},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestHealUserAgentNoOpWhenStoredUAWorks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != macChromeUA {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, macChromeUA)
	ua, changed, err := f.HealUserAgent(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("changed = true for an already-correct UA")
	}
	if ua != macChromeUA {
		t.Fatalf("ua = %q, want unchanged", ua)
	}
}

// The real-world case: cookie was minted by Chrome 151, cookies.json
// still says 150. The run must repair itself rather than 403.
func TestHealUserAgentFindsNeighbouringMajor(t *testing.T) {
	want := withChromeMajor(macChromeUA, 151)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != want {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, macChromeUA)
	ua, changed, err := f.HealUserAgent(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}
	if ua != want {
		t.Fatalf("ua = %q, want %q", ua, want)
	}
	if f.UserAgent() != want {
		t.Fatalf("fetcher UA not updated: %q", f.UserAgent())
	}
}

// When every candidate 403s the cookie really is expired, and the user
// must be told to re-paste rather than shown a UA error.
func TestHealUserAgentExhaustedReportsExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	f := newTestFetcher(t, srv, macChromeUA)
	if _, _, err := f.HealUserAgent(context.Background(), srv.URL); err != ErrCloudflareExpired {
		t.Fatalf("err = %v, want ErrCloudflareExpired", err)
	}
}

func TestSaveUserAgentPreservesCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.json")
	orig := `{"user_agent":"old","cookies":[{"name":"cf_clearance","value":"tok","domain":".example.com"}]}`
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveUserAgent(path, "new-ua"); err != nil {
		t.Fatal(err)
	}
	var cf CookieFile
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &cf); err != nil {
		t.Fatal(err)
	}
	if cf.UserAgent != "new-ua" {
		t.Fatalf("user_agent = %q, want new-ua", cf.UserAgent)
	}
	if len(cf.Cookies) != 1 || cf.Cookies[0].Value != "tok" {
		t.Fatalf("cookies clobbered: %+v", cf.Cookies)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("expected no leftover .tmp file, stat err = %v", err)
	}
}
