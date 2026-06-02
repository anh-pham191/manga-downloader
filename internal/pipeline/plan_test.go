package pipeline

import (
	"testing"

	"github.com/anhpham/downloader/internal/archive"
	"github.com/anhpham/downloader/internal/site"
)

func chap(n string) site.Chapter { return site.Chapter{Number: n, URL: "u-" + n} }

func TestPlan_Matrix(t *testing.T) {
	chapters := []site.Chapter{chap("1"), chap("2"), chap("3")}
	have := map[string]bool{"chap-0001": true, "chap-0002": true}
	haveComments := map[string]bool{"chap-0001": true}
	insp := archive.Inspection{Have: have, HaveComments: haveComments}

	cases := []struct {
		name string
		mode Mode
		want map[string]TaskKind
	}{
		{
			name: "sync-comments",
			mode: SyncComments,
			want: map[string]TaskKind{
				"chap-0002": Render, // in archive, no comments → render
			},
		},
		{
			name: "resume",
			mode: Resume,
			want: map[string]TaskKind{
				"chap-0003": Both, // not in archive → download + render
			},
		},
		{
			name: "sync-manga",
			mode: SyncManga,
			want: map[string]TaskKind{
				"chap-0002": Render,
				"chap-0003": Both,
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tasks := Plan(c.mode, chapters, insp, 4 /*width*/, false /*refresh*/)
			got := map[string]TaskKind{}
			for _, tk := range tasks {
				got[tk.Folder] = tk.Kind
			}
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for k, v := range c.want {
				if got[k] != v {
					t.Errorf("folder %q: got %v, want %v", k, got[k], v)
				}
			}
		})
	}
}

func TestPlan_WidthStabilityFromArchive(t *testing.T) {
	chapters := []site.Chapter{chap("1"), chap("10000")}
	have := map[string]bool{"chap-0001": true, "chap-9999": true} // 4-wide
	insp := archive.Inspection{Have: have, HaveComments: nil}
	tasks := Plan(SyncManga, chapters, insp, 5, false)
	got := map[string]bool{}
	for _, t := range tasks {
		got[t.Folder] = true
	}
	if !got["chap-00001"] {
		t.Errorf("Plan should propose folder chap-00001 with width=5; got %v", got)
	}
}

func TestPlan_RefreshRendersExistingComments(t *testing.T) {
	chapters := []site.Chapter{chap("1"), chap("2"), chap("3")}
	have := map[string]bool{"chap-0001": true, "chap-0002": true}
	haveComments := map[string]bool{"chap-0001": true} // chap-0001 already has comments
	insp := archive.Inspection{Have: have, HaveComments: haveComments}

	// refresh=false: only chap-0002 (in archive, missing comments) → Render.
	noRefresh := Plan(SyncComments, chapters, insp, 4, false)
	if len(noRefresh) != 1 || noRefresh[0].Folder != "chap-0002" {
		t.Fatalf("refresh=false: got %v, want [chap-0002]", noRefresh)
	}

	// refresh=true: both archived chapters re-render, incl. chap-0001
	// which already has comments. chap-0003 is not in the archive, so
	// sync-comments never touches it.
	got := map[string]TaskKind{}
	for _, tk := range Plan(SyncComments, chapters, insp, 4, true) {
		got[tk.Folder] = tk.Kind
	}
	want := map[string]TaskKind{"chap-0001": Render, "chap-0002": Render}
	if len(got) != len(want) {
		t.Fatalf("refresh=true: got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("folder %q: got %v, want %v", k, got[k], v)
		}
	}
	if _, ok := got["chap-0003"]; ok {
		t.Error("chap-0003 not in archive; should not be planned even with refresh")
	}
}
