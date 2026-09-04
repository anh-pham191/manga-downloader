// Package registry persists the source URL of every archived manga so
// update-all can find new chapters without the operator re-pasting
// URLs. One JSON file per manga root.
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const FileName = ".registry.json"

type Entry struct {
	URL        string    `json:"url"`
	Added      time.Time `json:"added"`
	LastSynced time.Time `json:"last_synced,omitzero"`
}

type Registry struct {
	Version int              `json:"version"`
	Manga   map[string]Entry `json:"manga"`
	root    string
}

// Load reads <root>/.registry.json. A missing file yields an empty
// registry; a malformed one is an error so we never silently wipe it.
func Load(root string) (*Registry, error) {
	r := &Registry{Version: 1, Manga: map[string]Entry{}, root: root}
	b, err := os.ReadFile(filepath.Join(root, FileName))
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, r); err != nil {
		return nil, fmt.Errorf("registry %s is corrupt: %w", FileName, err)
	}
	if r.Manga == nil {
		r.Manga = map[string]Entry{}
	}
	if r.Version == 0 {
		r.Version = 1
	}
	r.root = root
	return r, nil
}

// Save writes atomically (tmp + rename) under a file lock so two
// concurrent downloader processes cannot interleave writes.
func (r *Registry) Save() error {
	if err := os.MkdirAll(r.root, 0o755); err != nil {
		return err
	}
	lockPath := filepath.Join(r.root, FileName+".lock")
	lock := flock.New(lockPath)
	if err := lock.Lock(); err != nil {
		return err
	}
	defer lock.Unlock()

	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	final := filepath.Join(r.root, FileName)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, final)
}

func (r *Registry) Upsert(name, rawURL string) {
	e, ok := r.Manga[name]
	if !ok {
		e.Added = time.Now()
	}
	e.URL = rawURL
	r.Manga[name] = e
}

func (r *Registry) Touch(name string, at time.Time) {
	e, ok := r.Manga[name]
	if !ok {
		return
	}
	e.LastSynced = at
	r.Manga[name] = e
}

func (r *Registry) Get(name string) (Entry, bool) {
	e, ok := r.Manga[name]
	return e, ok
}

func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.Manga))
	for k := range r.Manga {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// RewriteHost swaps the scheme+host of every stored URL. newHost may
// be "example.com" (https assumed) or "http://example.com".
func (r *Registry) RewriteHost(newHost string) (int, error) {
	newHost = strings.TrimSpace(newHost)
	if !strings.Contains(newHost, "://") {
		newHost = "https://" + newHost
	}
	nu, err := url.Parse(newHost)
	if err != nil || nu.Host == "" {
		return 0, fmt.Errorf("invalid host %q", newHost)
	}
	n := 0
	for name, e := range r.Manga {
		u, err := url.Parse(e.URL)
		if err != nil {
			continue
		}
		u.Scheme = nu.Scheme
		u.Host = nu.Host
		e.URL = u.String()
		r.Manga[name] = e
		n++
	}
	return n, nil
}

// Root returns the manga root this registry was loaded from.
func (r *Registry) Root() string {
	return r.root
}
