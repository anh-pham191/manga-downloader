package layout

import (
	"testing"

	"github.com/anhpham/downloader/internal/site"
)

func TestWidth(t *testing.T) {
	cases := []struct {
		name string
		nums []string
		want int
	}{
		{"empty", nil, 4},
		{"under floor", []string{"1", "2", "3"}, 4},
		{"at floor", []string{"9999"}, 4},
		{"past floor", []string{"10000"}, 5},
		{"fractional", []string{"227.5", "228"}, 4},
		{"five digit max", []string{"99", "10500"}, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var chs []site.Chapter
			for _, n := range c.nums {
				chs = append(chs, site.Chapter{Number: n})
			}
			if got := Width(chs); got != c.want {
				t.Fatalf("Width(%v) = %d, want %d", c.nums, got, c.want)
			}
		})
	}
}

func TestFolder(t *testing.T) {
	cases := []struct {
		root, number string
		width        int
		want         string
	}{
		{"", "1", 4, "chap-0001"},
		{"", "227", 4, "chap-0227"},
		{"", "227.5", 4, "chap-0227-5"},
		{"out", "1", 4, "out/chap-0001"},
		{"", "10500", 5, "chap-10500"},
		{"", "garbage", 4, "chap-garbage"},
	}
	for _, c := range cases {
		t.Run(c.want, func(t *testing.T) {
			if got := Folder(c.root, c.number, c.width); got != c.want {
				t.Fatalf("Folder(%q,%q,%d) = %q, want %q",
					c.root, c.number, c.width, got, c.want)
			}
		})
	}
}

func TestInferredWidth(t *testing.T) {
	cases := []struct {
		name    string
		folders []string
		want    int
	}{
		{"empty", nil, 0},
		{"four wide", []string{"chap-0001", "chap-0227", "chap-9999"}, 4},
		{"five wide", []string{"chap-10500", "chap-00001"}, 5},
		{"mixed picks max", []string{"chap-0001", "chap-10500"}, 5},
		{"fractional ignored for width", []string{"chap-0227-5"}, 4},
		{"unparseable ignored", []string{"chap-foo", "chap-0001"}, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			set := map[string]bool{}
			for _, f := range c.folders {
				set[f] = true
			}
			if got := InferredWidth(set); got != c.want {
				t.Fatalf("InferredWidth(%v) = %d, want %d", c.folders, got, c.want)
			}
		})
	}
}

func TestImageName(t *testing.T) {
	cases := []struct {
		idx  int
		ext  string
		want string
	}{
		{1, "jpg", "001.jpg"},
		{99, "jpg", "099.jpg"},
		{100, "webp", "100.webp"},
		{1000, "jpg", "1000.jpg"}, // overflow past 3 wide is fine
	}
	for _, c := range cases {
		if got := ImageName(c.idx, c.ext); got != c.want {
			t.Errorf("ImageName(%d,%q) = %q, want %q", c.idx, c.ext, got, c.want)
		}
	}
}

func TestIsImageEntry(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"jpg in chapter", "chap-0001/001.jpg", true},
		{"webp in fractional chapter", "chap-0227-5/042.webp", true},
		{"comments PNG is NOT image", "chap-0001/zzz-comments.png", false},
		{"image with longer name", "chap-0001/cover.png", true},
		{"gif is an image (Sket Dance ch.9 was re-downloaded every run)", "chap-0009/001.gif", true},
		{"avif is an image", "chap-0001/001.avif", true},
		{"upper-case extension", "chap-0001/001.JPG", true},
		{"DS_Store ignored", "chap-0001/.DS_Store", false},
		{"non-chapter dir", "extras/note.jpg", false},
		{"root file ignored", "001.jpg", false},
		{"nested deeper ignored", "chap-0001/x/y.jpg", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsImageEntry(c.in); got != c.want {
				t.Fatalf("IsImageEntry(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestCommentsFilename(t *testing.T) {
	if CommentsFilename != "zzz-comments.png" {
		t.Fatalf("CommentsFilename = %q", CommentsFilename)
	}
}
