package mcp

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/anhpham/downloader/internal/archive"
	"github.com/anhpham/downloader/internal/pipeline"
	"github.com/anhpham/downloader/internal/registry"
)

type MangaEntry struct {
	Name             string `json:"name"`
	Path             string `json:"path"`
	SizeBytes        int64  `json:"size_bytes"`
	Chapters         int    `json:"chapters"`
	CommentsAttached int    `json:"comments_attached"`
	MissingComments  int    `json:"missing_comments"`
	ArchiveWidth     int    `json:"archive_width,omitempty"`
	URL              string `json:"url,omitempty"`
	LastSynced       string `json:"last_synced,omitempty"` // RFC3339
	Finished         bool   `json:"finished,omitempty"`
}

// ListManga walks root and returns one entry per *.cbz, sorted by name.
func ListManga(root string) ([]MangaEntry, error) {
	dirEntries, err := os.ReadDir(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []MangaEntry
	for _, de := range dirEntries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".cbz") {
			continue
		}
		name := strings.TrimSuffix(de.Name(), ".cbz")
		entry, err := buildEntry(root, name)
		if err != nil {
			return nil, err
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if reg, rerr := registry.Load(root); rerr == nil {
		for i := range out {
			fillRegistryFields(&out[i], reg)
		}
	}
	return out, nil
}

func fillRegistryFields(e *MangaEntry, reg *registry.Registry) {
	if en, ok := reg.Get(e.Name); ok {
		e.URL = en.URL
		e.Finished = en.Finished
		if !en.LastSynced.IsZero() {
			e.LastSynced = en.LastSynced.UTC().Format(time.RFC3339)
		}
	}
}

// InspectManga returns the entry for a single manga by name (without
// the .cbz suffix). Returns pipeline.ErrNoArchive if not found.
func InspectManga(root, name string) (MangaEntry, error) {
	path := filepath.Join(root, name+".cbz")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return MangaEntry{}, pipeline.ErrNoArchive
	} else if err != nil {
		return MangaEntry{}, err
	}
	entry, err := buildEntry(root, name)
	if err != nil {
		return MangaEntry{}, err
	}
	if reg, rerr := registry.Load(root); rerr == nil {
		fillRegistryFields(&entry, reg)
	}
	return entry, nil
}

func buildEntry(root, name string) (MangaEntry, error) {
	path := filepath.Join(root, name+".cbz")
	info, err := os.Stat(path)
	if err != nil {
		return MangaEntry{}, err
	}
	insp, err := archive.Inspect(path)
	if err != nil {
		return MangaEntry{}, err
	}
	chapters := len(insp.Have)
	commented := 0
	for folder := range insp.Have {
		if insp.HaveComments[folder] {
			commented++
		}
	}
	return MangaEntry{
		Name:             name,
		Path:             path,
		SizeBytes:        info.Size(),
		Chapters:         chapters,
		CommentsAttached: commented,
		MissingComments:  chapters - commented,
		ArchiveWidth:     insp.InferredWidth(),
	}, nil
}
