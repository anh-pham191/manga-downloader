package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/mcp"
	"github.com/anhpham/downloader/internal/pipeline"
	sourcesite "github.com/anhpham/downloader/internal/site/source"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if os.Args[1] == "mcp" {
		if err := runMCP(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	mode, ok := parseMode(os.Args[1])
	if !ok {
		usage()
		os.Exit(2)
	}

	fs := flag.NewFlagSet(os.Args[1], flag.ExitOnError)
	out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
	concurrency := fs.Int("concurrency", 4, "chapters in flight")
	from := fs.Int("from", 0, "first chapter number (0 = no lower bound)")
	to := fs.Int("to", 0, "last chapter number (0 = no upper bound)")
	verbose := fs.Bool("verbose", false, "per-chapter progress to stderr")
	cookiesPath := fs.String("cookies", defaultCookiesPath(), "path to cookie JSON")
	name := fs.String("name", "", "archive name (defaults to URL slug)")
	fs.Usage = usage
	fs.Parse(os.Args[2:]) //nolint:errcheck // ExitOnError handles errors

	if fs.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	mangaURL := fs.Arg(0)

	slug, err := slugFromURL(mangaURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid url:", err)
		os.Exit(2)
	}
	if *name != "" {
		slug = *name
	}

	cf, err := fetcher.LoadCookieFile(*cookiesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			printCookieInstructions(*cookiesPath)
			os.Exit(2)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	f, err := fetcher.New(cf, fetcher.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	site := &sourcesite.Site{Fetcher: f}

	err = pipeline.Run(context.Background(), pipeline.Opts{
		Mode:        mode,
		MangaURL:    mangaURL,
		Root:        *out,
		Name:        slug,
		From:        *from,
		To:          *to,
		Concurrency: *concurrency,
		Site:        site,
		Fetcher:     f,
		Verbose:     *verbose,
		Logger:      log.New(os.Stderr, "", 0),
	})
	switch {
	case err == nil:
		return
	case errors.Is(err, pipeline.ErrAnotherInstance):
		fmt.Fprintln(os.Stderr, "another downloader is running for", slug)
		os.Exit(2)
	case errors.Is(err, pipeline.ErrNoArchive):
		fmt.Fprintln(os.Stderr,
			"no archive at", *out+"/"+slug+".cbz",
			"— run `downloader sync-manga` first")
		os.Exit(0)
	case errors.Is(err, fetcher.ErrCloudflareExpired):
		fmt.Fprintln(os.Stderr,
			"→ refresh cf_clearance in", *cookiesPath,
			"and re-run with `resume` or `sync-manga`")
		os.Exit(1)
	default:
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseMode(s string) (pipeline.Mode, bool) {
	switch s {
	case "sync-comments":
		return pipeline.SyncComments, true
	case "resume":
		return pipeline.Resume, true
	case "sync-manga":
		return pipeline.SyncManga, true
	}
	return 0, false
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  downloader sync-manga    [flags] <manga-url>   download new chapters + comments + backfill missing comments
  downloader resume        [flags] <manga-url>   download new chapters + comments (no backfill)
  downloader sync-comments [flags] <manga-url>   backfill comments on existing archive (no new chapters)
  downloader mcp           [flags]               run local MCP server over stdio for Claude Desktop

flags:
  --out string           root directory (default: ~/Documents/Manga)
  --name string          archive name (default: URL slug)
  --concurrency int      chapters in flight (default 4)
  --from int             first chapter
  --to int               last chapter
  --cookies string       path to cookie JSON
  --verbose              per-chapter progress`)
}

func runMCP(args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	root := fs.String("out", defaultOutDir(), "manga root directory")
	cookies := fs.String("cookies", defaultCookiesPath(), "path to cookies.json")
	verbose := fs.Bool("verbose", false, "verbose logging to stderr")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var logger *log.Logger
	if *verbose {
		logger = log.New(os.Stderr, "mcp: ", log.LstdFlags)
	} else {
		logger = log.New(io.Discard, "", 0)
	}
	srv, err := mcp.New(mcp.Opts{
		Root:        *root,
		CookiesPath: *cookies,
		Logger:      logger,
	})
	if err != nil {
		return err
	}
	return srv.Serve(context.Background())
}

func defaultOutDir() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, "Documents", "Manga")
	}
	return "./downloads"
}

func defaultCookiesPath() string {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return "./cookies.json"
	}
	return filepath.Join(cfg, "downloader", "cookies.json")
}

// slugFromURL takes the last non-empty path segment as the manga slug.
func slugFromURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host in %q", raw)
	}
	clean := strings.Trim(u.Path, "/")
	if clean == "" {
		return "", fmt.Errorf("missing path in %q", raw)
	}
	return path.Base(clean), nil
}

func printCookieInstructions(p string) {
	fmt.Fprintf(os.Stderr, `No cookie file at %s.

To create one:

  1. Open the manga URL in your normal browser.
  2. Solve the "Verify you are human" checkbox if it appears.
  3. Open DevTools (Cmd+Opt+I).
     • Application → Storage → Cookies → the source site
       Copy the value of "cf_clearance".
     • Network tab → click any request → Headers → "User-Agent".
  4. Save them as JSON at the path above:

  {
    "user_agent": "Mozilla/5.0 …(paste exactly)…",
    "cookies": [
      { "name": "cf_clearance", "value": "…(paste)…", "domain": ".example.com" }
    ]
  }

The cookie usually lasts a few hours. When it expires you'll see a
403 — refresh "cf_clearance" the same way and re-run with resume or sync-manga.
`, p)
}
