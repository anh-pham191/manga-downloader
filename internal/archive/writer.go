package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// StageAndRename merges every .ok-marked chapter subdirectory of
// scratchRoot into cbzPath. If cbzPath does not exist, a fresh
// archive is written. Existing entries are preserved byte-for-
// byte via the raw-copy mechanism. The tmp file is created in the
// same directory as cbzPath so os.Rename is atomic. New entries
// are written with Method=Store to match the archive's existing
// convention (and to avoid wasted CPU re-deflating PNG/JPEG).
//
// Per-chapter subdirs of scratchRoot are included only if they
// contain a `.ok` marker file. The marker itself is never written
// into the archive.
func StageAndRename(cbzPath, scratchRoot string) error {
	tmpPath := cbzPath + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	cleanup := func() { tmp.Close(); os.Remove(tmpPath) }

	zw := zip.NewWriter(tmp)

	// 1. Copy existing entries from cbzPath (if it exists) via raw.
	if zr, err := zip.OpenReader(cbzPath); err == nil {
		defer zr.Close()
		for _, f := range zr.File {
			rc, err := f.OpenRaw()
			if err != nil {
				cleanup()
				return fmt.Errorf("openraw %s: %w", f.Name, err)
			}
			hdr := f.FileHeader
			w, err := zw.CreateRaw(&hdr)
			if err != nil {
				cleanup()
				return fmt.Errorf("createraw %s: %w", f.Name, err)
			}
			if _, err := io.Copy(w, rc); err != nil {
				cleanup()
				return fmt.Errorf("copy %s: %w", f.Name, err)
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		cleanup()
		return fmt.Errorf("open existing: %w", err)
	}

	// 2. Walk scratchRoot, include only .ok-marked chapter dirs.
	chapters, err := readMarkedChapters(scratchRoot)
	if err != nil {
		cleanup()
		return err
	}
	sort.Strings(chapters)
	for _, ch := range chapters {
		chDir := filepath.Join(scratchRoot, ch)
		ents, err := os.ReadDir(chDir)
		if err != nil {
			cleanup()
			return err
		}
		// Stable order inside each chapter.
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
		for _, e := range ents {
			if e.Name() == ".ok" || e.IsDir() {
				continue
			}
			srcPath := filepath.Join(chDir, e.Name())
			raw, err := os.ReadFile(srcPath)
			if err != nil {
				cleanup()
				return err
			}
			w, err := zw.CreateHeader(&zip.FileHeader{
				Name:   ch + "/" + e.Name(),
				Method: zip.Store,
			})
			if err != nil {
				cleanup()
				return err
			}
			if _, err := w.Write(raw); err != nil {
				cleanup()
				return err
			}
		}
	}
	if err := zw.Close(); err != nil {
		cleanup()
		return fmt.Errorf("close writer: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("close tmp: %w", err)
	}

	// 3. Verify the tmp archive in pure Go.
	if err := verifyArchive(tmpPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("verify: %w", err)
	}

	// 4. Atomic rename.
	if err := os.Rename(tmpPath, cbzPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func readMarkedChapters(scratchRoot string) ([]string, error) {
	var chs []string
	err := filepath.WalkDir(scratchRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil { return err }
		if !d.IsDir() || p == scratchRoot { return nil }
		rel, _ := filepath.Rel(scratchRoot, p)
		if filepath.Dir(rel) != "." { return nil } // only first level
		if _, err := os.Stat(filepath.Join(p, ".ok")); err == nil {
			chs = append(chs, rel)
		}
		return nil
	})
	if err != nil { return nil, err }
	return chs, nil
}

func verifyArchive(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil { return err }
	defer zr.Close()
	for _, f := range zr.File {
		rc, err := f.OpenRaw()
		if err != nil { return err }
		if _, err := io.Copy(io.Discard, rc); err != nil { return err }
	}
	return nil
}
