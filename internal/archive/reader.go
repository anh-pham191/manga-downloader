// Package archive reads and writes .cbz archives for the
// downloader. The reader (this file) classifies existing entries
// against the layout package's conventions; the writer (added in
// Task 11) does stage-and-rename.
package archive

import (
	"archive/zip"
	"errors"
	"os"
	"path"

	"github.com/anhpham/downloader/internal/layout"
)

// Inspection captures what's already in a .cbz archive.
type Inspection struct {
	// Have is the set of "chap-NNNN[-K]" folder names that contain
	// at least one image entry.
	Have map[string]bool
	// HaveComments is the subset of Have whose chapter folder also
	// contains a zzz-comments.png entry.
	HaveComments map[string]bool
}

// Inspect reads cbzPath and classifies its entries. A missing file
// returns an empty Inspection (not an error) — that's the
// "archive does not exist yet" path.
func Inspect(cbzPath string) (Inspection, error) {
	out := Inspection{
		Have:         map[string]bool{},
		HaveComments: map[string]bool{},
	}
	zr, err := zip.OpenReader(cbzPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return out, err
	}
	defer zr.Close()

	for _, f := range zr.File {
		name := f.Name
		dir, base := path.Split(name)
		dir = path.Clean(dir) // strips trailing slash
		if dir == "." || dir == "" {
			continue
		}
		if base == layout.CommentsFilename {
			out.HaveComments[dir] = true
			continue
		}
		if layout.IsImageEntry(name) {
			out.Have[dir] = true
		}
	}

	// Only retain HaveComments entries whose folder also has images.
	for k := range out.HaveComments {
		if !out.Have[k] {
			delete(out.HaveComments, k)
		}
	}

	return out, nil
}

// InferredWidth wraps layout.InferredWidth over Inspection.Have.
func (i Inspection) InferredWidth() int {
	return layout.InferredWidth(i.Have)
}
