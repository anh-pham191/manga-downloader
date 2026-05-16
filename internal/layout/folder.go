// Package layout owns the chapter folder-name convention. Both the
// image downloader (which creates these folders inside the .cbz)
// and the archive reader (which matches them) call into this
// package, so the convention has exactly one definition.
package layout

import (
	"fmt"
	"path/filepath"
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
