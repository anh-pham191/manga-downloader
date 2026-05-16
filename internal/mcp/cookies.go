package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
)

const defaultCookieDomain = ".truyenqqko.com"

// CookieStatusResult is what the get_cookie_status tool returns.
type CookieStatusResult struct {
	CookiePath   string `json:"cookie_path"`
	HasClearance bool   `json:"has_clearance"`
	Mtime        string `json:"mtime,omitempty"`
	Last8        string `json:"last8,omitempty"`
}

// CookieStatus inspects the cookie file without leaking the full
// cf_clearance value. A missing file is not an error — it reports
// HasClearance=false.
func CookieStatus(path string) (CookieStatusResult, error) {
	out := CookieStatusResult{CookiePath: path}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	cf, err := loadCookieFile(path)
	if err != nil {
		return out, err
	}
	for _, c := range cf.Cookies {
		if c.Name == "cf_clearance" && c.Value != "" {
			out.HasClearance = true
			if len(c.Value) >= 8 {
				out.Last8 = c.Value[len(c.Value)-8:]
			} else {
				out.Last8 = c.Value
			}
			break
		}
	}
	out.Mtime = info.ModTime().UTC().Format(time.RFC3339)
	return out, nil
}

// UpdateClearance writes a new cf_clearance value while preserving
// every other field of the cookie file. Empty/whitespace values are
// rejected. The write is crash-safe (temp file + rename).
func UpdateClearance(path, value, userAgent, domain string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("cf_clearance value is empty")
	}
	cf, err := loadOrInit(path)
	if err != nil {
		return err
	}
	if userAgent != "" {
		cf.UserAgent = userAgent
	}
	if cf.UserAgent == "" {
		cf.UserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	}
	setOrAppend(cf, "cf_clearance", value, pickDomain(cf, domain))
	return writeAtomic(path, cf)
}

func pickDomain(cf *fetcher.CookieFile, requested string) string {
	if requested != "" {
		return requested
	}
	for _, c := range cf.Cookies {
		if c.Name == "cf_clearance" && c.Domain != "" {
			return c.Domain
		}
	}
	return defaultCookieDomain
}

// setOrAppend updates the value of the first cookie with this name,
// or appends a new entry. When updating an existing entry, a non-empty
// `domain` overwrites the stored one — this lets callers fix a
// wrong-domain cookie via update_cookie, not just rotate the value.
// (Source sites rebrand occasionally; the user shouldn't have to
// hand-edit cookies.json for that.)
func setOrAppend(cf *fetcher.CookieFile, name, value, domain string) {
	for i := range cf.Cookies {
		if cf.Cookies[i].Name == name {
			cf.Cookies[i].Value = value
			if domain != "" {
				cf.Cookies[i].Domain = domain
			}
			return
		}
	}
	cf.Cookies = append(cf.Cookies, fetcher.CookieRecord{Name: name, Value: value, Domain: domain})
}

func loadOrInit(path string) (*fetcher.CookieFile, error) {
	cf, err := loadCookieFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &fetcher.CookieFile{}, nil
	}
	return cf, err
}

func loadCookieFile(path string) (*fetcher.CookieFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cf fetcher.CookieFile
	if err := json.Unmarshal(b, &cf); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &cf, nil
}

func writeAtomic(path string, cf *fetcher.CookieFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	enc := json.NewEncoder(tmp)
	enc.SetIndent("", "  ")
	if err := enc.Encode(cf); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
