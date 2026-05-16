package pipeline

import (
	"github.com/anhpham/downloader/internal/archive"
	"github.com/anhpham/downloader/internal/layout"
	"github.com/anhpham/downloader/internal/site"
)

type Mode int

const (
	SyncComments Mode = iota
	Resume
	SyncManga
)

type TaskKind int

const (
	Render TaskKind = iota // render comments only
	Both                   // download images + render comments
)

type Task struct {
	Folder  string  // chap-NNNN[-K]
	Number  string  // raw chapter number
	URL     string  // chapter URL
	Kind    TaskKind
}

// Plan converts (mode, source chapter list, archive inspection,
// effective width) into the work list for the run.
func Plan(mode Mode, chapters []site.Chapter, insp archive.Inspection, width int) []Task {
	var out []Task
	for _, c := range chapters {
		folder := layout.Folder("", c.Number, width)
		in := insp.Have[folder]
		hasComments := insp.HaveComments[folder]

		switch mode {
		case SyncComments:
			if in && !hasComments {
				out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Render})
			}
		case Resume:
			if !in {
				out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Both})
			}
		case SyncManga:
			if !in {
				out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Both})
			} else if !hasComments {
				out = append(out, Task{Folder: folder, Number: c.Number, URL: c.URL, Kind: Render})
			}
		}
	}
	return out
}
