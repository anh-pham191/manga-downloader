package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anhpham/downloader/internal/fetcher"
	"github.com/anhpham/downloader/internal/mcp"
	"github.com/anhpham/downloader/internal/pipeline"
	"github.com/anhpham/downloader/internal/registry"
	sourcesite "github.com/anhpham/downloader/internal/site/source"
)

// recordRun is called by the sync-manga / resume paths after a
// successful pipeline run so the registry learns the URL.
func recordRun(root, name, mangaURL string) {
	reg, err := registry.Load(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: registry unreadable, not recording:", err)
		return
	}
	reg.Upsert(name, mangaURL)
	reg.Touch(name, time.Now())
	if err := reg.Save(); err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not save registry:", err)
	}
}

// recordURL is called BEFORE a first-time sync-manga so a partial
// download is still resumable via update-all.
func recordURL(root, name, mangaURL string) {
	reg, err := registry.Load(root)
	if err != nil {
		return
	}
	if _, ok := reg.Get(name); ok {
		return
	}
	reg.Upsert(name, mangaURL)
	_ = reg.Save()
}

func runRegister(args []string) int {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
	fs.Parse(args) //nolint:errcheck
	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: downloader register [--out root] <name> <manga-url>")
		return 2
	}
	name, u := fs.Arg(0), fs.Arg(1)
	if _, err := slugFromURL(u); err != nil {
		fmt.Fprintln(os.Stderr, "invalid url:", err)
		return 2
	}
	reg, err := registry.Load(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	reg.Upsert(name, u)
	if err := reg.Save(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	archivePath := filepath.Join(*out, name+".cbz")
	if _, err := os.Stat(archivePath); errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "warning: no archive %s; update-all will report no-archive until sync-manga creates it\n", archivePath)
	}
	fmt.Printf("registered %s -> %s\n", name, u)
	return 0
}

