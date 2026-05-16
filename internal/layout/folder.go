// Package layout owns the chapter folder-name convention. Both the
// image downloader (which creates these folders inside the .cbz)
// and the archive reader (which matches them) call into this
// package, so the convention has exactly one definition.
package layout

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/anhpham/downloader/internal/site"
)

// Width returns the zero-padding width wide enough for the largest
// chapter number in the list, with a floor of 4 so re-runs that
// pick up a longer list don't reshuffle older folder names.
func Width(chapters []site.Chapter) int {
	max := 0
	for _, c := range chapters {
		whole := c.Number
		if i := strings.IndexByte(whole, '.'); i != -1 {
			whole = whole[:i]
		}
		if n, err := strconv.Atoi(whole); err == nil && n > max {
			max = n
		}
	}
	w := digitWidth(max)
	if w < 4 {
		w = 4
	}
	return w
}

// Folder turns a published number like "227.5" into a filesystem-
// friendly, lexicographically-sortable name like
// <root>/chap-0227-5. If root is empty, the result has no root.
// Unparseable numbers fall back to a sanitised raw form.
func Folder(root, number string, width int) string {
	whole, frac, hasFrac := strings.Cut(number, ".")
	n, err := strconv.Atoi(whole)
	if err != nil {
		name := "chap-" + strings.ReplaceAll(number, "/", "_")
		if root == "" {
			return name
		}
		return filepath.Join(root, name)
	}
	name := fmt.Sprintf("chap-%0*d", width, n)
	if hasFrac {
		name += "-" + frac
	}
	if root == "" {
		return name
	}
	return filepath.Join(root, name)
}

func digitWidth(n int) int {
	if n <= 0 {
		return 1
	}
	w := 0
	for n > 0 {
		w++
		n /= 10
	}
	return w
}

// CommentsFilename is the fixed filename used for the rendered
// reader-comments page inside a chapter folder. The zzz- prefix
// makes it sort last alphabetically, so comic readers display it
// as the final page of the chapter.
const CommentsFilename = "zzz-comments.png"

// ImageName returns the filename for the N-th image in a chapter
// (1-indexed), zero-padded to a minimum of 3 digits. Anything past
// 999 still renders correctly but loses the leading zero.
func ImageName(index int, ext string) string {
	return fmt.Sprintf("%03d.%s", index, ext)
}

var chapterFolderPattern = regexp.MustCompile(`^chap-\d+(-\d+)?$`)
var imageEntryPattern = regexp.MustCompile(`^chap-\d+(-\d+)?/[^/]+\.(jpg|jpeg|png|webp)$`)

// IsImageEntry reports whether a zip-entry name matches the
// downloader's image-name convention inside a chapter folder. The
// comments PNG (CommentsFilename) is not counted as an image entry.
func IsImageEntry(zipEntryName string) bool {
	if !imageEntryPattern.MatchString(zipEntryName) {
		return false
	}
	if filepath.Base(zipEntryName) == CommentsFilename {
		return false
	}
	return true
}

// InferredWidth returns the zero-padding width observed across a
// set of chap-NNNN[-K] folder names. Returns 0 if the set is
// empty or contains no parseable chapter folders. If the set has
// inconsistent widths (e.g. from historical bugs), returns the
// maximum.
func InferredWidth(folders map[string]bool) int {
	max := 0
	for name := range folders {
		if !chapterFolderPattern.MatchString(name) {
			continue
		}
		// "chap-" prefix is 5 chars; the whole-number part runs
		// until "-" (fractional) or end of string.
		body := name[len("chap-"):]
		if i := strings.IndexByte(body, '-'); i != -1 {
			body = body[:i]
		}
		if len(body) > max {
			max = len(body)
		}
	}
	return max
}
