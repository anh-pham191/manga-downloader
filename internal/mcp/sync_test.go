package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/pipeline"
	"github.com/anhpham/downloader/internal/registry"
)

func TestRunSync_MapsCFExpired(t *testing.T) {
	exec := &SyncExecutor{
		Root:        t.TempDir(),
		CookiesPath: t.TempDir() + "/cookies.json",
		RunState:    &RunState{},
		runFn: func(ctx context.Context, opts pipeline.Opts) error {
			return fetcher.ErrCloudflareExpired
		},
	}
	// Seed a cookie file so LoadCookieFile succeeds in production path.
	seedCookieFile(t, exec.CookiesPath)
	_, err := exec.Run(context.Background(), pipeline.SyncManga, SyncInput{
		URL: "https://example.com/manga", Name: "X",
	})
	var te *ToolError
	if !errors.As(err, &te) || te.Code != CodeCFTokenExpired {
		t.Fatalf("err = %v, want CF_TOKEN_EXPIRED", err)
	}
}

func TestRunSync_HappyPath(t *testing.T) {
	var gotOpts pipeline.Opts
	exec := &SyncExecutor{
		Root:        t.TempDir(),
		CookiesPath: t.TempDir() + "/cookies.json",
		RunState:    &RunState{},
		runFn: func(ctx context.Context, opts pipeline.Opts) error {
			gotOpts = opts
			return nil
		},
	}
	seedCookieFile(t, exec.CookiesPath)
	out, err := exec.Run(context.Background(), pipeline.SyncComments, SyncInput{
		URL: "https://x", Name: "X", Concurrency: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Mode != "sync-comments" {
		t.Fatalf("mode = %q", out.Mode)
	}
	if gotOpts.Concurrency != 7 {
		t.Fatalf("concurrency passed through wrong: %d", gotOpts.Concurrency)
	}
	if gotOpts.Name != "X" {
		t.Fatalf("name passed through wrong: %q", gotOpts.Name)
	}
}

func TestRunSync_BlockedWhileActive(t *testing.T) {
	rs := &RunState{}
	_, cancel := context.WithCancel(context.Background())
	_, _ = rs.Acquire("Other", "resume", cancel)
	exec := &SyncExecutor{
		Root: t.TempDir(), CookiesPath: t.TempDir() + "/cookies.json",
		RunState: rs,
		runFn:    func(ctx context.Context, _ pipeline.Opts) error { return nil },
	}
	seedCookieFile(t, exec.CookiesPath)
	_, err := exec.Run(context.Background(), pipeline.Resume, SyncInput{URL: "x", Name: "Y"})
	var te *ToolError
	if !errors.As(err, &te) || te.Code != CodeRunInProgress {
		t.Fatalf("err = %v, want RUN_IN_PROGRESS", err)
	}
}

func TestRunSync_DerivesNameFromURL(t *testing.T) {
	exec := &SyncExecutor{
		Root: t.TempDir(), CookiesPath: t.TempDir() + "/cookies.json",
		RunState: &RunState{},
		runFn:    func(ctx context.Context, _ pipeline.Opts) error { return nil },
	}
	seedCookieFile(t, exec.CookiesPath)
	out, err := exec.Run(context.Background(), pipeline.SyncManga, SyncInput{
		URL: "https://truyenqqko.com/truyen-tranh/gintama-216",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "gintama-216" {
		t.Fatalf("derived name = %q", out.Name)
	}
}

func TestSyncExecutor_RecordsRegistry(t *testing.T) {
	root := t.TempDir()
	exec := &SyncExecutor{
		Root:        root,
		CookiesPath: t.TempDir() + "/cookies.json",
		RunState:    &RunState{},
		runFn:       func(context.Context, pipeline.Opts) error { return nil },
	}
	seedCookieFile(t, exec.CookiesPath)
	if _, err := exec.Run(context.Background(), pipeline.Resume, SyncInput{URL: "https://truyenqqko.com/truyen-tranh/gintama-216"}); err != nil {
		t.Fatal(err)
	}
	reg, _ := registry.Load(root)
	en, ok := reg.Get("gintama-216")
	if !ok || en.URL != "https://truyenqqko.com/truyen-tranh/gintama-216" || en.LastSynced.IsZero() {
		t.Fatalf("registry not recorded: %+v ok=%v", en, ok)
	}
}

func TestSyncExecutor_DoesNotRecordSyncComments(t *testing.T) {
	root := t.TempDir()
	exec := &SyncExecutor{
		Root:        root,
		CookiesPath: t.TempDir() + "/cookies.json",
		RunState:    &RunState{},
		runFn:       func(context.Context, pipeline.Opts) error { return nil },
	}
	seedCookieFile(t, exec.CookiesPath)
	if _, err := exec.Run(context.Background(), pipeline.SyncComments, SyncInput{URL: "https://truyenqqko.com/truyen-tranh/gintama-216"}); err != nil {
		t.Fatal(err)
	}
	reg, _ := registry.Load(root)
	if _, ok := reg.Get("gintama-216"); ok {
		t.Fatal("sync_comments must not record to registry")
	}
}

// seedCookieFile writes a minimal cookies.json so the production
// fetcher.LoadCookieFile call inside SyncExecutor succeeds. The test
// fakes out runFn so the cookie's value is never actually used.
func seedCookieFile(t *testing.T, path string) {
	t.Helper()
	if err := UpdateClearance(path, "DUMMY", "", ""); err != nil {
		t.Fatal(err)
	}
}
