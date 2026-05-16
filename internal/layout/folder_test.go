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
