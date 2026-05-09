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
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/anhpham/downloader/internal/downloader"
	"github.com/anhpham/downloader/internal/fetcher"
	sourcesite "github.com/anhpham/downloader/internal/site/source"
)

func main() {
	out := flag.String("out", defaultOutDir(), "directory to write chapter folders into")
	concurrency := flag.Int("concurrency", 4, "chapters in flight at once")
	from := flag.Int("from", 0, "first chapter to download (0 = no lower bound)")
	to := flag.Int("to", 0, "last chapter to download (0 = no upper bound)")
	resume := flag.Bool("resume", false, "skip chapters whose folder already contains .done")
	verbose := flag.Bool("verbose", false, "print per-chapter progress to stderr")
	cookiesPath := flag.String("cookies", defaultCookiesPath(), "path to JSON cookie file (see README)")
	name := flag.String("name", "", "manga folder name (defaults to the URL slug)")
	flag.Usage = usage
	flag.Parse()

	if flag.NArg() != 1 {
		usage()
		os.Exit(2)
	}
	mangaURL := flag.Arg(0)

	slug, err := slugFromURL(mangaURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid manga url:", err)
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
		fmt.Fprintln(os.Stderr, "load cookies:", err)
		os.Exit(2)
	}

	f, err := fetcher.New(cf, fetcher.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "fetcher setup failed:", err)
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	logOut := io.Discard
	if *verbose {
		logOut = os.Stderr
	}
	logger := log.New(logOut, "", log.LstdFlags)

	d := &downloader.Downloader{
		Site:        &sourcesite.Site{Fetcher: f},
		Fetcher:     f,
		OutDir:      *out,
		MangaSlug:   slug,
		Concurrency: *concurrency,
		From:        *from,
		To:          *to,
		Resume:      *resume,
		Logger:      logger,
	}

	res, err := d.Run(ctx, mangaURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		if errors.Is(err, fetcher.ErrCloudflareExpired) {
			fmt.Fprintln(os.Stderr, "→ refresh cf_clearance in", *cookiesPath, "and re-run with --resume")
		}
		os.Exit(2)
	}

	fmt.Fprintf(os.Stderr, "Done: %d completed, %d skipped, %d failed.\n", res.Completed, res.Skipped, res.Failed)
	expired := false
	for _, fail := range res.Failures {
		fmt.Fprintf(os.Stderr, "  chap %s: %v\n", fail.Number, fail.Err)
		if errors.Is(fail.Err, fetcher.ErrCloudflareExpired) {
			expired = true
		}
	}
	if expired {
		fmt.Fprintln(os.Stderr, "→ refresh cf_clearance in", *cookiesPath, "and re-run with --resume")
	}
	if res.Failed > 0 {
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `Usage: downloader [flags] <manga-url>

Mirrors a manga from the configured source site to local disk. Each chapter
becomes one folder under <out>/<manga-slug>/.

Flags:
`)
	flag.PrintDefaults()
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
403 — refresh "cf_clearance" the same way and re-run with --resume.
`, p)
}
