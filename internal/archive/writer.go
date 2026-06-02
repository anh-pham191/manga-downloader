package archive

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// zip64SafeThreshold is the existing-archive size above which we
// shell out to the OS `zip` command instead of using Go's
// archive/zip. Go's writer mishandles zip64 metadata for raw-copied
// entries when the central directory crosses the 4 GB offset
// boundary, producing an invalid archive. 3.5 GB leaves headroom
// for additions before the output crosses Go's working ceiling.
//
// Exported as a var so tests can override it without building
// multi-GB fixtures.
var zip64SafeThreshold int64 = 3_500_000_000

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
//
// For archives at or above zip64SafeThreshold, the implementation
// shells out to the OS `zip` command — Go's archive/zip raw-copy
// path can corrupt zip64 central-directory offsets at that size.
func StageAndRename(cbzPath, scratchRoot string) error {
	if useOSZip(cbzPath) {
		return stageViaOSZip(cbzPath, scratchRoot)
	}
	return stageViaGoZip(cbzPath, scratchRoot)
}

// useOSZip returns true when the existing archive is large enough
// that Go's archive/zip raw-copy is unsafe. A missing file means
// we're creating a fresh archive — always small enough for Go.
func useOSZip(cbzPath string) bool {
	info, err := os.Stat(cbzPath)
	if err != nil {
		return false
	}
	return info.Size() >= zip64SafeThreshold
}

// stageViaGoZip is the fast path: copy existing entries raw, append
// new entries via archive/zip. Safe for archives below the zip64
// threshold.
func stageViaGoZip(cbzPath, scratchRoot string) error {
	tmpPath := cbzPath + ".tmp"
	tmp, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create tmp: %w", err)
	}
	cleanup := func() { tmp.Close(); os.Remove(tmpPath) }

	zw := zip.NewWriter(tmp)

	// Read the .ok-marked scratch chapters first and compute the set of
	// entry names they will write, so existing archive entries of the
	// same name are dropped (replaced) rather than duplicated.
	chapters, err := readMarkedChapters(scratchRoot)
	if err != nil {
		cleanup()
		return err
	}
	sort.Strings(chapters)

	scratchNames := map[string]bool{}
	for _, ch := range chapters {
		ents, err := os.ReadDir(filepath.Join(scratchRoot, ch))
		if err != nil {
			cleanup()
			return err
		}
		for _, e := range ents {
			if e.Name() == ".ok" || e.IsDir() {
				continue
			}
			scratchNames[ch+"/"+e.Name()] = true
		}
	}

	// 1. Copy existing entries from cbzPath (if it exists) via raw,
	//    skipping any name a scratch chapter will overwrite.
	if zr, err := zip.OpenReader(cbzPath); err == nil {
		defer zr.Close()
		for _, f := range zr.File {
			if scratchNames[f.Name] {
				continue
			}
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

	// 2. Append scratch entries (already gathered above).
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

// stageViaOSZip copies the existing .cbz to a tmp file, appends the
// scratch chapters via the OS `zip` command (which writes correct
// zip64 metadata), verifies, and atomically renames. The OS zip
// binary ships with macOS and is standard on every supported Linux
// distro; if it's missing the user gets a clear error message.
func stageViaOSZip(cbzPath, scratchRoot string) error {
	tmpPath := cbzPath + ".tmp"

	if err := copyFile(cbzPath, tmpPath); err != nil {
		return fmt.Errorf("copy existing: %w", err)
	}

	chapters, err := readMarkedChapters(scratchRoot)
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	sort.Strings(chapters)

	var files []string
	for _, ch := range chapters {
		chDir := filepath.Join(scratchRoot, ch)
		ents, err := os.ReadDir(chDir)
		if err != nil {
			os.Remove(tmpPath)
			return err
		}
		sort.Slice(ents, func(i, j int) bool { return ents[i].Name() < ents[j].Name() })
		for _, e := range ents {
			if e.Name() == ".ok" || e.IsDir() {
				continue
			}
			files = append(files, filepath.Join(ch, e.Name()))
		}
	}

	if len(files) > 0 {
		args := append([]string{"-X", "-0", tmpPath}, files...)
		cmd := exec.Command("zip", args...)
		cmd.Dir = scratchRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("os zip (%s): %w: %s", "zip "+args[0], err, out)
		}
	}

	if err := verifyArchive(tmpPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("verify: %w", err)
	}

	if err := os.Rename(tmpPath, cbzPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

func copyFile(src, dst string) error {
	s, err := os.Open(src)
	if err != nil {
		return err
	}
	defer s.Close()
	d, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer d.Close()
	if _, err := io.Copy(d, s); err != nil {
		return err
	}
	return d.Sync()
}

func readMarkedChapters(scratchRoot string) ([]string, error) {
	var chs []string
	err := filepath.WalkDir(scratchRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() || p == scratchRoot {
			return nil
		}
		rel, _ := filepath.Rel(scratchRoot, p)
		if filepath.Dir(rel) != "." {
			return nil
		} // only first level
		if _, err := os.Stat(filepath.Join(p, ".ok")); err == nil {
			chs = append(chs, rel)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return chs, nil
}

func verifyArchive(path string) error {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return err
	}
	defer zr.Close()
	for _, f := range zr.File {
		rc, err := f.OpenRaw()
		if err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, rc); err != nil {
			return err
		}
	}
	return nil
}
