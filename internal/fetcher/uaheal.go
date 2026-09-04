package fetcher

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
)

// chromeVersionRE matches the "Chrome/<major>.<rest>" token that
// every Chrome-derived User-Agent carries. Only the major version
// is meaningful: Chrome froze the remaining components at 0.0.0.
var chromeVersionRE = regexp.MustCompile(`Chrome/(\d+)(\.[\d.]+)?`)

// chromeMajor extracts the Chrome major version from a User-Agent.
// Reports false for UAs that aren't Chrome-shaped, which is the
// signal to leave them alone rather than guess at variants.
func chromeMajor(ua string) (int, bool) {
	m := chromeVersionRE.FindStringSubmatch(ua)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// withChromeMajor rewrites only the major version inside ua,
// preserving the rest of the string verbatim. Cloudflare bins on
// the whole UA, so every other token has to survive untouched.
func withChromeMajor(ua string, major int) string {
	return chromeVersionRE.ReplaceAllString(ua, fmt.Sprintf("Chrome/%d.0.0.0", major))
}

// installedChromeMajor reads the Chrome major version from the
// installed app bundle. This is a hint, not an answer: the bundle
// on disk can be a version ahead of the still-running browser that
// actually solved the challenge, which is exactly the mismatch this
// file exists to repair. It therefore only seeds a candidate.
func installedChromeMajor() (int, bool) {
	if runtime.GOOS != "darwin" {
		return 0, false
	}
	out, err := exec.Command("defaults", "read",
		"/Applications/Google Chrome.app/Contents/Info",
		"CFBundleShortVersionString").Output()
	if err != nil {
		return 0, false
	}
	m := regexp.MustCompile(`^(\d+)`).FindSubmatch(out)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0, false
	}
	return n, true
}

// uaCandidates returns User-Agents to try, stored value first, then
// the installed-bundle version, then majors fanning outward from the
// stored one. The fan covers the ordinary case where Chrome updated
// (or the browser lagged an update) since the UA was last pasted.
func uaCandidates(stored string, installed int, installedOK bool) []string {
	major, ok := chromeMajor(stored)
	if !ok {
		return []string{stored}
	}
	majors := []int{major}
	if installedOK && installed != major {
		majors = append(majors, installed)
	}
	for d := 1; d <= 3; d++ {
		majors = append(majors, major+d, major-d)
	}

	seen := map[int]bool{}
	var out []string
	for _, m := range majors {
		if m <= 0 || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, withChromeMajor(stored, m))
	}
	return out
}

// probe issues a bare GET with the given User-Agent and reports the
// status code. It deliberately bypasses Get's retry and 403-mapping
// so the caller can distinguish "wrong UA" from "expired cookie".
func (h *HTTPFetcher) probe(ctx context.Context, url, ua string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

// UserAgent reports the User-Agent currently in use.
func (h *HTTPFetcher) UserAgent() string { return h.userAgent }

// HealUserAgent verifies the stored User-Agent against probeURL and,
// if Cloudflare rejects it, searches nearby Chrome major versions for
// one the existing cf_clearance is bound to. It reports the healed UA
// and whether it differs from the stored one.
//
// The cookie is minted against the UA of the browser that solved the
// challenge, and nothing on disk reliably records which browser that
// was — so probing is the only way to recover it without asking the
// user to re-paste.
func (h *HTTPFetcher) HealUserAgent(ctx context.Context, probeURL string) (string, bool, error) {
	installed, installedOK := installedChromeMajor()
	cands := uaCandidates(h.userAgent, installed, installedOK)
	for i, ua := range cands {
		code, err := h.probe(ctx, probeURL, ua)
		if err != nil {
			return "", false, err
		}
		if code == http.StatusOK {
			h.userAgent = ua
			return ua, i != 0, nil
		}
		if code != http.StatusForbidden {
			// Not a Cloudflare verdict — a different UA won't help.
			return "", false, fmt.Errorf("probe %s: unexpected status %d", probeURL, code)
		}
	}
	return "", false, ErrCloudflareExpired
}

// SaveUserAgent rewrites user_agent in the cookie file, leaving the
// cookies themselves untouched so a healed UA survives to the next
// run instead of being re-probed every time.
func SaveUserAgent(path, ua string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	enc, err := json.Marshal(ua)
	if err != nil {
		return err
	}
	raw["user_agent"] = enc
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
