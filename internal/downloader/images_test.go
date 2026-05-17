package downloader

import "testing"

// TestExtFromURL_StripsQueryAndFragment proves the source CDN's
// cache-buster query strings don't leak into local filenames. Past
// behaviour produced filenames like `001.jpg?r=r8645456`, which
// broke archive.Inspect's image-entry regex and caused resyncs to
// see "empty" archives and re-download everything.
func TestExtFromURL_StripsQueryAndFragment(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"plain jpg", "https://cdn.example.com/path/001.jpg", "jpg"},
		{"jpg with cache-buster query", "https://cdn.example.com/001.jpg?r=r8645456", "jpg"},
		{"webp with multiple query params", "https://cdn.example.com/a.webp?v=1&t=2", "webp"},
		{"png with fragment", "https://cdn.example.com/img.PNG#top", "png"},
		{"jpeg with query AND fragment", "https://cdn.example.com/x.jpeg?q=1#f", "jpeg"},
		{"upper-case extension", "https://cdn.example.com/A.JPG", "jpg"},
		{"no extension", "https://cdn.example.com/file?q=1", "jpg"},
		{"trailing dot", "https://cdn.example.com/file.", "jpg"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extFromURL(tc.url); got != tc.want {
				t.Errorf("extFromURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}