func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
	fs.Parse(args) //nolint:errcheck
	reg, err := registry.Load(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tLAST SYNCED\tURL")
	for _, n := range reg.Names() {
		e, _ := reg.Get(n)
		last := "never"
		if !e.LastSynced.IsZero() {
			last = e.LastSynced.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", n, last, e.URL)
	}
	tw.Flush()
	return 0
}

func promptDomain(in io.Reader, out io.Writer) func(string, error) string {
	return func(oldHost string, cause error) string {
		fmt.Fprintf(out, "\nThe source host %s is unreachable:\n  %v\n", oldHost, cause)
		fmt.Fprintf(out, "This is NOT a Cloudflare/token error. If the site moved, enter the new host\n(e.g. truyenqqnew.com). Press Enter to abort: ")
		line, _ := bufio.NewReader(in).ReadString('\n')
		return strings.TrimSpace(line)
	}
}

func formatSummary(res pipeline.UpdateAllResult) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tNEW\tSTATUS")
	total, failed := 0, 0
	for _, o := range res.Outcomes {
		detail := o.Status
		if o.Err != nil && o.Status != "ok" {
			detail += ": " + o.Err.Error()
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\n", o.Name, o.NewChapters, detail)
		total += o.NewChapters
		if o.Status == "failed" || o.Status == "no-archive" {
			failed++
		}
	}
	tw.Flush()
	if res.DomainMoved {
		fmt.Fprintf(&b, "\nregistry host rewritten to %s\n", res.NewHost)
	}
	fmt.Fprintf(&b, "\nmanga: %d  new chapters: %d  failed: %d\n", len(res.Outcomes), total, failed)
	return b.String()
}

func runUpdateAll(args []string) int {
	fs := flag.NewFlagSet("update-all", flag.ExitOnError)
	out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
	cookiesPath := fs.String("cookies", defaultCookiesPath(), "path to cookie JSON")
	concurrency := fs.Int("concurrency", 4, "chapters in flight")
	domain := fs.String("domain", "", "new source host to apply if the stored one is unreachable (skips the prompt)")
	verbose := fs.Bool("verbose", false, "per-manga progress to stderr")
	fs.Parse(args) //nolint:errcheck

	reg, err := registry.Load(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(reg.Names()) == 0 {
		fmt.Fprintln(os.Stderr, "registry is empty; run `downloader discover` or `downloader register <name> <url>` first")
		return 0
	}

	cf, err := fetcher.LoadCookieFile(*cookiesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			printCookieInstructions(*cookiesPath)
			return 2
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	f, err := fetcher.New(cf, fetcher.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	firstName := reg.Names()[0]
	first, _ := reg.Get(firstName)
	ctx := context.Background()
	if healed, changed, herr := f.HealUserAgent(ctx, first.URL); herr != nil {
		if errors.Is(herr, fetcher.ErrCloudflareExpired) {
			fmt.Fprintln(os.Stderr, "→ refresh cf_clearance in", *cookiesPath, "and re-run `update-all`")
			return 1
		}
		// A transport failure here is handled by UpdateAll's preflight; only
		// bail on non-transport errors.
		if fetcher.Classify(herr) != fetcher.KindHostUnreachable {
			fmt.Fprintln(os.Stderr, herr)
			return 1
		}
	} else if changed {
		fmt.Fprintln(os.Stderr, "user-agent drifted; healed to:", healed)
		_ = fetcher.SaveUserAgent(*cookiesPath, healed)
	}

	var logger *log.Logger
	if *verbose {
		logger = log.New(os.Stderr, "", log.LstdFlags)
	}
	ask := promptDomain(os.Stdin, os.Stderr)
	if *domain != "" {
		d := *domain
		ask = func(string, error) string { return d }
	}

	res, err := pipeline.UpdateAll(ctx, pipeline.UpdateAllOpts{
		Root:        *out,
		Registry:    reg,
		Site:        &sourcesite.Site{Fetcher: f},
		Fetcher:     f,
		Concurrency: *concurrency,
		Logger:      logger,
		AskDomain:   ask,
	})
	fmt.Print(formatSummary(res))
	switch {
	case err == nil:
		for _, o := range res.Outcomes {
			if o.Status == "failed" || o.Status == "no-archive" {
				return 1
			}
		}
		return 0
	case errors.Is(err, fetcher.ErrCloudflareExpired):
		fmt.Fprintln(os.Stderr, "→ refresh cf_clearance in", *cookiesPath, "and re-run `update-all`")
		return 1
	case errors.Is(err, pipeline.ErrDomainMoved):
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr, "→ re-run with --domain <newhost> once you know the new address")
		return 1
	default:
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
}

func runDiscover(args []string) int {
	fs := flag.NewFlagSet("discover", flag.ExitOnError)
	out := fs.String("out", defaultOutDir(), "root directory for .cbz archives")
	cookiesPath := fs.String("cookies", defaultCookiesPath(), "path to cookie JSON")
	base := fs.String("site", "https://truyenqqko.com/", "any URL on the source site (used to derive the host)")
	fs.Parse(args) //nolint:errcheck

	reg, err := registry.Load(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	all, err := mcp.ListManga(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	var missing []string
	for _, m := range all {
		if _, ok := reg.Get(m.Name); !ok {
			missing = append(missing, m.Name)
		}
	}
	if len(missing) == 0 {
		fmt.Println("every archive is already registered")
		return 0
	}

	cf, err := fetcher.LoadCookieFile(*cookiesPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			printCookieInstructions(*cookiesPath)
			return 2
		}
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	f, err := fetcher.New(cf, fetcher.Options{})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	s := &sourcesite.Site{Fetcher: f}
	ctx := context.Background()
	var none []string
	for _, name := range missing {
		hits, err := s.Search(ctx, *base, name)
		if err != nil {
			if errors.Is(err, fetcher.ErrCloudflareExpired) {
				fmt.Fprintln(os.Stderr, "→ refresh cf_clearance in", *cookiesPath)
				return 1
			}
			fmt.Fprintf(os.Stderr, "%s: search failed: %v\n", name, err)
			none = append(none, name)
			continue
		}
		if len(hits) == 0 {
			none = append(none, name)
			continue
		}
		fmt.Printf("\n%s\n", name)
		for i, h := range hits {
			if i == 3 {
				break
			}
			fmt.Printf("  %d. %s\n     %s\n", i+1, h.Title, h.URL)
		}
	}
	if len(none) > 0 {
		fmt.Println("\nno search hits (paste a URL with `downloader register \"<name>\" <url>`):")
		for _, n := range none {
			fmt.Println("  " + n)
		}
	}
	fmt.Println("\nconfirm with: downloader register \"<name>\" <url>")
	return 0
}
