package fetcher

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
)

// CookieFile is the on-disk format users paste credentials into.
// JSON is chosen over TOML/YAML to keep the dependency footprint
// minimal — every dep here is in the standard library.
type CookieFile struct {
	UserAgent string         `json:"user_agent"`
	Cookies   []CookieRecord `json:"cookies"`
}

type CookieRecord struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path,omitempty"`
}

// LoadCookieFile reads a JSON cookie file from disk. The format is
// forgiving: missing Path defaults to "/", and an empty UserAgent
// triggers a fallback to the package-default UA.
func LoadCookieFile(path string) (*CookieFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cf CookieFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if len(cf.Cookies) == 0 {
		return nil, fmt.Errorf("%s: no cookies defined", path)
	}
	if cf.UserAgent == "" {
		cf.UserAgent = defaultUA
	}
	for i := range cf.Cookies {
		if cf.Cookies[i].Path == "" {
			cf.Cookies[i].Path = "/"
		}
	}
	return &cf, nil
}

// jarFromCookies builds a cookie jar that net/http will attach the
// given cookies to whenever a request matches their domain/path.
func jarFromCookies(cookies []CookieRecord) (*cookiejar.Jar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookie jar: %w", err)
	}
	// cookiejar groups by URL, not by domain alone, so we materialise
	// one URL per cookie domain and stash all cookies for that domain
	// against it.
	byDomain := map[string][]*http.Cookie{}
	for _, c := range cookies {
		dom := c.Domain
		if dom == "" {
			return nil, fmt.Errorf("cookie %q has empty domain", c.Name)
		}
		byDomain[dom] = append(byDomain[dom], &http.Cookie{
			Name:   c.Name,
			Value:  c.Value,
			Domain: dom,
			Path:   c.Path,
		})
	}
	for dom, cs := range byDomain {
		host := dom
		if len(host) > 0 && host[0] == '.' {
			host = host[1:]
		}
		u := &url.URL{Scheme: "https", Host: host, Path: "/"}
		jar.SetCookies(u, cs)
	}
	return jar, nil
}

// defaultUA matches a recent stable Chrome on macOS, used only when
// the cookie file omits user_agent. Pasted cookies are bound to the
// UA of the browser that solved the challenge, so users should
// always populate it explicitly.
const defaultUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
